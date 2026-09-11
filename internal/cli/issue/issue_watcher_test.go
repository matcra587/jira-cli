package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/spf13/cobra"
)

type watcherErrorWriter struct {
	err    error
	writes int
}

func (w *watcherErrorWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, w.err
}

// newWatcherTestRoot wires a minimal cobra root + persistent flags so the
// watcher commands resolve `cmdutil.JiraClientForCommand` via a temp config that
// points at the supplied httptest URL. Token credential is provided via
// JIRA_TOKEN_DEFAULT env so credential resolution succeeds without a
// keyring.
func newWatcherTestRoot(t *testing.T, srvURL string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := `default_profile = "default"
queries_path = "` + filepath.ToSlash(t.TempDir()) + `/queries"

[[profiles]]
name = "default"
base_url = "` + srvURL + `"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("JIRA_TOKEN_DEFAULT", "test-token")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root := &cobra.Command{Use: "jira"}
	pf := root.PersistentFlags()
	pf.String("profile", "default", "")
	pf.String("config", cfgPath, "")
	pf.Bool("json", true, "")
	pf.Bool("compact", false, "")
	pf.Bool("plain", false, "")
	pf.Bool("raw", false, "")
	pf.BoolP("interactive", "i", false, "")
	pf.BoolP("debug", "d", false, "")
	pf.String("color", "never", "")
	pf.Bool("adf-strict", false, "")

	root.AddGroup(&cobra.Group{ID: "resources", Title: "resources"})
	for _, factory := range WatcherCommands {
		root.AddCommand(factory())
	}

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetContext(cmdutil.WithDetector(context.Background(), cli.Detection{Mode: cli.ModeJSON, IsTTY: false}))
	return root, stdout, stderr
}

func decodeWatcherErrorEnvelope(t *testing.T, stdout, stderr *bytes.Buffer, target any) {
	t.Helper()
	// Machine mode: the failure envelope is on stdout, the same stream as
	// success.
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	for _, line := range slices.Backward(lines) {
		line := bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal(line, target); err != nil {
			t.Fatalf("stdout envelope is not JSON: %v\nstdout=%s", err, stdout.String())
		}
		return
	}
	t.Fatalf("stdout has no JSON envelope:\n%s", stdout.String())
}

const myselfFixture = `{"accountId":"712020:test-user","emailAddress":"user@example.com","displayName":"Test User","active":true}`

func watcherListJSON(isWatching bool, count int, accounts ...[2]string) string {
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

func TestWatchersListEmitsEnvelopeShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfFixture))
		case "/rest/api/3/issue/JCT-1/watchers":
			_, _ = w.Write([]byte(watcherListJSON(
				true, 2,
				[2]string{"acc-alice", "Alice"},
				[2]string{"acc-bob", "Bob"},
			)))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root, stdout, stderr := newWatcherTestRoot(t, srv.URL)
	root.SetArgs([]string{"watchers", "list", "JCT-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	data, _ := env["data"].(map[string]any)
	watchers, _ := data["watchers"].([]any)
	if len(watchers) != 2 {
		t.Fatalf("watchers = %v, want 2", watchers)
	}
	if v, _ := data["is_watching"].(bool); !v {
		t.Errorf("is_watching = false, want true")
	}
	if c, _ := data["watch_count"].(float64); c != 2 {
		t.Errorf("watch_count = %v, want 2", c)
	}
}

func TestWatchersAddPostsRawAccountIDStringAndReadsBack(t *testing.T) {
	var postBody atomic.Value
	var postCount, getCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfFixture))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			atomic.AddInt32(&postCount, 1)
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			postBody.Store(buf.String())
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			atomic.AddInt32(&getCount, 1)
			_, _ = w.Write([]byte(watcherListJSON(true, 1, [2]string{"712020:test-user", "Test"})))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root, stdout, stderr := newWatcherTestRoot(t, srv.URL)
	root.SetArgs([]string{"watchers", "add", "JCT-1", "--user", "me"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	got, _ := postBody.Load().(string)
	if got != `"712020:test-user"` {
		t.Fatalf("POST body = %q, want %q", got, `"712020:test-user"`)
	}
	if atomic.LoadInt32(&postCount) != 1 {
		t.Errorf("POST count = %d, want 1", atomic.LoadInt32(&postCount))
	}
	if atomic.LoadInt32(&getCount) < 1 {
		t.Errorf("readback GET = %d, want at least 1", atomic.LoadInt32(&getCount))
	}

	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	data, _ := env["data"].(map[string]any)
	if _, has := data["watchers"]; !has {
		t.Errorf("readback path: data.watchers missing: %s", stdout)
	}
	if _, has := data["was_already_watching"]; !has {
		t.Errorf("readback path: data.was_already_watching missing: %s", stdout)
	}
}

func TestWatchersAddNoReadbackBareShape(t *testing.T) {
	var postSeen, getAfterPost int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfFixture))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			atomic.StoreInt32(&postSeen, 1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			if atomic.LoadInt32(&postSeen) == 1 {
				atomic.AddInt32(&getAfterPost, 1)
			}
			_, _ = w.Write([]byte(watcherListJSON(false, 0)))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root, stdout, stderr := newWatcherTestRoot(t, srv.URL)
	root.SetArgs([]string{"watchers", "add", "JCT-1", "--user", "me", "--no-readback"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	if atomic.LoadInt32(&getAfterPost) != 0 {
		t.Errorf("--no-readback issued %d post-mutation GETs, want 0", atomic.LoadInt32(&getAfterPost))
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	data, _ := env["data"].(map[string]any)
	if _, has := data["watchers"]; has {
		t.Errorf("--no-readback returned readback shape: %s", stdout)
	}
	if data["account_id"] != "712020:test-user" {
		t.Errorf("data.account_id = %v, want 712020:test-user", data["account_id"])
	}
	if data["attempted"] != true {
		t.Errorf("data.attempted = %v, want true", data["attempted"])
	}
}

func TestWatchersAddAmbiguousEmitsCandidatesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfFixture))
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/user/search"):
			_, _ = w.Write([]byte(`[
				{"accountId":"a-1","displayName":"Alice Smith","emailAddress":"alice.smith@example.com","active":true},
				{"accountId":"a-2","displayName":"Alice Jones","emailAddress":"alice.jones@example.com","active":true}
			]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root, stdout, stderr := newWatcherTestRoot(t, srv.URL)
	root.SetArgs([]string{"watchers", "add", "JCT-1", "--user", "alice"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected ambiguity error, got success:\n%s", stdout)
	}
	var env map[string]any
	decodeWatcherErrorEnvelope(t, stdout, stderr, &env)
	errs, _ := env["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors[] = %v, want 1: %s", errs, stderr)
	}
	first, _ := errs[0].(map[string]any)
	if first["type"] != "validation" {
		t.Errorf("errors[0].type = %v, want validation", first["type"])
	}
	// The ambiguity error must carry a stable machine code so agents can
	// branch on it without parsing the message.
	if code, _ := first["code"].(string); code == "" {
		t.Errorf("errors[0].code is empty, want a stable validation code: %s", stderr)
	}
	// The failure envelope must report the exit code so a machine consumer
	// sees the same exit value the process returns.
	meta, _ := env["meta"].(map[string]any)
	if ec, ok := meta["exit_code"].(float64); !ok || int(ec) != 3 {
		t.Errorf("meta.exit_code = %v, want 3: %s", meta["exit_code"], stderr)
	}
	cands, _ := first["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates len = %d, want 2: %s", len(cands), stderr)
	}
}

