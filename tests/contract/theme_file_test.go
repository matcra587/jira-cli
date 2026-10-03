package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomThemeActualHelpUsesSelectedFileBeforeRendererSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "palette.toml")
	if err := os.WriteFile(settings, []byte("theme='monokai'\ntheme_path='palette.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(palette, []byte("base='light'\n[colors]\nhelp_command='#123456'\nhelp_section='#123456'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "--config", settings, "--output=human", "--color=always")
	if exit != 0 || stdout != "" || !strings.Contains(stderr, "38;2;18;52;86") {
		t.Fatalf("actual help ignored custom theme selected before construction: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "--config", settings, "--output=human", "--color=never")
	if exit != 0 || stdout != "" || strings.ContainsAny(stderr, "\x1b\a") || !strings.Contains(stderr, "jira") {
		t.Fatalf("custom theme leaked controls through color-never help: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestCustomThemeActualEntityRenderingUsesPaletteAndKeepsMachineValuesPlain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "palette.toml")
	if err := os.WriteFile(settings, []byte("theme_path='palette.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(palette, []byte("base='dark'\nentity_colors=['#242424']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1" {
			t.Errorf("unexpected theme fixture request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":{"summary":"theme fixture","assignee":{"accountId":"user-1","displayName":"Palette Person"}}}`))
	}))
	t.Cleanup(server.Close)
	for _, mode := range []string{"human", "json", "compact", "agent"} {
		t.Run(mode, func(t *testing.T) {
			env := append(searchEnv(server.URL), "TERM=xterm-256color", "COLORTERM=truecolor")
			args := []string{"issue", "view", "ABC-1", "--comments=0", "--config", settings, "--color=always"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" || !strings.Contains(stdout, "Palette Person") {
				t.Fatalf("custom entity render failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "38;2;36;36;36mPalette Person") {
					t.Fatalf("actual assignee sink discarded custom color: %q", stdout)
				}
			} else if strings.ContainsAny(stdout, "\x1b\a") {
				t.Fatal("custom entity styling leaked into a machine value")
			}
		})
	}
	if err := os.WriteFile(palette, []byte("base='dark'\nentity_colors=['#ff0000']\n[colors]\nred='#ff0000'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(searchEnv(server.URL), "TERM=xterm-256color", "COLORTERM=truecolor")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "view", "ABC-1", "--comments=0", "--config", settings, "--output=human", "--color=always")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Palette Person") || strings.Contains(stdout, "mPalette Person") {
		t.Fatalf("fully reserved custom palette must render the assignee plain: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestCustomThemeActualInvalidFileWarnsThroughEnvelopeAndKeepsLocalCommandsUsable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(settings, []byte("theme='monokai'\ntheme_path='missing.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "version", "--config", settings, "--output=json")
	var frame struct {
		OK       bool `json:"ok"`
		Warnings []struct {
			Type string `json:"type"`
		} `json:"warnings"`
	}
	if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "theme_invalid" {
		t.Fatalf("invalid custom file disabled local command or escaped its envelope: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_THEME=monokai"}, "version", "--config", settings, "--output=json")
	if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Warnings) != 0 {
		t.Fatalf("environment override consulted the losing missing file: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}
