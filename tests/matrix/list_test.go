//go:build flagmatrix

package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file carries two things: the mx3 execution harness (the compiled
// binary, the scrubbed environment, the request recorder — corpus.go's
// shape, duplicated because tests/audit is build-tagged outputaudit and
// this package cannot import it), and the `jira issue list` cases.
//
// Every case here asserts the flag's OBSERVABLE effect — the request the
// stub received, the envelope, or a refusal by taxonomy code — never
// mere exit-0. A case that would still pass with the flag's wiring cut
// is the defect this matrix exists to end.

// mx3Bin compiles the real binary exactly once per test process. The
// build directory is deliberately left to the OS's temp cleanup: this
// package's TestMain is harness-owned and cannot host a cleanup hook.
var (
	mx3BinOnce sync.Once
	mx3BinPath string
	mx3BinErr  error
)

func mx3Binary(t *testing.T) string {
	t.Helper()
	mx3BinOnce.Do(func() {
		rootDir, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			mx3BinErr = err
			return
		}
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err != nil {
			mx3BinErr = fmt.Errorf("resolve repo root: %s has no go.mod: %w", rootDir, err)
			return
		}
		dir, err := os.MkdirTemp("", "jira-matrix-bin-")
		if err != nil {
			mx3BinErr = err
			return
		}
		mx3BinPath = filepath.Join(dir, "jira")
		if runtime.GOOS == "windows" {
			mx3BinPath += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", mx3BinPath, "./cmd/jira")
		cmd.Dir = rootDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			mx3BinErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	if mx3BinErr != nil {
		t.Fatalf("flagmatrix: %v", mx3BinErr)
	}
	return mx3BinPath
}

// mx3Env is the scrubbed allowlist every invocation runs under —
// corpus.go's scenarioEnv, minus the color modes this suite never
// captures. baseURL is REQUIRED: base_url can come from the keyring, so
// HOME/XDG alone do not isolate this CLI, and every run must aim at an
// explicit loopback stub.
func mx3Env(workdir, baseURL string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workdir,
		"TMPDIR=" + workdir,
		"XDG_CONFIG_HOME=" + filepath.Join(workdir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(workdir, "cache"),
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"NO_PROXY=127.0.0.1,localhost",
		"COLUMNS=100",
		"TERM=dumb",
		"NO_COLOR=1",
		"JIRA_KEYRING=false",
		"DBUS_SESSION_BUS_ADDRESS=/dev/null",
		"XDG_RUNTIME_DIR=" + workdir,
		"JIRA_EMAIL=person@example.com",
		"JIRA_API_TOKEN=matrix-token",
		"JIRA_BASE_URL=" + baseURL,
	}
	if runtime.GOOS == "windows" {
		env = append(env,
			"PATHEXT="+os.Getenv("PATHEXT"), "USERPROFILE="+workdir,
			"APPDATA="+filepath.Join(workdir, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(workdir, "AppData", "Local"))
	}
	return env
}

// mx3Result is one invocation's outcome.
type mx3Result struct {
	Stdout string
	Stderr string
	Exit   int
}

// mx3Run executes one invocation in workdir against baseURL's stub.
// stdin is a pipe (never a terminal), so Interactive is false in every
// run — which is what makes the --force refusal below reachable.
func mx3Run(t *testing.T, workdir, baseURL string, args ...string) mx3Result {
	t.Helper()
	bin := mx3Binary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = mx3Env(workdir, baseURL)
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run %v: %v\nstderr: %s", args, err, stderr.String())
		}
		code = exit.ExitCode()
	}
	return mx3Result{Stdout: stdout.String(), Stderr: stderr.String(), Exit: code}
}

// mx3Envelope is the machine frame's slice this suite branches on.
type mx3Envelope struct {
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	} `json:"errors"`
	Warnings []struct {
		Type string `json:"type"`
	} `json:"warnings"`
}

func mx3Decode(t *testing.T, res mx3Result) mx3Envelope {
	t.Helper()
	var env mx3Envelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("decode envelope: %v\nstdout: %s\nstderr: %s", err, res.Stdout, res.Stderr)
	}
	return env
}

// mx3Data decodes the envelope's data slot into a generic map.
func mx3Data(t *testing.T, res mx3Result) map[string]any {
	t.Helper()
	env := mx3Decode(t, res)
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v\nstdout: %s", err, res.Stdout)
	}
	return data
}

