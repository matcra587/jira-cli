package contract

import (
	"bytes"
	"encoding/json"
	stdlibErrors "errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// ----- shared fixtures ------------------------------------------------------

const (
	myselfBody = `{"accountId":"712020:test-user","emailAddress":"user@example.com","displayName":"Test User","active":true}`
)

// watchersBody is the GET /watchers response shape Atlassian returns.
func watchersBody(isWatching bool, count int, accounts ...[2]string) string {
	users := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		users = append(users, map[string]any{
			"accountId":   a[0],
			"displayName": a[1],
			"active":      true,
		})
	}
	out, _ := json.Marshal(map[string]any{
		"isWatching": isWatching,
		"watchCount": count,
		"watchers":   users,
	})
	return string(out)
}

func runJiraWatchers(t *testing.T, srvURL, profileEnv string, args ...string) ([]byte, error) {
	t.Helper()
	cfg := jiraConfig(t, srvURL)
	// Build a real binary rather than `go run` so we observe the program's
	// exit code directly (go run masks non-zero exits as `1`, which breaks
	// the exit-2 / exit-3 assertions on the failure paths).
	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, append([]string{"--config", cfg, "--output=json"}, args...)...)
	cmd.Env = append(cmd.Environ(), profileEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// Machine mode: the failure envelope is on stdout, the same stream as
		// success, with ok:false and a non-zero exit code.
		return jsonEnvelopeLineFromStream(t, stdout.Bytes(), "stdout", stdout.Bytes(), stderr.Bytes(), cmd.Args, nil), err
	}
	return stdout.Bytes(), nil
}

// ----- watchers list contract ----------

func TestWatchersListEnvelopeShape(t *testing.T) {
	body := watchersBody(
		true, 3,
		[2]string{"acc-alice", "Alice"},
		[2]string{"acc-bob", "Bob"},
		[2]string{"acc-carol", "Carol"},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token", "issue", "watchers", "list", "JCT-1")
	if err != nil {
		t.Fatalf("watchers list error = %v\n%s", err, out)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("watchers list output is not JSON: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("envelope missing data: %s", out)
	}
	issue, _ := data["issue"].(map[string]any)
	if issue["key"] != "JCT-1" {
		t.Fatalf("data.issue = %v, want key JCT-1: %s", issue, out)
	}
	watchers, _ := data["watchers"].([]any)
	if len(watchers) != 3 {
		t.Fatalf("watchers = %v, want 3 entries", watchers)
	}
	if v, _ := data["is_watching"].(bool); !v {
		t.Errorf("is_watching = false, want true")
	}
	if c, _ := data["watch_count"].(float64); c != 3 {
		t.Errorf("watch_count = %v, want 3", c)
	}
}

func TestWatchersListEmptyKeepsIssueAndNonNullWatchers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/issue/EMPTY-1/watchers":
			_, _ = w.Write([]byte(`{"isWatching":false,"watchCount":0,"watchers":[]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t,
		srv.URL,
		"JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "list", "EMPTY-1",
	)
	if err != nil {
		t.Fatalf("watchers list error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Issue struct {
				Key string `json:"key"`
			} `json:"issue"`
			Watchers []json.RawMessage `json:"watchers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
	if env.Data.Issue.Key != "EMPTY-1" {
		t.Fatalf("data.issue.key = %q, want EMPTY-1\n%s", env.Data.Issue.Key, out)
	}
	if env.Data.Watchers == nil || len(env.Data.Watchers) != 0 {
		t.Fatalf("data.watchers = %#v, want non-null empty array\n%s", env.Data.Watchers, out)
	}
}

func TestWatchersListPreservesEmptyEmailPresence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/rest/api/3/issue/JCT-1/watchers" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"isWatching":false,"watchCount":2,"watchers":[` +
			`{"accountId":"with-empty","displayName":"Empty","emailAddress":""},` +
			`{"accountId":"without","displayName":"Missing"}]}`))
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t,
		srv.URL,
		"JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "list", "JCT-1",
	)
	if err != nil {
		t.Fatalf("watchers list error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Watchers []map[string]any `json:"watchers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
	if email, exists := env.Data.Watchers[0]["email_address"]; !exists || email != "" {
		t.Fatalf("present empty email = %#v, want explicit empty string", env.Data.Watchers[0])
	}
	if _, exists := env.Data.Watchers[1]["email_address"]; exists {
		t.Fatalf("missing email must remain omitted: %#v", env.Data.Watchers[1])
	}
}

// ----- watchers add contract ----------

// Asserts the Atlassian POST quirk: body is a raw JSON string ("acc-id"), not
// an object {"accountId": "..."}. Idempotent, follow-up GET drives final state.
func TestWatchersAddPostsRawAccountIDStringAndReadsBack(t *testing.T) {
	var postBody atomic.Value
	var postCount, getCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			atomic.AddInt32(&postCount, 1)
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			postBody.Store(string(buf))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			atomic.AddInt32(&getCount, 1)
			body := watchersBody(false, 1, [2]string{"712020:test-user", "Test User"})
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "me",
	)
	if err != nil {
		t.Fatalf("watchers add error = %v\n%s", err, out)
	}

	got, _ := postBody.Load().(string)
	if got != `"712020:test-user"` {
		t.Fatalf("POST body = %q, want %q (Atlassian raw-JSON-string quirk)", got, `"712020:test-user"`)
	}
	if atomic.LoadInt32(&postCount) != 1 {
		t.Errorf("POST /watchers hit %d times, want 1", atomic.LoadInt32(&postCount))
	}
	if atomic.LoadInt32(&getCount) < 1 {
		t.Errorf("expected at least one GET /watchers (readback ), got %d", atomic.LoadInt32(&getCount))
	}

	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("watchers add output is not JSON: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("envelope missing data: %s", out)
	}
	if _, hasReadback := data["watchers"]; !hasReadback {
		t.Errorf("readback path: data.watchers missing: %s", out)
	}
	if _, hasFlag := data["was_already_watching"]; !hasFlag {
		t.Errorf("readback path: data.was_already_watching missing: %s", out)
	}
	if data["user"] != "me" || data["user_resolved"] != true || data["account_id_resolved"] != "712020:test-user" {
		t.Errorf("readback path lost stable user-resolution context: %#v", data)
	}
}

