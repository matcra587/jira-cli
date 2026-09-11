package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Comment full lifecycle:
//   - comment list contract (oldest-first, pagination, lossy warnings)
//   - comment list --all rate-limit-during-pagination: partial data + warning + exit 0
//   - comment add contract incl. visibility plumbing + back-compat alias
//   - comment edit contract incl. visibility replace/preserve/clear semantics + author preserved
//   - comment delete contract: --force gate under --no-input + envelope shape
//   - unit-style flag-validation in command_schemas (mutex visibility)
//   - empty-body rejection
//   - per-comment lossy warnings on comment list

// commentEnvelope is the per-test envelope shape used by the comment contract tests.
// The shared helper `testEnvelope` in headless_contract_test.go does not
// surface `data` as a typed map nor expose `warnings[]`, both of which the
// comment-list tests need to assert on. Keeping a local type avoids touching
// the shared helper; that helper is reused for envelope-meta assertions only.
type commentEnvelope struct {
	Meta     map[string]any   `json:"meta"`
	Data     map[string]any   `json:"data"`
	Errors   []map[string]any `json:"errors"`
	Warnings []map[string]any `json:"warnings"`
}

func decodeCommentEnvelope(t *testing.T, raw []byte) commentEnvelope {
	t.Helper()
	var env commentEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, raw)
	}
	return env
}

// commentTestServer wraps httptest with a small route registry. Handlers are
// keyed by `METHOD path` exactly as Atlassian exposes them; unmatched routes
// fail the test loudly so we never accidentally hit a wrong endpoint.
type commentTestServer struct {
	t        *testing.T
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	routes   map[string]http.HandlerFunc
}

func newCommentServer(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *commentTestServer) {
	t.Helper()
	cts := &commentTestServer{t: t, routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cts.mu.Lock()
		cts.requests = append(cts.requests, r)
		cts.bodies = append(cts.bodies, body)
		cts.mu.Unlock()
		// Re-attach the body so handlers can read it.
		r.Body = io.NopCloser(bytes.NewReader(body))

		key := r.Method + " " + r.URL.Path
		h, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, cts
}

func (c *commentTestServer) RequestBodies() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.bodies))
	copy(out, c.bodies)
	return out
}

func (c *commentTestServer) Requests() []*http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*http.Request, len(c.requests))
	copy(out, c.requests)
	return out
}

// runJira invokes the CLI binary with the given args. Stdout and stderr are
// captured separately; the test asserts on each. Exit code surfaces via the
// returned ExitError (nil on success).
func runJira(t *testing.T, args ...string) (stdout, stderr []byte, exitCode int) {
	t.Helper()
	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return so.Bytes(), se.Bytes(), ee.ExitCode()
		}
		t.Fatalf("jira %v: %v\nstderr=%s", args, err, se.String())
	}
	return so.Bytes(), se.Bytes(), 0
}

// ---------- comment list contract ----------

func TestCommentListReturnsEnvelopeWithPaginationAndOrdering(t *testing.T) {
	// Real tenants OMIT isLast on this endpoint — the fixture omits it too,
	// pinning the computed startAt+returned>=total boundary instead of a
	// decoded-false "more pages" lie.
	page := `{
		"comments": [
			{"id":"100","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]},"author":{"accountId":"u1","displayName":"Alice"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-04-01T10:00:00.000+0000"},
			{"id":"101","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"second"}]}]},"author":{"accountId":"u2","displayName":"Bob"},"created":"2026-04-02T10:00:00.000+0000","updated":"2026-04-02T11:00:00.000+0000"}
		],
		"startAt":0,
		"maxResults":50,
		"total":2
	}`
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{
		"GET /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("orderBy"); got != "created" {
				t.Errorf("orderBy = %q; want created", got)
			}
			_, _ = io.WriteString(w, page)
		},
	})

	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "list", "PROJ-1", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)

	issue, _ := env.Data["issue"].(map[string]any)
	if issue["key"] != "PROJ-1" {
		t.Fatalf("data.issue = %v, want key PROJ-1: %s", issue, stdout)
	}
	comments, _ := env.Data["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("comments len = %d; want 2: %s", len(comments), stdout)
	}
	first, _ := comments[0].(map[string]any)
	if first["id"] != "100" {
		t.Errorf("first comment id = %v; want 100 (oldest-first)", first["id"])
	}

	pagination, ok := env.Meta["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("meta.pagination missing (the canonical location): %s", stdout)
	}
	if _, hasOld := env.Data["pagination"]; hasOld {
		t.Fatalf("pagination must live in meta, not data: %s", stdout)
	}
	if pagination["total"] != float64(2) {
		t.Errorf("pagination.total = %v; want 2 (endpoint-reported)", pagination["total"])
	}
	if pagination["startAt"] == nil || pagination["maxResults"] == nil {
		t.Errorf("pagination missing required fields: %v", pagination)
	}
	if pagination["isLast"] != true {
		t.Errorf("pagination.isLast = %v; want true (startAt+returned covers total even without a wire isLast)", pagination["isLast"])
	}
	if cursor, has := pagination["nextCursor"]; has && cursor != "" {
		t.Errorf("complete single page must not carry a cursor: %v", cursor)
	}

	if len(cts.Requests()) != 1 {
		t.Errorf("requests = %d; want 1", len(cts.Requests()))
	}
}

