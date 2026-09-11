package contract

// --dry-run is a LOCAL PREVIEW. It must not
// reach Jira (no writes, no hidden reads) and must not write cache.
// Remote validation, when offered, lives behind an explicit
// --validate-remote flag that uses read-only service paths only.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestWatcherAddDryRunPerformsNoLiveCalls proves `issue watch --dry-run`
// is local-only: a server that fails the test on ANY request must see
// none. The previous behavior resolved the user via a live GET.
func TestWatcherAddDryRunPerformsNoLiveCalls(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "watch", "JCT-1", "--dry-run")
	if code != 0 {
		t.Fatalf("watch --dry-run exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("watch --dry-run made %d live request(s); dry-run must be local-only", n)
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
}

// TestWatcherRemoveDryRunPerformsNoLiveCalls — same contract for unwatch.
func TestWatcherRemoveDryRunPerformsNoLiveCalls(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "unwatch", "JCT-1", "--dry-run")
	if code != 0 {
		t.Fatalf("unwatch --dry-run exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("unwatch --dry-run made %d live request(s); dry-run must be local-only", n)
	}
}

// TestWatcherAddValidateRemoteResolvesUserButDoesNotMutate proves the
// explicit --validate-remote flag uses a read-only path: it resolves
// the user (a GET) but never POSTs the watcher.
func TestWatcherAddValidateRemoteResolvesUserButDoesNotMutate(t *testing.T) {
	var posts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(myselfBody))
	})
	mux.HandleFunc("/rest/api/3/issue/JCT-1/watchers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			t.Errorf("--validate-remote must not POST the watcher")
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "watch", "JCT-1", "--dry-run", "--validate-remote")
	if code != 0 {
		t.Fatalf("watch --dry-run --validate-remote exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if n := posts.Load(); n != 0 {
		t.Fatalf("--validate-remote sent %d POST(s); it must be read-only", n)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	if env.Data["account_id_resolved"] == nil || env.Data["account_id_resolved"] == "" {
		t.Fatalf("--validate-remote should report account_id_resolved: %#v", env.Data)
	}
}

func TestWatcherAddValidateRemoteRejectsInactiveAccountID(t *testing.T) {
	var posts atomic.Int32
	var userLookups atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/user", func(w http.ResponseWriter, r *http.Request) {
		userLookups.Add(1)
		if got := r.URL.Query().Get("accountId"); got != "inactive" {
			t.Fatalf("accountId query = %q, want inactive", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accountId":"inactive","displayName":"Inactive User","active":false}`))
	})
	mux.HandleFunc("/rest/api/3/issue/JCT-1/watchers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			t.Errorf("--validate-remote must not POST the watcher")
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "watchers", "add", "JCT-1", "--user", "accountId:inactive", "--dry-run", "--validate-remote")
	if code == 0 {
		t.Fatalf("watch --dry-run --validate-remote accepted inactive account\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if n := userLookups.Load(); n != 1 {
		t.Fatalf("--validate-remote made %d user lookup(s), want 1", n)
	}
	if n := posts.Load(); n != 0 {
		t.Fatalf("--validate-remote sent %d POST(s); it must be read-only", n)
	}
}

// TestWeblinkDryRunRejectsMalformedURLLocally — weblink dry-run
// must do honest LOCAL validation. A syntactically invalid URL must be
// caught without contacting Jira.
func TestWeblinkDryRunRejectsMalformedURLLocally(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("weblink --dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "weblink", "JCT-1", "--url", "not a url", "--dry-run")
	if code == 0 {
		t.Fatalf("weblink --dry-run accepted a malformed URL; want local validation failure")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("weblink --dry-run made %d live request(s); URL validation must be local-only", n)
	}
	assertWeblinkURLErrorEnvelope(t, stdout, stderr, []string{
		"jira", "--config", cfg, "--output=json",
		"issue", "weblink", "JCT-1", "--url", "not a url", "--dry-run",
	}, "flag_value_invalid")
}

func TestWeblinkRejectsMissingURLWithSpecificCode(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("weblink with missing --url made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "weblink", "JCT-1", "--dry-run")
	if code == 0 {
		t.Fatalf("weblink --dry-run accepted a missing --url; want required-flag failure")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("weblink with missing --url made %d live request(s); required-flag validation must be local-only", n)
	}
	assertWeblinkURLErrorEnvelope(t, stdout, stderr, []string{
		"jira", "--config", cfg, "--output=json",
		"issue", "weblink", "JCT-1", "--dry-run",
	}, "required_flag_missing")
}

func assertWeblinkURLErrorEnvelope(t *testing.T, stdout, stderr []byte, args []string, wantCode string) {
	t.Helper()
	var env struct {
		Errors []struct {
			Code string `json:"code"`
			Flag string `json:"flag"`
		} `json:"errors"`
	}
	decodeErrorEnvelopeFromStdout(t, stdout, stderr, args, &env)
	if len(env.Errors) == 0 {
		t.Fatalf("error envelope has no errors\nstdout=%s", stdout)
	}
	if got := env.Errors[0].Code; got != wantCode {
		t.Fatalf("errors[0].code = %q, want %q\nstdout=%s", got, wantCode, stdout)
	}
	if got := env.Errors[0].Flag; got != "url" {
		t.Fatalf("errors[0].flag = %q, want url\nstderr=%s", got, stderr)
	}
}

// TestWeblinkDryRunStatesRemoteNotChecked — a valid URL passes
// dry-run, but the preview must be honest that reachability was not
// verified.
func TestWeblinkDryRunStatesRemoteNotChecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("weblink --dry-run made a live request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--output=json",
		"issue", "weblink", "JCT-1", "--url", "https://example.com/page", "--dry-run")
	if code != 0 {
		t.Fatalf("weblink --dry-run exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
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
	checked, ok := env.Data["url_remote_checked"].(bool)
	if !ok || checked {
		t.Fatalf("weblink --dry-run must report url_remote_checked=false (honest about not verifying reachability): %#v", env.Data)
	}
}

func TestWeblinkLiveStatesRemoteNotChecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue/JCT-1/remotelink" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("JIRA_TOKEN_DEFAULT", "test-token")

	stdout, stderr, code := runJira(
		t,
		"--config", jiraConfig(t, srv.URL),
		"--output=json",
		"issue", "weblink", "JCT-1",
		"--url", "https://example.com/page",
	)
	if code != 0 {
		t.Fatalf("weblink live exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	if checked, ok := env.Data["url_remote_checked"].(bool); !ok || checked {
		t.Fatalf("live weblink must report url_remote_checked=false: %#v", env.Data)
	}
}

// TestBoardsListRejectsDryRunFlag — `boards list` cannot honor a
// --dry-run flag (it always does a live read + cache write), so the
// flag is removed. An unknown flag is a usage error.
func TestBoardsListRejectsDryRunFlag(t *testing.T) {
	stdout, stderr, code := runJira(t, "boards", "list", "--dry-run")
	if code == 0 {
		t.Fatalf("boards list still accepts --dry-run; the dishonest flag should be removed")
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "dry-run") && !strings.Contains(combined, "unknown flag") {
		t.Fatalf("expected an unknown-flag error for boards list --dry-run, got: %s", combined)
	}
}

// TestCacheBoardsRejectsDryRunFlag — same for the cache primer.
func TestCacheBoardsRejectsDryRunFlag(t *testing.T) {
	stdout, stderr, code := runJira(t, "cache", "boards", "--dry-run")
	if code == 0 {
		t.Fatalf("cache boards still accepts --dry-run; the dishonest flag should be removed")
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "dry-run") && !strings.Contains(combined, "unknown flag") {
		t.Fatalf("expected an unknown-flag error for cache boards --dry-run, got: %s", combined)
	}
}