// mx3RequireRefusal asserts a run failed with the given taxonomy code on
// the given field — the refusal shape of proof: exit non-zero AND the
// classified code, never message text.
func mx3RequireRefusal(t *testing.T, res mx3Result, code, field string) {
	t.Helper()
	if res.Exit == 0 {
		t.Fatalf("expected a refusal, got exit 0\nstdout: %s", res.Stdout)
	}
	env := mx3Decode(t, res)
	if len(env.Errors) == 0 {
		t.Fatalf("refusal envelope carries no errors\nstdout: %s", res.Stdout)
	}
	if env.Errors[0].Code != code {
		t.Fatalf("refusal code = %q, want %q\nstdout: %s", env.Errors[0].Code, code, res.Stdout)
	}
	if field != "" && env.Errors[0].Field != field {
		t.Fatalf("refusal field = %q, want %q\nstdout: %s", env.Errors[0].Field, field, res.Stdout)
	}
}

// mx3Request is one recorded stub-side request.
type mx3Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
}

// mx3Recorder collects every request a stub handler saw.
type mx3Recorder struct {
	mu   sync.Mutex
	reqs []mx3Request
}

func (r *mx3Recorder) record(req *http.Request) mx3Request {
	body, _ := io.ReadAll(req.Body) // a short read surfaces as a failed body assertion
	rec := mx3Request{Method: req.Method, Path: req.URL.Path, Query: req.URL.Query(), Body: body}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, rec)
	return rec
}

func (r *mx3Recorder) all() []mx3Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mx3Request(nil), r.reqs...)
}