func TestCommentListEmptyKeepsIssueAndNonNullComments(t *testing.T) {
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"GET /rest/api/3/issue/EMPTY-1/comment": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"comments":[],"startAt":0,"maxResults":50,"total":0}`)
		},
	})

	stdout, stderr, code := runJira(
		t,
		"--config", jiraConfig(t, srv.URL),
		"--output=json",
		"issue", "comment", "list", "EMPTY-1",
	)
	if code != 0 {
		t.Fatalf("comment list exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	env := decodeCommentEnvelope(t, stdout)
	issue, _ := env.Data["issue"].(map[string]any)
	if issue["key"] != "EMPTY-1" {
		t.Fatalf("data.issue = %v, want key EMPTY-1: %s", issue, stdout)
	}
	comments, ok := env.Data["comments"].([]any)
	if !ok || comments == nil || len(comments) != 0 {
		t.Fatalf("data.comments = %#v, want non-null empty array: %s", env.Data["comments"], stdout)
	}
}

func TestCommentListAllPaginatesUntilIsLast(t *testing.T) {
	page1 := `{"comments":[{"id":"100","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-04-01T10:00:00.000+0000"}],"startAt":0,"maxResults":1,"total":2,"isLast":false}`
	page2 := `{"comments":[{"id":"101","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-02T10:00:00.000+0000","updated":"2026-04-02T10:00:00.000+0000"}],"startAt":1,"maxResults":1,"total":2,"isLast":true}`
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"GET /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, r *http.Request) {
			startAt := r.URL.Query().Get("startAt")
			switch startAt {
			case "", "0":
				_, _ = io.WriteString(w, page1)
			case "1":
				_, _ = io.WriteString(w, page2)
			default:
				t.Errorf("unexpected startAt %q", startAt)
			}
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "list", "PROJ-1", "--all", "--limit", "1", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)
	comments, _ := env.Data["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("--all comments len = %d; want 2: %s", len(comments), stdout)
	}
	pagination, _ := env.Meta["pagination"].(map[string]any)
	if pagination["isLast"] != true {
		t.Errorf("after --all, pagination.isLast = %v; want true", pagination["isLast"])
	}
}

// ---------- T040a: rate-limit during pagination ----------

func TestCommentListAllRateLimitDuringPaginationReturnsPartialData(t *testing.T) {
	page1 := `{"comments":[{"id":"100","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-04-01T10:00:00.000+0000"}],"startAt":0,"maxResults":1,"total":3,"isLast":false}`
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"GET /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("startAt") == "1" {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"errorMessages":["rate limited"]}`)
				return
			}
			_, _ = io.WriteString(w, page1)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "list", "PROJ-1", "--all", "--limit", "1", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0 (partial data on rate-limit)\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)
	comments, _ := env.Data["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("partial comments len = %d; want 1: %s", len(comments), stdout)
	}
	pagination, _ := env.Meta["pagination"].(map[string]any)
	if pagination["isLast"] != false {
		t.Errorf("pagination.isLast = %v; want false (partial)", pagination["isLast"])
	}
	if pagination["nextCursor"] == nil || pagination["nextCursor"] == "" {
		t.Errorf("pagination.nextCursor missing on partial: %v", pagination)
	}

	found := false
	for _, w := range env.Warnings {
		if w["type"] == "rate-limit-during-paginate" {
			if w["pages_fetched"] == nil || w["retry_after_seconds"] == nil {
				t.Errorf("rate-limit warning missing fields: %v", w)
			}
			if got := int(w["retry_after_seconds"].(float64)); got != 30 {
				t.Errorf("retry_after_seconds = %d, want 30", got)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no rate-limit-during-paginate warning: %+v", env.Warnings)
	}
}

// ---------- comment add contract + back-compat alias ----------

func TestCommentAddSubmitsADFAndReturnsEnvelope(t *testing.T) {
	var addBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"POST /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, r *http.Request) {
			addBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"42","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]},"author":{"accountId":"u","displayName":"A"},"created":"2026-05-05T11:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000"}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "add", "PROJ-1", "--markdown", "hello", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)
	comment, _ := env.Data["comment"].(map[string]any)
	if comment["id"] != "42" {
		t.Fatalf("data.comment.id = %v; want 42", comment["id"])
	}

	var sent map[string]any
	if err := json.Unmarshal(addBody, &sent); err != nil {
		t.Fatalf("decode add body: %v: %s", err, addBody)
	}
	body, ok := sent["body"].(map[string]any)
	if !ok || body["type"] != "doc" {
		t.Fatalf("wire body is not ADF: %s", addBody)
	}
}

func TestCommentAddVisibilityRolePlumbsToWire(t *testing.T) {
	var addBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"POST /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, r *http.Request) {
			addBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"43","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-05-05T11:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000","visibility":{"type":"role","value":"Developers"}}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "add", "PROJ-1", "--markdown", "hi", "--visibility-role", "Developers", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0", code)
	}
	var sent map[string]any
	if err := json.Unmarshal(addBody, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	vis, ok := sent["visibility"].(map[string]any)
	if !ok {
		t.Fatalf("wire body missing visibility: %s", addBody)
	}
	if vis["type"] != "role" || vis["value"] != "Developers" {
		t.Errorf("visibility = %v; want {type:role, value:Developers}", vis)
	}
}

