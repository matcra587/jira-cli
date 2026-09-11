package contract

// "Profile change mid-session": per-profile default_board semantics.
// Two profiles in one config; both
// share the same boards cache (cache is per-profile but we intentionally
// prime ONE cache per profile so each invocation reads its own). The
// default profile resolves to "Engineering Sprint" → ENG, the work
// profile resolves to "Platform Roadmap" → PLAT. Both runs share the
// same OS process invocation pattern (back-to-back invocations of the
// same compiled binary).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func jiraConfigTwoProfilesEachDefaultBoard(t *testing.T, baseURL string) string {
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
default_board = "Engineering Sprint"

[[profiles]]
name = "work"
base_url = "` + baseURL + `"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
default_board = "Platform Roadmap"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// TestDefaultBoardPerProfileSemantics — `--profile work` resolves
// "Platform Roadmap" against the work cache; `--profile default` (the
// active fallback when --profile is omitted) resolves "Engineering
// Sprint" against the default cache. Both share the same config file
// and the same fake Jira server.
func TestDefaultBoardPerProfileSemantics(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	srv := newFakeSearchServer(t)
	cfg := jiraConfigTwoProfilesEachDefaultBoard(t, srv.srv.URL)

	// Each profile has its OWN cache namespace under
	// XDG_CACHE_HOME/jira-cli/<cache-key>/boards.json. Both prime with
	// the same two-board JSON for simplicity; the resolver still picks
	// the per-profile config's name.
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)
	primedBoardsCache(t, cacheRoot, cfg, "work", srv.srv.URL, twoBoardCacheJSON)

	// Run 1: default profile (no --profile flag)
	cmd := exec.Command(buildJiraBinary(t), "--config", cfg, "issue", "list", "--output=json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list (default profile) error = %v\n%s", err, out)
	}
	if !strings.Contains(srv.lastJQL.Last(), "project in (ENG)") {
		t.Errorf("default profile JQL = %q; want project in (ENG)", srv.lastJQL.Last())
	}
	var env1 map[string]any
	_ = json.Unmarshal(out, &env1)
	data1, _ := env1["data"].(map[string]any)
	scope1, _ := data1["board_scope"].(map[string]any)
	if name, _ := scope1["name"].(string); name != "Engineering Sprint" {
		t.Errorf("default profile scope.name = %q; want Engineering Sprint", name)
	}

	// Run 2: --profile work
	cmd = exec.Command(
		buildJiraBinary(t), "--config", cfg,
		"--profile", "work",
		"issue", "list", "--output=json",
	)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list (work profile) error = %v\n%s", err, out)
	}
	if !strings.Contains(srv.lastJQL.Last(), "project in (PLAT)") {
		t.Errorf("work profile JQL = %q; want project in (PLAT)", srv.lastJQL.Last())
	}
	var env2 map[string]any
	_ = json.Unmarshal(out, &env2)
	data2, _ := env2["data"].(map[string]any)
	scope2, _ := data2["board_scope"].(map[string]any)
	if name, _ := scope2["name"].(string); name != "Platform Roadmap" {
		t.Errorf("work profile scope.name = %q; want Platform Roadmap", name)
	}
}