func (r *mx3Recorder) byPath(path string) []mx3Request {
	var out []mx3Request
	for _, req := range r.all() {
		if req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

// mx3Stub serves handler on a loopback listener for the test's life.
func mx3Stub(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// mx3RecordOnly is the stub for invocations that must contact NOTHING:
// it records whatever arrives (so the failure names the leak) and
// refuses it. The assertion is mx3RequireNoRequests.
func mx3RecordOnly(rec *mx3Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		http.Error(w, `{"errorMessages":["the stub expected no traffic at all"]}`, http.StatusBadRequest)
	})
}

func mx3RequireNoRequests(t *testing.T, rec *mx3Recorder) {
	t.Helper()
	if reqs := rec.all(); len(reqs) != 0 {
		t.Fatalf("expected no requests, stub saw %d (first: %s %s)", len(reqs), reqs[0].Method, reqs[0].Path)
	}
}

const (
	mx3SearchPath = "/rest/api/3/search/jql"
	mx3CountPath  = "/rest/api/3/search/approximate-count"
)

// mx3SearchBody is the search endpoint's request shape, as the stub
// reads it back off the wire.
type mx3SearchBody struct {
	JQL           string   `json:"jql"`
	NextPageToken string   `json:"nextPageToken"`
	MaxResults    int      `json:"maxResults"`
	Fields        []string `json:"fields"`
}

// mx3ListHandler answers the search and approximate-count endpoints with
// a minimal single-issue final page, recording everything.
func mx3ListHandler(rec *mx3Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.Path == mx3SearchPath:
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"id":"1","key":"KEY-1","fields":{"summary":"One"}}]}`)
		case req.Method == http.MethodPost && req.Path == mx3CountPath:
			_, _ = fmt.Fprint(w, `{"count":7}`)
		default:
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
		}
	})
}

// mx3SearchRequest returns THE one search request a run made, decoded.
func mx3SearchRequest(t *testing.T, rec *mx3Recorder) mx3SearchBody {
	t.Helper()
	reqs := rec.byPath(mx3SearchPath)
	if len(reqs) != 1 {
		t.Fatalf("search requests = %d, want exactly 1", len(reqs))
	}
	var body mx3SearchBody
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("decode search body: %v\nbody: %s", err, reqs[0].Body)
	}
	return body
}

// mx3ListJQL runs `issue list` with args and returns the JQL string the
// stub's search endpoint received.
func mx3ListJQL(t *testing.T, args ...string) mx3SearchBody {
	t.Helper()
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3ListHandler(rec))
	argv := append([]string{"issue", "list", "--output=json"}, args...)
	res := mx3Run(t, t.TempDir(), baseURL, argv...)
	if res.Exit != 0 {
		t.Fatalf("issue list %v: exit %d\nstdout: %s\nstderr: %s", args, res.Exit, res.Stdout, res.Stderr)
	}
	return mx3SearchRequest(t, rec)
}

// The convenience filters: each contributes its OWN clause to the JQL
// the stub receives, ANDed in the command's fixed order. Asserting the
// whole composed string at once pins every clause and the order — a
// filter whose wiring were cut would drop its clause and fail here.
func TestListFilterFlagsComposeJQL(t *testing.T) {
	body := mx3ListJQL(t,
		"--project", "ABC", "--assignee", "alice", "--status", "Open",
		"--type", "Bug", "--label", "bug", "--reporter", "bob", "--priority", "High")

	want := `project in ("ABC") AND assignee in ("alice") AND status in ("Open") AND issuetype in ("Bug") AND labels in ("bug") AND reporter in ("bob") AND priority in ("High")`
	if body.JQL != want {
		t.Fatalf("stub JQL = %q, want %q", body.JQL, want)
	}

	Prove(t, "jira issue list", "project", `stub search JQL contained project in ("ABC") as its own AND clause`)
	Prove(t, "jira issue list", "assignee", `stub search JQL contained assignee in ("alice") as its own AND clause`)
	Prove(t, "jira issue list", "status", `stub search JQL contained status in ("Open") as its own AND clause`)
	Prove(t, "jira issue list", "type", `stub search JQL contained issuetype in ("Bug") as its own AND clause`)
	Prove(t, "jira issue list", "label", `stub search JQL contained labels in ("bug") as its own AND clause`)
	Prove(t, "jira issue list", "reporter", `stub search JQL contained reporter in ("bob") as its own AND clause`)
	Prove(t, "jira issue list", "priority", `stub search JQL contained priority in ("High") as its own AND clause`)
}

// The date bounds and --watching. --updated-before is given in the
// slash spelling deliberately, so the assertion also pins that the value
// is parsed and re-canonicalized rather than echoed.
func TestListDateAndWatchingFlagsComposeJQL(t *testing.T) {
	body := mx3ListJQL(t,
		"--created-after", "-7d", "--created-before", "2026-08-01",
		"--updated-after", "-2w", "--updated-before", "2026/08/01",
		"--watching")

	want := `created >= -7d AND created <= "2026-08-01" AND updated >= -2w AND updated <= "2026-08-01" AND watcher = currentUser()`
	if body.JQL != want {
		t.Fatalf("stub JQL = %q, want %q", body.JQL, want)
	}

	Prove(t, "jira issue list", "created-after", `stub search JQL contained created >= -7d`)
	Prove(t, "jira issue list", "created-before", `stub search JQL contained created <= "2026-08-01"`)
	Prove(t, "jira issue list", "updated-after", `stub search JQL contained updated >= -2w`)
	Prove(t, "jira issue list", "updated-before", `stub search JQL contained updated <= "2026-08-01", canonicalized from the 2026/08/01 spelling`)
	Prove(t, "jira issue list", "watching", `stub search JQL contained watcher = currentUser()`)
}

// --order-by composes the trailing ORDER BY with the field's own default
// direction; --reverse flips exactly that direction.
func TestListOrderByAndReverseComposeOrderClause(t *testing.T) {
	forward := mx3ListJQL(t, "--project", "ABC", "--order-by", "updated")
	if want := `project in ("ABC") ORDER BY updated DESC`; forward.JQL != want {
		t.Fatalf("order-by JQL = %q, want %q", forward.JQL, want)
	}
	flipped := mx3ListJQL(t, "--project", "ABC", "--order-by", "updated", "--reverse")
	if want := `project in ("ABC") ORDER BY updated ASC`; flipped.JQL != want {
		t.Fatalf("order-by --reverse JQL = %q, want %q", flipped.JQL, want)
	}

	Prove(t, "jira issue list", "order-by", `stub search JQL ended ORDER BY updated DESC, the field's default direction`)
	Prove(t, "jira issue list", "reverse", `adding --reverse flipped the stub-received ORDER BY updated DESC to ASC`)
}

