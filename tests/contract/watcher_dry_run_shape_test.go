package contract

// Watcher --dry-run is a LOCAL preview (no hidden live resolve). The
// preview envelope must be honest about what it could resolve without
// contacting Jira:
//   - accountId:<id> is locally derivable → account_id_resolved set,
//     user_resolved:true, no live call.
//   - a name/email needs remote resolution → user echoed back,
//     user_resolved:false, account_id_resolved absent.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestWatcherDryRunAccountIDPrefixResolvesLocally proves that passing a
// locally-derivable accountId:<id> populates account_id_resolved in the
// dry-run preview WITHOUT any live request.
func TestWatcherDryRunAccountIDPrefixResolvesLocally(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "watchers", "add", "JCT-1", "--user", "accountId:712020:abc", "--dry-run")
	if code != 0 {
		t.Fatalf("watchers add --dry-run exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("accountId dry-run made %d live request(s); must be local-only", n)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	if env.Data["dry_run"] != true {
		t.Fatalf("data.dry_run = %#v, want true", env.Data["dry_run"])
	}
	if env.Data["user"] != "accountId:712020:abc" {
		t.Fatalf("data.user = %#v, want the original identifier", env.Data["user"])
	}
	issue, _ := env.Data["issue"].(map[string]any)
	if issue["key"] != "JCT-1" {
		t.Fatalf("data.issue.key = %#v, want JCT-1", env.Data["issue"])
	}
	if env.Data["account_id_resolved"] != "712020:abc" {
		t.Fatalf("data.account_id_resolved = %#v, want \"712020:abc\" (locally derivable)", env.Data["account_id_resolved"])
	}
	if env.Data["user_resolved"] != true {
		t.Fatalf("data.user_resolved = %#v, want true (accountId derivable without a call)", env.Data["user_resolved"])
	}
}

// TestWatcherDryRunNameEchoesUnresolved proves that a name/email — which
// genuinely needs remote resolution — is echoed back unresolved in the
// dry-run preview, with no account_id_resolved and no live call.
func TestWatcherDryRunNameEchoesUnresolved(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "watchers", "add", "JCT-1", "--user", "alice@example.com", "--dry-run")
	if code != 0 {
		t.Fatalf("watchers add --dry-run exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("name dry-run made %d live request(s); must be local-only", n)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	if env.Data["dry_run"] != true {
		t.Fatalf("data.dry_run = %#v, want true", env.Data["dry_run"])
	}
	if env.Data["user"] != "alice@example.com" {
		t.Fatalf("data.user = %#v, want the echoed identifier", env.Data["user"])
	}
	if env.Data["user_resolved"] != false {
		t.Fatalf("data.user_resolved = %#v, want false (name needs remote resolution)", env.Data["user_resolved"])
	}
	if _, present := env.Data["account_id_resolved"]; present {
		t.Fatalf("data.account_id_resolved must be absent when resolution did not run: %#v", env.Data)
	}
}
