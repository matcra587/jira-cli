package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAutomaticThemeActualSelectionAndEnvironmentProvenance(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(settings, []byte("theme='monokai'\ntheme_path='missing-palette.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_THEME= AUTO ", "COLORFGBG=0;15", "JIRA_KEYRING=false", "JIRA_THEME_LIGHT=invalid-losing-pair", "JIRA_THEME_DARK=dark", "CLOG_THEME_LIGHT=light", "CLOG_THEME_DARK=dark"}, "config", "list", "--config", settings, "--output=json", "--no-input")
	if exit != 0 || stderr != "" || strings.Contains(stdout, "theme_unknown") || strings.Contains(stdout, "theme_invalid") {
		t.Fatalf("automatic environment did not override file selection: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var frame struct {
		Data envelope.ConfigListOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatalf("automatic environment provenance changed: %v %q", err, stdout)
	}
	found := false
	for _, setting := range frame.Data.Settings {
		if setting.Key == "theme" {
			found = setting.Value == " AUTO " && setting.Source == "env"
		}
	}
	if !found {
		t.Fatal("automatic environment lost exact authored value or winning source")
	}
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			env := []string{"JIRA_KEYRING=false", "COLORFGBG=0;15"}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "theme", "--name=auto", "--config", path, "--no-input", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("automatic selector publication failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if mode != "human" {
				out := decodeConfigTheme(t, stdout, mode)
				if out.Name != "auto" || !out.Changed || !out.Created || !out.NamePresent {
					t.Fatal("automatic publication lost typed facts")
				}
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "config", "theme", "--config", path, "--no-input", "--output=json")
			out := decodeConfigTheme(t, stdout, "json")
			if exit != 0 || stderr != "" || out.Name != "auto" || !out.NamePresent || strings.Contains(stdout, "theme_unknown") {
				t.Fatal("published automatic selector did not resolve without warning")
			}
		})
	}
}

func TestAutomaticThemeActualOfflineSchemaAndCompletion(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"agent", "schema", "--path=config theme", "--output=json"},
		{"--@complete=configvalue", "--", "theme"},
	} {
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "auto") {
			t.Fatalf("automatic selector missing from local discovery: %v exit=%d stdout=%q stderr=%q", args, exit, stdout, stderr)
		}
	}
}

func TestAutomaticThemeActualBashCompletion(t *testing.T) {
	env := []string{"JIRA_KEYRING=false"}
	script, stderr, exit := runBinaryEnv(t, env, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("Bash generation failed", exit, stderr)
	}
	for _, args := range [][]string{
		{"jira", "config", "set", "theme", "au"},
		{"jira", "config", "theme", "--name", "au"},
	} {
		values := completionArgumentReplies(t, script, env, args)
		if !slices.Equal(values, []string{"auto"}) {
			t.Fatalf("generated automatic theme candidates=%q args=%q", values, args)
		}
	}
}

func TestAutomaticThemeActualMigrationAndExactRollback(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
			// Every migrated profile must stay independent of native credential stores.
			const original = "# Retain all legacy bytes.\n[THEME]\nNAME=' AUTO '\n[[profiles]]\nname='default'\nsecret_backend='env'\n[[profiles]]\nname='unselected'\nsecret_backend='env'\n"
			if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_KEYRING=false", "COLORFGBG=0;15", "DBUS_SESSION_BUS_ADDRESS=/dev/null"}
			args := []string{"config", "migrate", "--source", source, "--config", target, "--recovery-dir", recovery, "--no-input", "--output=" + mode}
			stdout, stderr, exit := runBinaryEnv(t, env, append(slices.Clone(args), "--dry-run")...)
			wantPreview := "theme"
			if mode == "human" {
				wantPreview = "would convert profiles"
			}
			if exit != 0 || stderr != "" || !strings.Contains(stdout, wantPreview) {
				t.Fatalf("automatic migration preview failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatal("automatic migration preview published recovery")
			}
			stdout, stderr, exit = runBinaryEnv(t, env, append(args, "--yes")...)
			if exit != 0 || stderr != "" {
				t.Fatalf("automatic migration publication failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "config", "get", "theme", "--config", target, "--output=json")
			if out := decodeConfigGet(t, "json", stdout); exit != 0 || stderr != "" || !out.Present || out.Value != " AUTO " {
				t.Fatal("migration did not retain the authored automatic selector")
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "config", "rollback", "--journal", filepath.Join(recovery, "migration.json"), "--yes", "--no-input", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("automatic migration rollback failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			data, err := os.ReadFile(source)
			if err != nil || string(data) != original {
				t.Fatal("automatic rollback changed selected or unselected original bytes", err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("automatic rollback lost target absence")
			}
		})
	}
}