// The hidden compatibility aliases --sort, --order and --desc resolve
// into --order-by/--reverse; each is proved by the same wire effect its
// canonical has.
func TestListHiddenOrderAliasesComposeOrderClause(t *testing.T) {
	sort := mx3ListJQL(t, "--project", "ABC", "--sort", "created")
	if want := `project in ("ABC") ORDER BY created DESC`; sort.JQL != want {
		t.Fatalf("--sort JQL = %q, want %q", sort.JQL, want)
	}
	order := mx3ListJQL(t, "--project", "ABC", "--order", "priority")
	if want := `project in ("ABC") ORDER BY priority DESC`; order.JQL != want {
		t.Fatalf("--order JQL = %q, want %q", order.JQL, want)
	}
	desc := mx3ListJQL(t, "--project", "ABC", "--order-by", "updated", "--desc")
	if want := `project in ("ABC") ORDER BY updated ASC`; desc.JQL != want {
		t.Fatalf("--desc JQL = %q, want %q", desc.JQL, want)
	}

	Prove(t, "jira issue list", "sort", `hidden --sort created reached the stub as ORDER BY created DESC`)
	Prove(t, "jira issue list", "order", `hidden --order priority reached the stub as ORDER BY priority DESC`)
	Prove(t, "jira issue list", "desc", `hidden --desc flipped the stub-received ORDER BY updated DESC to ASC`)
}

func TestListHiddenWatchedAliasComposesClause(t *testing.T) {
	body := mx3ListJQL(t, "--watched")
	if want := `watcher = currentUser()`; body.JQL != want {
		t.Fatalf("--watched JQL = %q, want %q", body.JQL, want)
	}
	Prove(t, "jira issue list", "watched", `hidden --watched reached the stub as watcher = currentUser()`)
}

// --jql is the raw escape hatch: the caller's query reaches the wire
// verbatim, composed with nothing.
func TestListJQLPassesThroughVerbatim(t *testing.T) {
	raw := `project = "ABC" AND summary ~ "order by" ORDER BY created ASC`
	body := mx3ListJQL(t, "--jql", raw)
	if body.JQL != raw {
		t.Fatalf("stub JQL = %q, want the --jql value verbatim %q", body.JQL, raw)
	}
	Prove(t, "jira issue list", "jql", "stub received the --jql query byte-for-byte, uncomposed")
}

// --limit sets the page size actually requested: 7, not the default 50.
func TestListLimitSetsRequestedPageSize(t *testing.T) {
	body := mx3ListJQL(t, "--project", "ABC", "--limit", "7")
	if body.MaxResults != 7 {
		t.Fatalf("stub maxResults = %d, want 7 (default would be 50)", body.MaxResults)
	}
	Prove(t, "jira issue list", "limit", "stub saw maxResults=7 in the search request, not the default 50")
}

// --all switches the walk from --limit's default 50 to full 100-issue
// pages, and drains: the second request resumes from the first page's
// cursor.
func TestListAllDrainsFullPages(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		if req.Method != http.MethodPost || req.Path != mx3SearchPath {
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body mx3SearchBody
		if err := json.Unmarshal(req.Body, &body); err != nil || body.NextPageToken == "" {
			_, _ = fmt.Fprint(w, `{"isLast":false,"nextPageToken":"page-2","issues":[{"id":"1","key":"KEY-1","fields":{"summary":"One"}}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"id":"2","key":"KEY-2","fields":{"summary":"Two"}}]}`)
	}))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "list", "--project", "ABC", "--all", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("issue list --all: exit %d\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}
	reqs := rec.byPath(mx3SearchPath)
	if len(reqs) != 2 {
		t.Fatalf("search requests = %d, want 2 (the drain must follow the cursor)", len(reqs))
	}
	var first, second mx3SearchBody
	if err := json.Unmarshal(reqs[0].Body, &first); err != nil {
		t.Fatalf("decode first body: %v", err)
	}
	if err := json.Unmarshal(reqs[1].Body, &second); err != nil {
		t.Fatalf("decode second body: %v", err)
	}
	if first.MaxResults != 100 {
		t.Fatalf("first page maxResults = %d, want 100 (without --all the default limit asks for 50)", first.MaxResults)
	}
	if second.NextPageToken != "page-2" {
		t.Fatalf("second request token = %q, want %q", second.NextPageToken, "page-2")
	}
	Prove(t, "jira issue list", "all",
		"stub saw a 100-issue first page (50 without the flag) and a second request resuming from the returned cursor")
}

