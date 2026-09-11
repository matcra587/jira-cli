package contract

// Contract tests for default_board resolution.
//
// `profiles.<name>.default_board` set + no `--board` flag → applied as
// if the user had passed `--board NAME`. Explicit `--board "Other"`
// overrides. Explicit `--board ""` suppresses the default. Missing /
// ambiguous default → exit 3 with the pinned error wording.
//
// These tests exercise `issue list` end-to-end against an httptest fake
// of `/rest/api/3/search/jql`. The boards cache is primed on disk (JSON
// matching internal/cache.Entry shape) so BoardService.ResolveOne can
// resolve names without hitting the agile API.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cache"
)

// primedBoardsCache writes a boards cache entry whose Data is the JSON
// `[]Board` payload BoardService.ResolveOne reads. The cache directory
// path is the per-config/site/profile location under cacheRoot.
func primedBoardsCache(t *testing.T, cacheRoot, cfg, profile, baseURL, boardsJSON string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	if _, err := cache.Write(cache.Key(profile, baseURL, cfg), "boards", json.RawMessage(boardsJSON)); err != nil {
		t.Fatalf("cache.Write boards: %v", err)
	}
}

// jiraConfigWithDefaultBoard writes a config.toml with the supplied
// profile + default_board value. baseURL points at the httptest server.
func jiraConfigWithDefaultBoard(t *testing.T, baseURL, defaultBoard string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `default_profile = "default"
queries_path = "` + filepath.ToSlash(t.TempDir()) + `/queries"

[[profiles]]
name = "default"
base_url = "` + baseURL + `"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
default_board = "` + defaultBoard + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// fakeSearchServer captures the JQL body of every POST /search/jql and
// returns a single-issue response. Tests inspect lastJQL after the
// invocation.
type fakeSearchServer struct {
	srv     *httptest.Server
	lastJQL requestCapture[string]
}

func newFakeSearchServer(t *testing.T) *fakeSearchServer {
	t.Helper()
	f := &fakeSearchServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql" {
			var body struct {
				JQL string `json:"jql"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode search body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.lastJQL.Add(body.JQL)
			_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"ENG-1","fields":{"summary":"hit"}}]}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// twoBoardCacheJSON is a minimal cache payload with two distinct boards.
// "Engineering Sprint" → ENG project. "Platform Roadmap" → PLAT project.
const twoBoardCacheJSON = `[
  {"id":42,"name":"Engineering Sprint","type":"scrum","project_keys":["ENG"]},
  {"id":99,"name":"Platform Roadmap","type":"kanban","project_keys":["PLAT"]}
]`

// default_board set + no flag → precedence "default_board".
func TestDefaultBoardAppliedWhenNoFlag(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithDefaultBoard(t, srv.srv.URL, "Engineering Sprint")
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)

	cmd := exec.Command(buildJiraBinary(t), "--config", cfg, "issue", "list", "--output=json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list error = %v\n%s", err, out)
	}

	if !strings.Contains(srv.lastJQL.Last(), "project in (ENG)") {
		t.Errorf("emitted JQL did not contain board scope: %q", srv.lastJQL.Last())
	}

	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if precedence, _ := data["precedence"].(string); precedence != "default_board" {
		t.Errorf("data.precedence = %q; want %q", precedence, "default_board")
	}
	scope, _ := data["board_scope"].(map[string]any)
	if applied, _ := scope["applied"].(bool); !applied {
		t.Errorf("data.board_scope.applied = false; want true")
	}
}

// explicit --board overrides default_board (precedence "flag").
func TestDefaultBoardOverriddenByExplicitFlag(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithDefaultBoard(t, srv.srv.URL, "Engineering Sprint")
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)

	cmd := exec.Command(
		buildJiraBinary(t), "--config", cfg,
		"issue", "list", "--output=json",
		"--board", "Platform Roadmap",
	)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list error = %v\n%s", err, out)
	}

	if !strings.Contains(srv.lastJQL.Last(), "project in (PLAT)") {
		t.Errorf("emitted JQL did not contain flag-supplied scope: %q", srv.lastJQL.Last())
	}

	var env map[string]any
	_ = json.Unmarshal(out, &env)
	data, _ := env["data"].(map[string]any)
	if precedence, _ := data["precedence"].(string); precedence != "flag" {
		t.Errorf("data.precedence = %q; want %q", precedence, "flag")
	}
}

// `--board ""` suppresses default (precedence "none", no scope).
func TestDefaultBoardSuppressedByEmptyFlag(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithDefaultBoard(t, srv.srv.URL, "Engineering Sprint")
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)

	cmd := exec.Command(
		buildJiraBinary(t), "--config", cfg,
		"issue", "list", "--output=json",
		"--board", "",
	)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list error = %v\n%s", err, out)
	}

	if strings.Contains(srv.lastJQL.Last(), "project in (") {
		t.Errorf("emitted JQL contained scope when --board='' should suppress it: %q", srv.lastJQL.Last())
	}

	var env map[string]any
	_ = json.Unmarshal(out, &env)
	data, _ := env["data"].(map[string]any)
	if precedence, _ := data["precedence"].(string); precedence != "none" {
		t.Errorf("data.precedence = %q; want %q", precedence, "none")
	}
	scope, _ := data["board_scope"].(map[string]any)
	if applied, _ := scope["applied"].(bool); applied {
		t.Errorf("data.board_scope.applied = true; want false (--board '' suppresses)")
	}
}

// default_board referencing a missing entry → exit 3 + pinned error
// wording. Uses buildJiraBinary so the exit-code assertion sees the
// CLI's real exit status; `go run` would wrap any non-zero exit as 1
// and lose the signal.
func TestDefaultBoardMissingExitsThreeWithPinnedWording(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithDefaultBoard(t, srv.srv.URL, "Nonexistent")
	// Cache has 2 boards but neither matches "Nonexistent".
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)

	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, "--config", cfg, "issue", "list", "--output=json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit; got success\nstdout:%s\nstderr:%s", stdout.String(), stderr.String())
	}
	exitCode := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exitCode = ee.ExitCode()
	}
	if exitCode != 3 {
		t.Errorf("exit code = %d; want 3 (validation)\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}

	// Pinned literal wording, checked against the decoded envelope message so the
	// literal quotes match (raw machine-mode stdout carries them JSON-escaped):
	// `default_board "X" not found in boards cache — run "jira cache boards --refresh" or unset with "jira config set profiles.<profile>.default_board ''"`
	wantSubstr := `default_board "Nonexistent" not found in boards cache — run "jira cache boards --refresh" or unset with "jira config set profiles.default.default_board ''"`
	var env map[string]any
	decodeErrorEnvelopeFromStdout(t, stdout.Bytes(), stderr.Bytes(), cmd.Args, &env)
	errs, _ := env["errors"].([]any)
	found := false
	for _, e := range errs {
		m, _ := e.(map[string]any)
		if msg, _ := m["message"].(string); strings.Contains(msg, wantSubstr) {
			found = true
		}
	}
	if !found {
		t.Errorf("error envelope did not contain pinned wording.\nwant substring:\n  %s\nstdout:\n  %s", wantSubstr, stdout.String())
	}
}

// default_board ambiguous (two boards share name) → exit 3,
// envelope errors[0].candidates[] populated. Uses buildJiraBinary +
// split stdout/stderr so the envelope JSON is parseable.
func TestDefaultBoardAmbiguousReturnsCandidates(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	const ambiguousCacheJSON = `[
  {"id":42,"name":"Engineering","type":"scrum","project_keys":["ENG"]},
  {"id":99,"name":"Engineering","type":"kanban","project_keys":["OPS"]}
]`

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithDefaultBoard(t, srv.srv.URL, "Engineering")
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, ambiguousCacheJSON)

	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, "--config", cfg, "issue", "list", "--output=json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	var env map[string]any
	decodeErrorEnvelopeFromStdout(t, stdout.Bytes(), stderr.Bytes(), cmd.Args, &env)
	errs, _ := env["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("envelope.errors empty; expected ambiguous-board error\nstdout:\n%s", stdout.String())
	}
	first, _ := errs[0].(map[string]any)
	cands, _ := first["candidates"].([]any)
	if len(cands) != 2 {
		t.Errorf("errors[0].candidates len = %d; want 2", len(cands))
	}
}