// Idempotent: adding a user already watching → exit 0 with
// was_already_watching: true. The wire still sees the POST (Atlassian
// short-circuits server-side and returns 204), but the response indicates
// pre-existing membership.
func TestWatchersAddIdempotentWhenAlreadyWatching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			// Return the same accountId we're trying to add → already watching.
			body := watchersBody(true, 1, [2]string{"712020:test-user", "Test User"})
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "me",
	)
	if err != nil {
		t.Fatalf("idempotent add error = %v\n%s", err, out)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if v, _ := data["was_already_watching"].(bool); !v {
		t.Fatalf("was_already_watching = false, want true (already in list): %s", out)
	}
}

// --no-readback skips the follow-up GET and emits the bare {account_id, attempted}
// shape per envelope-shapes.md.
func TestWatchersAddNoReadbackBareShape(t *testing.T) {
	var getAfterPost atomic.Int32
	var sawPost atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			sawPost.Store(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			if sawPost.Load() == 1 {
				getAfterPost.Add(1)
			}
			body := watchersBody(false, 0)
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "me", "--no-readback",
	)
	if err != nil {
		t.Fatalf("watchers add --no-readback error = %v\n%s", err, out)
	}
	if getAfterPost.Load() != 0 {
		t.Fatalf("--no-readback issued %d follow-up GETs, want 0", getAfterPost.Load())
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if _, has := data["watchers"]; has {
		t.Errorf("--no-readback returned readback shape with data.watchers: %s", out)
	}
	if v, _ := data["account_id"].(string); v != "712020:test-user" {
		t.Errorf("data.account_id = %q, want 712020:test-user", v)
	}
	if v, _ := data["attempted"].(bool); !v {
		t.Errorf("data.attempted = false, want true")
	}
	if data["user"] != "me" || data["user_resolved"] != true || data["account_id_resolved"] != "712020:test-user" {
		t.Errorf("no-readback path lost stable user-resolution context: %#v", data)
	}
}

// ----- watchers remove contract ----------

func TestWatchersRemoveDeletesByAccountIDAndReadsBack(t *testing.T) {
	var deleteQuery atomic.Value
	var deleteHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			deleteHits.Add(1)
			deleteQuery.Store(r.URL.RawQuery)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			body := watchersBody(false, 0)
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "remove", "JCT-1", "--user", "me",
	)
	if err != nil {
		t.Fatalf("watchers remove error = %v\n%s", err, out)
	}
	if deleteHits.Load() != 1 {
		t.Errorf("DELETE /watchers hit %d times, want 1", deleteHits.Load())
	}
	q, _ := deleteQuery.Load().(string)
	values, _ := url.ParseQuery(q)
	if got := values.Get("accountId"); got != "712020:test-user" {
		t.Fatalf("DELETE query accountId = %q, want %q", got, "712020:test-user")
	}

	var env map[string]any
	_ = json.Unmarshal(out, &env)
	data, _ := env["data"].(map[string]any)
	if data == nil || data["watchers"] == nil {
		t.Errorf("readback missing watchers slice: %s", out)
	}
	// Removing me when I'm not watching → was_already_watching must be false.
	if v, exists := data["was_already_watching"]; !exists {
		t.Errorf("missing was_already_watching: %s", out)
	} else if b, _ := v.(bool); b {
		t.Errorf("was_already_watching = true, want false (we're not in list)")
	}
}