// --unbounded lifts --all's cap and is refused without --all beside it.
func TestListUnboundedRefusedWithoutAll(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "list", "--project", "ABC", "--unbounded", "--output=json")
	mx3RequireRefusal(t, res, "flag_not_applicable", "unbounded")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue list", "unbounded",
		"refused without --all by code flag_not_applicable on field unbounded, before any request")
}

// --next-page-token rides into the request's cursor field verbatim.
func TestListNextPageTokenReachesRequest(t *testing.T) {
	body := mx3ListJQL(t, "--project", "ABC", "--next-page-token", "tok-123")
	if body.NextPageToken != "tok-123" {
		t.Fatalf("stub nextPageToken = %q, want %q", body.NextPageToken, "tok-123")
	}
	Prove(t, "jira issue list", "next-page-token", "stub saw nextPageToken=tok-123 in the search request body")
}

// --cursor is 0.15's hidden spelling of --next-page-token.
func TestListHiddenCursorAliasReachesRequest(t *testing.T) {
	body := mx3ListJQL(t, "--project", "ABC", "--cursor", "tok-9")
	if body.NextPageToken != "tok-9" {
		t.Fatalf("stub nextPageToken = %q, want %q", body.NextPageToken, "tok-9")
	}
	Prove(t, "jira issue list", "cursor", "hidden --cursor tok-9 reached the stub as nextPageToken=tok-9")
}

// --count takes the approximate-count endpoint INSTEAD of the search:
// the count request carries the composed JQL, no search runs at all, and
// the envelope publishes the estimate.
func TestListCountSkipsSearch(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3ListHandler(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "list", "--project", "ABC", "--count", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("issue list --count: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	if got := rec.byPath(mx3SearchPath); len(got) != 0 {
		t.Fatalf("search requests = %d, want 0 — --count must fetch nothing", len(got))
	}
	counts := rec.byPath(mx3CountPath)
	if len(counts) != 1 {
		t.Fatalf("count requests = %d, want 1", len(counts))
	}
	var body mx3SearchBody
	if err := json.Unmarshal(counts[0].Body, &body); err != nil {
		t.Fatalf("decode count body: %v", err)
	}
	if want := `project in ("ABC")`; body.JQL != want {
		t.Fatalf("count JQL = %q, want %q", body.JQL, want)
	}
	data := mx3Data(t, res)
	count, ok := data["count"].(map[string]any)
	if !ok || count["value"] != float64(7) {
		t.Fatalf("data.count = %v, want value 7", data["count"])
	}
	Prove(t, "jira issue list", "count",
		"stub saw one approximate-count request and zero search requests; data.count.value carried the stub's 7")
}

// --fields replaces the default six-field set with exactly what was
// asked for.
func TestListFieldsRestrictsRequestedFields(t *testing.T) {
	body := mx3ListJQL(t, "--project", "ABC", "--fields", "summary,status")
	want := []string{"summary", "status"}
	if len(body.Fields) != len(want) || body.Fields[0] != want[0] || body.Fields[1] != want[1] {
		t.Fatalf("stub fields = %v, want %v (default is the six-field table set)", body.Fields, want)
	}
	Prove(t, "jira issue list", "fields",
		"stub saw fields=[summary status] in the search request instead of the six-field default")
}

// --columns is refused when it names a column whose field --fields did
// not fetch — a column of blanks would read as empty values, not
// unfetched ones. The refusal fires before any request.
func TestListColumnsRefusedWhenFieldUnfetched(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "list",
		"--project", "ABC", "--fields", "summary", "--columns", "assignee", "--output=json")
	mx3RequireRefusal(t, res, "flag_conflict", "columns")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue list", "columns",
		"--columns assignee beside --fields summary refused by code flag_conflict on field columns, before any request")
}

// --as-jql prints the composed query and contacts nothing: the stub sees
// zero requests and data.jql carries the exact composition.
func TestListAsJQLPrintsAndContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "list",
		"--project", "ABC", "--status", "Open", "--as-jql", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("issue list --as-jql: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if want := `project in ("ABC") AND status in ("Open")`; data["jql"] != want {
		t.Fatalf("data.jql = %v, want %q", data["jql"], want)
	}
	Prove(t, "jira issue list", "as-jql",
		"data.jql carried the composed query while the stub saw zero requests")
}