func TestWatcherAmbiguityPreservesWriterFailureWithoutClaimingEnvelope(t *testing.T) {
	writeErr := errors.New("stdout closed")
	stdout := &watcherErrorWriter{err: writeErr}
	cmd := &cobra.Command{Use: "add"}
	cmd.SetOut(stdout)
	ambiguity := &jira.AmbiguousUserError{Query: "alice"}

	err := handleResolveErr(cmd, "issue.watchers.add", ambiguity)
	if !errors.Is(err, ambiguity) || !errors.Is(err, writeErr) {
		t.Fatalf("handleResolveErr() error = %v, want ambiguity and writer causes", err)
	}
	if _, ok := errors.AsType[*cli.OutputError](err); !ok {
		t.Fatalf("handleResolveErr() error type = %T, want *cli.OutputError", err)
	}
	if _, ok := errors.AsType[cmdutil.EnvelopeWrittenError](err); ok {
		t.Fatalf("handleResolveErr() claimed a written envelope after writer failure: %v", err)
	}
	mapped := cli.MapError(err)
	if mapped.Code != "user_ambiguous" || mapped.Type != "validation" || cli.ExitCode(mapped) != 3 {
		t.Fatalf("MapError() = %#v, want primary user_ambiguous validation exit 3", mapped)
	}
	if stdout.writes != 1 {
		t.Fatalf("stdout writes = %d, want one failed envelope attempt", stdout.writes)
	}
}