// ----- resolution paths ----------

func TestWatchersAddUserMeSkipsUserSearch(t *testing.T) {
	var sawSearch atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			sawSearch.Store(1)
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/watchers"):
			_, _ = w.Write([]byte(watchersBody(true, 1, [2]string{"712020:test-user", "Test"})))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "me",
	)
	if err != nil {
		t.Fatalf("error = %v\n%s", err, out)
	}
	if sawSearch.Load() != 0 {
		t.Fatal("--user me triggered /user/search; expected /myself only")
	}
}

func TestWatchersAddAccountIDPrefixSkipsUserSearch(t *testing.T) {
	var sawSearch atomic.Int32
	var postBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			sawSearch.Store(1)
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost:
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			postBody.Store(string(buf))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/watchers"):
			_, _ = w.Write([]byte(watchersBody(false, 0)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "accountId:712020:abc",
	)
	if err != nil {
		t.Fatalf("error = %v\n%s", err, out)
	}
	if sawSearch.Load() != 0 {
		t.Fatal("accountId: prefix triggered /user/search; expected local parse only")
	}
	if got, _ := postBody.Load().(string); got != `"712020:abc"` {
		t.Fatalf("POST body = %q, want %q", got, `"712020:abc"`)
	}
}

func TestWatchersAddEmailQueriesUserSearch(t *testing.T) {
	var query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			query.Store(r.URL.Query().Get("query"))
			_, _ = w.Write([]byte(`[{"accountId":"acc-alice","displayName":"Alice","emailAddress":"alice@example.com","active":true}]`))
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/watchers"):
			_, _ = w.Write([]byte(watchersBody(false, 1, [2]string{"acc-alice", "Alice"})))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "alice@example.com",
	)
	if err != nil {
		t.Fatalf("error = %v\n%s", err, out)
	}
	if got, _ := query.Load().(string); got != "alice@example.com" {
		t.Fatalf("/user/search query = %q, want alice@example.com", got)
	}
}

// ----- ambiguity exit 3 ----------

func TestWatchersAddAmbiguousReturnsExit3WithCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			_, _ = w.Write([]byte(`[
				{"accountId":"a-1","displayName":"Alice Smith","emailAddress":"alice.smith@example.com","active":true},
				{"accountId":"a-2","displayName":"Alice Jones","emailAddress":"alice.jones@example.com","active":true}
			]`))
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		default:
			t.Errorf("unexpected mutation request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "alice",
	)
	if err == nil {
		t.Fatalf("watchers add expected to fail (exit 3), got success:\n%s", out)
	}
	if exit, ok := exitCodeOf(err); !ok || exit != 3 {
		t.Errorf("exit code = %d, want 3 (validation/ambiguity)", exit)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	errs, _ := env["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors[] len = %d, want 1: %s", len(errs), out)
	}
	first, _ := errs[0].(map[string]any)
	if t1, _ := first["type"].(string); t1 != "validation" {
		t.Errorf("errors[0].type = %q, want validation", t1)
	}
	cands, _ := first["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates len = %d, want 2: %s", len(cands), out)
	}
}

