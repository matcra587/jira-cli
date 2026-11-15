package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestMigrationTUIActivatesPreferencesAndFreshRollbackRetainsOriginal(t *testing.T) {
	t.Parallel()
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "same"}[same], func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
			if same {
				target = source
			}
			original := []byte("# exact source retained\n[[profiles]]\nname='default'\nsecret_backend='env'\n[tui]\npreview='bottom'\npreview_size=55\nrefresh_interval=90\n[[tui.sections]]\ntitle='Queue'\njql='project = DEMO ORDER BY key ASC'\n[tui.keys]\ncopy_key=['alt+y']\n")
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_API_TOKEN=", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_NO_UPDATE_CHECK=1"}
			args := []string{"config", "migrate", "--source=" + source, "--config=" + target, "--recovery-dir=" + recovery, "--no-input", "--output=json"}
			invoke := func(extra string) envelope.ConfigMigrateOutput {
				t.Helper()
				stdout, stderr, exit := runBinaryEnv(t, env, append(args, extra)...)
				var frame struct {
					OK   bool                         `json:"ok"`
					Data envelope.ConfigMigrateOutput `json:"data"`
				}
				if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || !frame.Data.Ready || len(frame.Data.Unmapped) != 0 || len(frame.Data.Deferred) != 0 {
					t.Fatalf("TUI migration failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				return frame.Data
			}
			preview := invoke("--dry-run")
			if preview.RecoveryPublished || preview.Receipt != nil {
				t.Fatal("preview acquired effects")
			}
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatal("preview created recovery", err)
			}
			live := invoke("--yes")
			if live.Receipt == nil || live.Receipt.Status != "applied" || live.Receipt.CredentialState != "external" {
				t.Fatal("live migration lost external credential receipt")
			}
			settings, err := config.Load(config.Options{Path: target, Environ: func() []string { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			current := settings.Config
			if !current.TUIPreview || current.TUIPreviewDock != config.PreviewDockBottom || current.TUIPreviewSize != 55 || current.TUIRefreshInterval != 90 || len(current.TUIWorkspace.Sections) != 1 || current.TUIWorkspace.Sections[0].JQL != "project = DEMO ORDER BY key ASC" || current.TUIWorkspace.Keys["copy-key"][0] != "alt+y" {
				t.Fatal("published settings did not reach current runtime consumers")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "rollback", "--journal="+filepath.Join(recovery, "migration.json"), "--yes", "--no-input", "--output=json")
			if exit != 0 || stderr != "" || !strings.Contains(stdout, `"restored":true`) {
				t.Fatal("fresh recorded-policy rollback failed", exit, stdout, stderr)
			}
			got, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("rollback changed original settings", err)
			}
			if !same {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("rollback lost target absence", err)
				}
			}
		})
	}
}

func TestLegacyConfigFailureSuggestsMigrationWithoutChangingFile(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"human", "json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "legacy.toml")
			original := []byte("[[profiles]]\nname='default'\nsecret_backend='env'\n")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_API_TOKEN=", "OP_SERVICE_ACCOUNT_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null"}, "issue", "list", "--as-jql", "--config="+path, "--output="+mode)
			if exit != 3 || !strings.Contains(stdout+stderr, "jira config migrate") {
				t.Fatal("legacy config lost migration remedy", exit, stdout, stderr)
			}
			if mode != "human" {
				var frame struct {
					Errors []struct{ Code, Hint, Next string } `json:"errors"`
				}
				if stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "config_invalid" || frame.Errors[0].Next != "jira config migrate" || !strings.Contains(frame.Errors[0].Hint, "--dry-run") {
					t.Fatal("machine failure lost typed recovery advice", stdout, stderr)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("legacy detection changed configuration", err)
			}
		})
	}
}
