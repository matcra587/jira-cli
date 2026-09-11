package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cache"
)

func TestDynamicCompletionUsesExplicitConfigForProfiles(t *testing.T) {
	bin := buildJiraBinary(t)
	cfg := writeCompletionConfig(t)
	xdg := filepath.Join(t.TempDir(), "xdg")

	cmd := exec.Command(bin, "--config", cfg, "--@complete=profile", "--", "")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("profile completion: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"work", "play"} {
		if !strings.Contains(got, want) {
			t.Fatalf("profile completion missing %q from explicit config:\n%s", want, got)
		}
	}
	if strings.Contains(got, "default") {
		t.Fatalf("profile completion fell back to default config instead of explicit --config:\n%s", got)
	}
}

func TestDynamicCompletionUsesExplicitProfileForCache(t *testing.T) {
	bin := buildJiraBinary(t)
	cfg := writeCompletionConfig(t)
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	if _, err := cache.Write(cacheKeyForTestConfig(t, cfg, "play", "https://play.atlassian.net"), "boards", json.RawMessage(`[
		{"id":77,"name":"Play Board","type":"kanban","project_keys":["PLY"]}
	]`)); err != nil {
		t.Fatalf("cache.Write play boards: %v", err)
	}
	if _, err := cache.Write(cacheKeyForTestConfig(t, cfg, "work", "https://work.atlassian.net"), "boards", json.RawMessage(`[
		{"id":11,"name":"Work Board","type":"scrum","project_keys":["WRK"]}
	]`)); err != nil {
		t.Fatalf("cache.Write work boards: %v", err)
	}

	cmd := exec.Command(bin, "--config", cfg, "--profile", "play", "--@complete=cacheboard", "--", "")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cacheboard completion: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Play Board\t77") {
		t.Fatalf("cacheboard completion did not use --profile play:\n%s", got)
	}
	if strings.Contains(got, "Work Board") {
		t.Fatalf("cacheboard completion leaked default profile candidates:\n%s", got)
	}

	cmd = exec.Command(bin, "--@complete=cacheboard", "--", "--config="+cfg, "--profile=play")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forwarded cacheboard completion: %v\n%s", err, out)
	}
	got = string(out)
	if !strings.Contains(got, "Play Board\t77") || strings.Contains(got, "Work Board") {
		t.Fatalf("forwarded completion did not preserve --config/--profile:\n%s", got)
	}

	cmd = exec.Command(bin, "--@complete=cacheboard", "--", "issue", "list", "--config", cfg, "--profile", "play", "--board")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("post-command profile completion: %v\n%s", err, out)
	}
	got = string(out)
	if !strings.Contains(got, "Play Board\t77") || strings.Contains(got, "Work Board") {
		t.Fatalf("completion ignored Cobra-valid post-command --profile:\n%s", got)
	}
}

func TestLinkTypeCompletionInsertsTypeName(t *testing.T) {
	bin := buildJiraBinary(t)
	cfg := writeCompletionConfig(t)
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	if _, err := cache.Write(cacheKeyForTestConfig(t, cfg, "work", "https://work.atlassian.net"), "linktypes", json.RawMessage(`[
		{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"}
	]`)); err != nil {
		t.Fatalf("cache.Write linktypes: %v", err)
	}

	cmd := exec.Command(bin, "--config", cfg, "--@complete=cachelinktype", "--", "")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cachelinktype completion: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Blocks\t10000 (is blocked by / blocks)") {
		t.Fatalf("link type completion must insert link type name and describe id:\n%s", got)
	}
	if strings.Contains(got, "10000\tBlocks") {
		t.Fatalf("link type completion inserted id for a name-valued --type flag:\n%s", got)
	}
}

func TestAliasExpansionSkipsValueTakingGlobalFlags(t *testing.T) {
	var seenJQL requestCapture[string]
	srv := newJQLCaptureServer(t, &seenJQL)
	cfg := jiraConfig(t, srv.URL)
	bin := buildJiraBinary(t)

	out, err := exec.Command(bin, "--config", cfg, "alias", "set", "mine", "--", "issue", "list", "--jql", "project = PROJ").CombinedOutput()
	if err != nil {
		t.Fatalf("alias set: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "--config", cfg, "--output", "json", "--timeout", "30s", "mine")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias expansion after global flags: %v\n%s", err, out)
	}
	if seenJQL.Last() != "project = PROJ ORDER BY updated DESC" {
		t.Fatalf("alias expansion sent JQL %q, want project = PROJ ORDER BY updated DESC\n%s", seenJQL.Last(), out)
	}

	seenJQL.Reset()
	cmd = exec.Command(bin, "mine", "--config", cfg, "--output=compact")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias expansion with post-command --config: %v\n%s", err, out)
	}
	if seenJQL.Last() != "project = PROJ ORDER BY updated DESC" {
		t.Fatalf("post-command --config alias expansion sent JQL %q, want project = PROJ ORDER BY updated DESC\n%s", seenJQL.Last(), out)
	}
}

func writeCompletionConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `default_profile = "work"

[[profiles]]
name = "work"
base_url = "https://work.atlassian.net"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800

[[profiles]]
name = "play"
base_url = "https://play.atlassian.net"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func newJQLCaptureServer(t *testing.T, seen *requestCapture[string]) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		seen.Add(body.JQL)
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"PROJ-1","fields":{"summary":"Alias hit"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}