// 0 matches → exit 2 (not_found), input echoed.
func TestWatchersAddZeroMatchReturnsExit2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfBody))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out, err := runJiraWatchers(
		t, srv.URL, "JIRA_TOKEN_DEFAULT=test-token",
		"issue", "watchers", "add", "JCT-1", "--user", "ghost@example.com",
	)
	if err == nil {
		t.Fatalf("watchers add expected to fail (exit 2), got success:\n%s", out)
	}
	if exit, ok := exitCodeOf(err); !ok || exit != 2 {
		t.Errorf("exit code = %d, want 2 (not_found)", exit)
	}
	if !strings.Contains(string(out), "ghost@example.com") {
		t.Errorf("error envelope must echo input: %s", out)
	}
}

// ----- watch / unwatch shortcut equivalence ----------

func TestWatchShortcutEquivalentToWatchersAddMe(t *testing.T) {
	var post1, post2 atomic.Value
	chooseTarget := func(target *atomic.Value) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/rest/api/3/myself":
				_, _ = w.Write([]byte(myselfBody))
			case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				buf := make([]byte, r.ContentLength)
				_, _ = r.Body.Read(buf)
				target.Store(string(buf))
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				_, _ = w.Write([]byte(watchersBody(true, 1, [2]string{"712020:test-user", "Test"})))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}

	srvA := httptest.NewServer(chooseTarget(&post1))
	defer srvA.Close()
	srvB := httptest.NewServer(chooseTarget(&post2))
	defer srvB.Close()

	if out, err := runJiraWatchers(t, srvA.URL, "JIRA_TOKEN_DEFAULT=test-token", "issue", "watchers", "add", "JCT-1", "--user", "me"); err != nil {
		t.Fatalf("watchers add error = %v\n%s", err, out)
	}
	if out, err := runJiraWatchers(t, srvB.URL, "JIRA_TOKEN_DEFAULT=test-token", "issue", "watch", "JCT-1"); err != nil {
		t.Fatalf("watch shortcut error = %v\n%s", err, out)
	}

	a, _ := post1.Load().(string)
	b, _ := post2.Load().(string)
	if a == "" || a != b {
		t.Fatalf("watchers add me POST body = %q, watch shortcut POST body = %q, want equal", a, b)
	}
}

func TestUnwatchShortcutEquivalentToWatchersRemoveMe(t *testing.T) {
	var del1, del2 atomic.Value
	chooseTarget := func(target *atomic.Value) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/rest/api/3/myself":
				_, _ = w.Write([]byte(myselfBody))
			case r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				target.Store(r.URL.RawQuery)
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				_, _ = w.Write([]byte(watchersBody(false, 0)))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}

	srvA := httptest.NewServer(chooseTarget(&del1))
	defer srvA.Close()
	srvB := httptest.NewServer(chooseTarget(&del2))
	defer srvB.Close()

	if out, err := runJiraWatchers(t, srvA.URL, "JIRA_TOKEN_DEFAULT=test-token", "issue", "watchers", "remove", "JCT-1", "--user", "me"); err != nil {
		t.Fatalf("watchers remove error = %v\n%s", err, out)
	}
	if out, err := runJiraWatchers(t, srvB.URL, "JIRA_TOKEN_DEFAULT=test-token", "issue", "unwatch", "JCT-1"); err != nil {
		t.Fatalf("unwatch shortcut error = %v\n%s", err, out)
	}

	a, _ := del1.Load().(string)
	b, _ := del2.Load().(string)
	if a == "" || a != b {
		t.Fatalf("watchers remove me DELETE query = %q, unwatch shortcut DELETE query = %q, want equal", a, b)
	}
}

// ----- helpers -------------------------------------------------------------

// exitCodeOf extracts the exit code from an *exec.ExitError-shaped error.
// Returns ok=false if the error isn't an exit error (e.g. command-not-found).
func exitCodeOf(err error) (int, bool) {
	if ee, ok := stdlibErrors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), true
	}
	return 0, false
}