func TestCommentAddBackCompatAliasMatchesAddSubcommand(t *testing.T) {
	mu := sync.Mutex{}
	var bodies [][]byte
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"99","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-05-05T11:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000"}`)
	}
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"POST /rest/api/3/issue/PROJ-1/comment": handler,
	})
	cfg := jiraConfig(t, srv.URL)

	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "PROJ-1", "--markdown", "alias", "--output=json")
	if code != 0 {
		t.Fatalf("alias form exit = %d; want 0", code)
	}
	_, _, code = runJira(t, "--config", cfg, "issue", "comment", "add", "PROJ-1", "--markdown", "alias", "--output=json")
	if code != 0 {
		t.Fatalf("add subcommand exit = %d; want 0", code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("got %d wire bodies; want 2", len(bodies))
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Errorf("alias body diverges from add subcommand body:\n  alias = %s\n  add   = %s", bodies[0], bodies[1])
	}
}

// ---------- comment edit contract ----------

func TestCommentEditPreservesAuthorAndPlumbsBody(t *testing.T) {
	var editBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"PUT /rest/api/3/issue/PROJ-1/comment/55": func(w http.ResponseWriter, r *http.Request) {
			editBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"55","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"fixed"}]}]},"author":{"accountId":"original","displayName":"Original"},"updateAuthor":{"accountId":"caller","displayName":"Caller"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-05-05T11:22:33.000+0000"}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55", "--markdown", "fixed", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0\nstdout=%s", code, stdout)
	}

	var sent map[string]any
	if err := json.Unmarshal(editBody, &sent); err != nil {
		t.Fatalf("decode edit body: %v", err)
	}
	if _, has := sent["author"]; has {
		t.Errorf("edit wire body must not include author: %s", editBody)
	}

	env := decodeCommentEnvelope(t, stdout)
	comment, _ := env.Data["comment"].(map[string]any)
	author, _ := comment["author"].(map[string]any)
	if author["account_id"] != "original" {
		t.Errorf("envelope comment.author.account_id = %v; want original", author["account_id"])
	}
	upd, _ := comment["update_author"].(map[string]any)
	if upd == nil || upd["account_id"] != "caller" {
		t.Errorf("envelope comment.update_author.account_id = %v; want caller", upd)
	}
	for field, want := range map[string]any{
		"comment_id":        "55",
		"visibility_change": "keep",
	} {
		if env.Data[field] != want {
			t.Errorf("data.%s = %#v, want %#v", field, env.Data[field], want)
		}
	}
	if _, exists := env.Data["body_adf_summary"].(map[string]any); !exists {
		t.Errorf("live edit missing body_adf_summary: %s", stdout)
	}
}

func TestCommentEditVisibilityReplaceOnSupply(t *testing.T) {
	var editBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"PUT /rest/api/3/issue/PROJ-1/comment/55": func(w http.ResponseWriter, r *http.Request) {
			editBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"55","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000","visibility":{"type":"role","value":"Admins"}}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55", "--markdown", "x", "--visibility-role", "Admins", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var sent map[string]any
	if err := json.Unmarshal(editBody, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	vis, ok := sent["visibility"].(map[string]any)
	if !ok || vis["type"] != "role" || vis["value"] != "Admins" {
		t.Errorf("visibility plumb = %v; want {type:role, value:Admins}", sent["visibility"])
	}
}

func TestCommentEditPreserveWhenOmitted(t *testing.T) {
	var editBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"PUT /rest/api/3/issue/PROJ-1/comment/55": func(w http.ResponseWriter, r *http.Request) {
			editBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"55","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000"}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55", "--markdown", "x", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var sent map[string]any
	if err := json.Unmarshal(editBody, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, has := sent["visibility"]; has {
		t.Errorf("preserve-when-omitted: visibility key must NOT appear on wire: %s", editBody)
	}
}

func TestCommentEditClearVisibilitySendsNull(t *testing.T) {
	var editBody []byte
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"PUT /rest/api/3/issue/PROJ-1/comment/55": func(w http.ResponseWriter, r *http.Request) {
			editBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"id":"55","body":{"type":"doc","version":1,"content":[]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-05-05T11:00:00.000+0000"}`)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55", "--markdown", "x", "--clear-visibility", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !bytes.Contains(editBody, []byte(`"visibility":null`)) && !bytes.Contains(editBody, []byte(`"visibility": null`)) {
		t.Errorf("--clear-visibility did not send visibility:null on the wire: %s", editBody)
	}
}