// The board fixture: name lookup, configuration, and the filter behind
// it. broken (when non-nil and true) fails the three board endpoints
// with a non-retryable 400 while the search stays healthy — the shape
// the cache tests below need.
func mx3BoardHandler(rec *mx3Recorder, broken *bool, brokenMu *sync.Mutex) http.Handler {
	boardDown := func() bool {
		if broken == nil {
			return false
		}
		brokenMu.Lock()
		defer brokenMu.Unlock()
		return *broken
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.Path == "/rest/agile/1.0/board":
			if boardDown() {
				http.Error(w, `{"errorMessages":["board lookup is down"]}`, http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint","type":"scrum"}]}`)
		case req.Method == http.MethodGet && req.Path == "/rest/agile/1.0/board/42/configuration":
			if boardDown() {
				http.Error(w, `{"errorMessages":["configuration is down"]}`, http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"id":42,"name":"Sprint","filter":{"id":"100"}}`)
		case req.Method == http.MethodGet && req.Path == "/rest/api/3/filter/100":
			if boardDown() {
				http.Error(w, `{"errorMessages":["filter is down"]}`, http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"id":"100","jql":"project = A OR project = B ORDER BY Rank ASC"}`)
		case req.Method == http.MethodPost && req.Path == mx3SearchPath:
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"id":"1","key":"KEY-1","fields":{"summary":"One"}}]}`)
		default:
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
		}
	})
}

// --board resolves the named board's filter, PARENTHESISES it, ANDs the
// convenience filters onto it, and splits the filter's trailing ORDER BY
// back out to the end of the whole query.
func TestListBoardComposesParenthesisedScope(t *testing.T) {
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			rec := &mx3Recorder{}
			baseURL := mx3Stub(t, mx3BoardHandler(rec, nil, nil))
			res := mx3Run(t, t.TempDir(), baseURL, "issue", verb,
				"--board", "Sprint", "--status", "Done", "--output=json")
			if res.Exit != 0 {
				t.Fatalf("issue list --board: exit %d\nstderr: %s", res.Exit, res.Stderr)
			}
			body := mx3SearchRequest(t, rec)
			want := `(project = A OR project = B) AND status in ("Done") ORDER BY Rank ASC`
			if verb == "mine" {
				want = `(project = A OR project = B) AND assignee = currentUser() AND status in ("Done") ORDER BY Rank ASC`
			}
			if body.JQL != want {
				t.Fatalf("stub JQL = %q, want %q", body.JQL, want)
			}
			if got := rec.byPath("/rest/agile/1.0/board"); len(got) != 1 {
				t.Fatalf("board name lookups = %d, want 1", len(got))
			}
			Prove(t, "jira issue "+verb, "board",
				`stub search JQL was the board filter parenthesised, ANDed with status in ("Done"), its ORDER BY Rank split to the end`)
		})
	}
}

// --board-id resolves the same board by number, skipping the name lookup
// entirely.
func TestListBoardIDSkipsNameLookup(t *testing.T) {
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			rec := &mx3Recorder{}
			baseURL := mx3Stub(t, mx3BoardHandler(rec, nil, nil))
			res := mx3Run(t, t.TempDir(), baseURL, "issue", verb, "--board-id", "42", "--output=json")
			if res.Exit != 0 {
				t.Fatalf("issue list --board-id: exit %d\nstderr: %s", res.Exit, res.Stderr)
			}
			body := mx3SearchRequest(t, rec)
			want := `(project = A OR project = B) ORDER BY Rank ASC`
			if verb == "mine" {
				want = `(project = A OR project = B) AND assignee = currentUser() ORDER BY Rank ASC`
			}
			if body.JQL != want {
				t.Fatalf("stub JQL = %q, want %q", body.JQL, want)
			}
			if got := rec.byPath("/rest/agile/1.0/board"); len(got) != 0 {
				t.Fatalf("board name lookups = %d, want 0 — --board-id must not search by name", len(got))
			}
			if got := rec.byPath("/rest/agile/1.0/board/42/configuration"); len(got) != 1 {
				t.Fatalf("configuration reads = %d, want 1", len(got))
			}
			Prove(t, "jira issue "+verb, "board-id",
				"stub saw the board resolved via /board/42/configuration with zero name lookups, and the search ran the board's query")
		})
	}
}

// --refresh FORGETS the cache entry rather than bypassing it: after a
// --refresh whose re-read failed, a following plain run must ALSO fail —
// a bypass would have left the stale entry to be served.
func TestListBoardRefreshForgetsCacheEntry(t *testing.T) {
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			rec := &mx3Recorder{}
			var brokenMu sync.Mutex
			broken := false
			baseURL := mx3Stub(t, mx3BoardHandler(rec, &broken, &brokenMu))
			setBroken := func(v bool) { brokenMu.Lock(); broken = v; brokenMu.Unlock() }
			home := t.TempDir()
			argv := []string{"issue", verb, "--board-id", "42", "--output=json"}

			if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
				t.Fatalf("warm-up run: exit %d\nstderr: %s", res.Exit, res.Stderr)
			}
			setBroken(true)
			if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
				t.Fatalf("cached run against broken board endpoints: exit %d — the cache is not serving, so nothing below discriminates\nstderr: %s", res.Exit, res.Stderr)
			}
			refreshed := mx3Run(t, home, baseURL, append(argv, "--refresh")...)
			if refreshed.Exit == 0 {
				t.Fatalf("--refresh against broken board endpoints succeeded — it served the cache it promised to ignore\nstdout: %s", refreshed.Stdout)
			}
			after := mx3Run(t, home, baseURL, argv...)
			if after.Exit == 0 {
				t.Fatalf("plain run AFTER the failed --refresh succeeded — the stale entry was bypassed, not forgotten\nstdout: %s", after.Stdout)
			}
			Prove(t, "jira issue "+verb, "refresh",
				"a failed --refresh left the next plain run failing too: the board cache entry was forgotten, not bypassed (a cached run against the same broken site succeeded just before)")
		})
	}
}

// mx3AgeCacheEntries rewrites every disk-cache entry's written stamp to
// age ago. The entry carries its own timestamp (diskEntry.Written) —
// mtime games would not work, deliberately so — hence the rewrite.
func mx3AgeCacheEntries(t *testing.T, workdir string, age time.Duration) {
	t.Helper()
	cacheDir := filepath.Join(workdir, "cache")
	aged := 0
	err := filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil // not a cache entry; leave it
		}
		if _, ok := entry["written"]; !ok {
			return nil
		}
		stamp, err := json.Marshal(time.Now().Add(-age).UTC())
		if err != nil {
			return err
		}
		entry["written"] = stamp
		out, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		aged++
		return os.WriteFile(path, out, 0o600)
	})
	if err != nil {
		t.Fatalf("age cache entries: %v", err)
	}
	if aged == 0 {
		t.Fatalf("no cache entries found under %s to age — the warm-up run cached nothing", cacheDir)
	}
}

// --ttl-minutes changes how old a cached board resolution may be: an
// entry aged past the 24h default is a miss (and the re-read fails on
// the broken site), while a wider --ttl-minutes accepts the same entry.
func TestListBoardTTLMinutesWidensAcceptance(t *testing.T) {
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			rec := &mx3Recorder{}
			var brokenMu sync.Mutex
			broken := false
			baseURL := mx3Stub(t, mx3BoardHandler(rec, &broken, &brokenMu))
			home := t.TempDir()
			argv := []string{"issue", verb, "--board-id", "42", "--output=json"}

			if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
				t.Fatalf("warm-up run: exit %d\nstderr: %s", res.Exit, res.Stderr)
			}
			mx3AgeCacheEntries(t, home, 25*time.Hour)
			brokenMu.Lock()
			broken = true
			brokenMu.Unlock()

			expired := mx3Run(t, home, baseURL, argv...)
			if expired.Exit == 0 {
				t.Fatalf("default TTL accepted a 25h-old entry — the aged entry should have been a miss\nstdout: %s", expired.Stdout)
			}
			widened := mx3Run(t, home, baseURL, append(argv, "--ttl-minutes", "2880")...)
			if widened.Exit != 0 {
				t.Fatalf("--ttl-minutes 2880 rejected a 25h-old entry it should accept: exit %d\nstderr: %s", widened.Exit, widened.Stderr)
			}
			Prove(t, "jira issue "+verb, "ttl-minutes",
				"a 25h-old board cache entry missed under the 24h default (run failed on the broken site) and hit under --ttl-minutes 2880 (run succeeded)")
		})
	}
}
