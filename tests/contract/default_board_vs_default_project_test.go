package contract

// / : when both `default_project` and `default_board` are set on
// the same profile, `default_board` wins **exclusively** on commands
// that consume `--board`. No intersection, no union; default_project is
// ignored on those commands. Documented in spec Assumptions section
// and research.md .

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jiraConfigWithBothDefaults writes a config.toml with BOTH
// default_project and default_board set on the same profile.
func jiraConfigWithBothDefaults(t *testing.T, baseURL, defaultProject, defaultBoard string) string {
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
default_project = "` + defaultProject + `"
default_board = "` + defaultBoard + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// TestDefaultBoardWinsExclusivelyOverDefaultProject — board's project
// keys (ENG) drive the JQL, NOT the default_project value (UNRELATED).
// No union: there's no `project = UNRELATED OR project in (ENG)`.
// No intersection: just the board's project keys.
func TestDefaultBoardWinsExclusivelyOverDefaultProject(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	srv := newFakeSearchServer(t)
	cfg := jiraConfigWithBothDefaults(
		t, srv.srv.URL,
		"UNRELATED",          // default_project intentionally pointing nowhere
		"Engineering Sprint", // default_board → ENG
	)
	primedBoardsCache(t, cacheRoot, cfg, "default", srv.srv.URL, twoBoardCacheJSON)

	cmd := exec.Command(buildJiraBinary(t), "--config", cfg, "issue", "list", "--output=json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue list error = %v\n%s", err, out)
	}

	// Board's projects (ENG) drive the JQL.
	if !strings.Contains(srv.lastJQL.Last(), "project in (ENG)") {
		t.Errorf("emitted JQL missing board scope: %q", srv.lastJQL.Last())
	}

	// default_project must NOT bleed into the JQL.
	if strings.Contains(srv.lastJQL.Last(), "UNRELATED") {
		t.Errorf("default_project bled into JQL — board should win exclusively: %q", srv.lastJQL.Last())
	}

	var env map[string]any
	_ = json.Unmarshal(out, &env)
	data, _ := env["data"].(map[string]any)
	if precedence, _ := data["precedence"].(string); precedence != "default_board" {
		t.Errorf("data.precedence = %q; want %q", precedence, "default_board")
	}
}