func TestCommentEditMutuallyExclusiveFlagsExit3(t *testing.T) {
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55",
		"--markdown", "x", "--visibility-role", "Admins", "--clear-visibility", "--output=json")
	if code != 3 {
		t.Fatalf("exit = %d; want 3 (validation)", code)
	}
	if len(cts.Requests()) != 0 {
		t.Errorf("HTTP call made despite mutex-flag validation error: %d requests", len(cts.Requests()))
	}

	_, _, code2 := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55",
		"--markdown", "x", "--visibility-role", "Admins", "--visibility-group", "Eng", "--output=json")
	if code2 != 3 {
		t.Fatalf("role+group exit = %d; want 3", code2)
	}
}

// ---------- comment delete contract ----------

func TestCommentDeleteForceGatedUnderNoInput(t *testing.T) {
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "delete", "PROJ-1", "55", "--no-input", "--output=json")
	if code != 3 {
		t.Fatalf("exit = %d; want 3 (force omitted under --no-input)", code)
	}
	if len(cts.Requests()) != 0 {
		t.Errorf("HTTP call made despite missing --force: %d", len(cts.Requests()))
	}
}

func TestCommentDeleteWithForceReturnsEnvelope(t *testing.T) {
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"DELETE /rest/api/3/issue/PROJ-1/comment/55": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "delete", "PROJ-1", "55", "--force", "--no-input", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d; want 0\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)
	if env.Data["comment_id"] != "55" {
		t.Errorf("data.comment_id = %v; want 55", env.Data["comment_id"])
	}
	if env.Data["deleted"] != true {
		t.Errorf("data.deleted = %v; want true", env.Data["deleted"])
	}
}