func TestWatchersRemoveDeletesByAccountIDAndReadsBack(t *testing.T) {
	var deleteQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(myselfFixture))
		case r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			deleteQuery.Store(r.URL.RawQuery)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
			_, _ = w.Write([]byte(watcherListJSON(false, 0)))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	root, stdout, stderr := newWatcherTestRoot(t, srv.URL)
	root.SetArgs([]string{"watchers", "remove", "JCT-1", "--user", "me"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	q, _ := deleteQuery.Load().(string)
	values, _ := url.ParseQuery(q)
	if values.Get("accountId") != "712020:test-user" {
		t.Fatalf("DELETE query accountId = %q, want 712020:test-user", values.Get("accountId"))
	}
	var env map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &env)
	data, _ := env["data"].(map[string]any)
	if data["watchers"] == nil {
		t.Errorf("readback missing watchers: %s", stdout)
	}
}

func TestWatchShortcutEquivalentToWatchersAddMe(t *testing.T) {
	captures := make([]atomic.Value, 2)
	makeHandler := func(slot int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/rest/api/3/myself":
				_, _ = w.Write([]byte(myselfFixture))
			case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				buf := new(bytes.Buffer)
				_, _ = buf.ReadFrom(r.Body)
				captures[slot].Store(buf.String())
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				_, _ = w.Write([]byte(watcherListJSON(true, 1, [2]string{"712020:test-user", "Test"})))
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
		}
	}
	srvA := httptest.NewServer(makeHandler(0))
	defer srvA.Close()
	srvB := httptest.NewServer(makeHandler(1))
	defer srvB.Close()

	rootA, _, _ := newWatcherTestRoot(t, srvA.URL)
	rootA.SetArgs([]string{"watchers", "add", "JCT-1", "--user", "me"})
	if err := rootA.Execute(); err != nil {
		t.Fatalf("watchers add: %v", err)
	}
	rootB, _, _ := newWatcherTestRoot(t, srvB.URL)
	rootB.SetArgs([]string{"watch", "JCT-1"})
	if err := rootB.Execute(); err != nil {
		t.Fatalf("watch shortcut: %v", err)
	}
	a, _ := captures[0].Load().(string)
	b, _ := captures[1].Load().(string)
	if a == "" || a != b {
		t.Fatalf("body mismatch: watchers-add=%q watch=%q", a, b)
	}
}

func TestUnwatchShortcutEquivalentToWatchersRemoveMe(t *testing.T) {
	captures := make([]atomic.Value, 2)
	makeHandler := func(slot int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/rest/api/3/myself":
				_, _ = w.Write([]byte(myselfFixture))
			case r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				captures[slot].Store(r.URL.RawQuery)
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/JCT-1/watchers":
				_, _ = w.Write([]byte(watcherListJSON(false, 0)))
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
		}
	}
	srvA := httptest.NewServer(makeHandler(0))
	defer srvA.Close()
	srvB := httptest.NewServer(makeHandler(1))
	defer srvB.Close()

	rootA, _, _ := newWatcherTestRoot(t, srvA.URL)
	rootA.SetArgs([]string{"watchers", "remove", "JCT-1", "--user", "me"})
	if err := rootA.Execute(); err != nil {
		t.Fatalf("watchers remove: %v", err)
	}
	rootB, _, _ := newWatcherTestRoot(t, srvB.URL)
	rootB.SetArgs([]string{"unwatch", "JCT-1"})
	if err := rootB.Execute(); err != nil {
		t.Fatalf("unwatch shortcut: %v", err)
	}
	a, _ := captures[0].Load().(string)
	b, _ := captures[1].Load().(string)
	if a == "" || a != b {
		t.Fatalf("query mismatch: watchers-remove=%q unwatch=%q", a, b)
	}
}