// ---------- lossy ADF surfaces warnings ----------

func TestCommentListEmitsLossyWarningPerComment(t *testing.T) {
	page := `{
		"comments":[
			{"id":"200","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"plain"}]}]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-01T10:00:00.000+0000","updated":"2026-04-01T10:00:00.000+0000"},
			{"id":"201","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"placeholder","attrs":{"text":"p"}},{"type":"text","text":"see"}]}]},"author":{"accountId":"u","displayName":"A"},"created":"2026-04-02T10:00:00.000+0000","updated":"2026-04-02T10:00:00.000+0000"}
		],
		"startAt":0,"maxResults":50,"total":2,"isLast":true
	}`
	srv, _ := newCommentServer(t, map[string]http.HandlerFunc{
		"GET /rest/api/3/issue/PROJ-1/comment": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, page)
		},
	})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "list", "PROJ-1", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout=%s", code, stdout)
	}
	env := decodeCommentEnvelope(t, stdout)

	matched := 0
	for _, w := range env.Warnings {
		if w["comment_id"] == "201" {
			matched++
			lc, _ := w["lossy_constructs"].([]any)
			if len(lc) == 0 {
				t.Errorf("warning has empty lossy_constructs: %v", w)
			}
			found := false
			for _, c := range lc {
				if c == "placeholder" {
					found = true
				}
			}
			if !found {
				t.Errorf("lossy_constructs missing placeholder: %v", lc)
			}
		}
	}
	if matched != 1 {
		t.Errorf("warnings for comment 201 = %d; want 1: %+v", matched, env.Warnings)
	}
	for _, w := range env.Warnings {
		if w["comment_id"] == "200" {
			t.Errorf("unexpected warning for non-lossy comment 200: %v", w)
		}
	}
}

// ---------- T044a: empty-body validation (Edge Case) ----------

func TestCommentAddEmptyBodyMarkdownRejectedLocally(t *testing.T) {
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{})
	cfg := jiraConfig(t, srv.URL)
	stdout, _, code := runJira(t, "--config", cfg, "issue", "comment", "add", "PROJ-1", "--markdown", "", "--no-input", "--output=json")
	if code != 3 {
		t.Fatalf("exit = %d; want 3 (empty body)\nstdout=%s", code, stdout)
	}
	if len(cts.Requests()) != 0 {
		t.Errorf("HTTP call made despite empty body: %d", len(cts.Requests()))
	}
	// Machine mode: the empty-body reason is in the stdout envelope.
	if !strings.Contains(strings.ToLower(string(stdout)), "comment body") && !strings.Contains(strings.ToLower(string(stdout)), "body is required") {
		t.Errorf("envelope should mention the empty-body reason: %s", stdout)
	}
}

func TestCommentAddEmptyJSONInputRejectedLocally(t *testing.T) {
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{})
	cfg := jiraConfig(t, srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(`{"type":"doc","version":1,"content":[]}`), 0o600); err != nil {
		t.Fatalf("write empty.json: %v", err)
	}
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "add", "PROJ-1", "--json-input", path, "--no-input", "--output=json")
	if code != 3 {
		t.Fatalf("exit = %d; want 3 (empty json-input doc)", code)
	}
	if len(cts.Requests()) != 0 {
		t.Errorf("HTTP call made despite empty body: %d", len(cts.Requests()))
	}
}

func TestCommentEditWithoutBodyOrJSONInputRejectedLocally(t *testing.T) {
	srv, cts := newCommentServer(t, map[string]http.HandlerFunc{})
	cfg := jiraConfig(t, srv.URL)
	_, _, code := runJira(t, "--config", cfg, "issue", "comment", "edit", "PROJ-1", "55", "--no-input", "--output=json")
	if code != 3 {
		t.Fatalf("exit = %d; want 3 (no body)", code)
	}
	if len(cts.Requests()) != 0 {
		t.Errorf("HTTP call made despite missing body: %d", len(cts.Requests()))
	}
}
