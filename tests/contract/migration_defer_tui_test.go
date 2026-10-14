package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestMigrationDeferredTUIRequiresExplicitChoiceAndRetainsExactRecovery(t *testing.T) {
	t.Parallel()
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "same"}[same], func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
			if same {
				target = source
			}
			original := []byte("# preserve exact TUI state\ndefault_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='person@example.invalid'\nsecret_backend='env'\n[tui]\nrefresh_interval=60\ndefault_tab='issues'\ntabs=['issues','private-value-marker']\n[keys]\n")
			// The unknown root table must not be admitted even with explicit deferral.
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_SECRET_BACKEND=", "JIRA_API_TOKEN_ENV=", "OP_SERVICE_ACCOUNT_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_NO_UPDATE_CHECK=1"}
			stdout, _, exit := runBinaryEnv(t, env, "config", "migrate", "--source="+source, "--config="+target, "--defer-tui", "--dry-run", "--output=json", "--no-input")
			var invalid struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &invalid) != nil || exit == 0 || invalid.OK || len(invalid.Errors) != 1 || invalid.Errors[0].Code != "config_invalid" {
				t.Fatalf("deferral admitted unknown schema or invocation failed: exit=%d stdout=%s", exit, stdout)
			}
			original = bytes.TrimSuffix(original, []byte("[keys]\n"))
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			invoke := func(args ...string) envelope.ConfigMigrateOutput {
				t.Helper()
				stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output=json", "--no-input")...)
				var frame struct {
					OK   bool                         `json:"ok"`
					Data envelope.ConfigMigrateOutput `json:"data"`
				}
				if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || strings.Contains(stdout, "private-value-marker") {
					t.Fatalf("migration invocation failed or exposed values: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				return frame.Data
			}
			base := []string{"config", "migrate", "--source=" + source, "--config=" + target, "--recovery-dir=" + recovery}
			strict := invoke(append(slices.Clone(base), "--dry-run")...)
			if len(strict.Deferred) != 0 || strict.Ready || !slices.Equal(strict.Unmapped, []string{"tui.default_tab", "tui.refresh_interval", "tui.tabs"}) {
				t.Fatal("ordinary conversion silently deferred TUI")
			}
			preview := invoke(append(slices.Clone(base), "--defer-tui", "--dry-run")...)
			if !slices.Equal(preview.Deferred, strict.Unmapped) || !preview.Ready || len(preview.Unmapped) != 0 || preview.RecoveryPublished || preview.Receipt != nil {
				t.Fatal("explicit preview not ready or had effects")
			}
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatal("preview created recovery")
			}
			out := invoke(append(slices.Clone(base), "--defer-tui", "--yes")...)
			if !slices.Equal(out.Deferred, strict.Unmapped) || !out.RecoveryPublished || out.Receipt == nil || out.Receipt.Status != "applied" || out.Receipt.CredentialState != "external" {
				t.Fatal("live migration lost receipt")
			}
			candidate, err := os.ReadFile(target)
			if err != nil || bytes.Contains(candidate, []byte("tui")) || bytes.Contains(candidate, []byte("private-value-marker")) {
				t.Fatal("deferred preferences became inert runtime settings", err)
			}
			var record struct {
				Version        int    `json:"version"`
				MappingVersion int    `json:"mapping_version"`
				SourceKind     string `json:"source_kind"`
				Candidate      []byte `json:"candidate"`
				Source         struct {
					Contents []byte `json:"contents"`
				} `json:"source"`
			}
			data, err := os.ReadFile(filepath.Join(recovery, "configuration.json"))
			if err != nil || json.Unmarshal(data, &record) != nil || record.Version != 10 || record.MappingVersion != 9 || record.SourceKind != "legacy" || !bytes.Equal(record.Candidate, candidate) || !bytes.Equal(record.Source.Contents, original) {
				t.Fatal("recovery lost exact bytes or explicit policy", err)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "rollback", "--journal="+filepath.Join(recovery, "migration.json"), "--yes", "--output=json", "--no-input")
			var rollback struct {
				OK   bool                          `json:"ok"`
				Data envelope.ConfigRollbackOutput `json:"data"`
			}
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &rollback) != nil || !rollback.OK || !rollback.Data.Restored {
				t.Fatal("fresh rollback failed", exit, stderr)
			}
			restored, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(restored, original) {
				t.Fatal("rollback lost exact original", err)
			}
			if !same {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("rollback lost target absence")
				}
			}
		})
	}
}

func TestMigrationDeferredTUIPreviewHumanAndCompactKeepValuesPrivate(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"human", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "old.toml")
			target := filepath.Join(dir, "new.toml")
			recovery := filepath.Join(dir, "recovery")
			original := []byte("[[profiles]]\nname='default'\nsecret_backend='env'\n[tui]\ndefault_tab='private-tab-marker'\n")
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_API_TOKEN=", "JIRA_SECRET_BACKEND=", "JIRA_API_TOKEN_ENV=", "OP_SERVICE_ACCOUNT_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_NO_UPDATE_CHECK=1"}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "migrate", "--source="+source, "--config="+target, "--recovery-dir="+recovery, "--defer-tui", "--dry-run", "--output="+mode, "--no-input")
			if exit != 0 || stderr != "" || strings.Contains(stdout, "private-tab-marker") {
				t.Fatal("preview failed or exposed TUI values", exit, stderr)
			}
			if mode == "compact" {
				var out envelope.ConfigMigrateOutput
				if json.Unmarshal([]byte(stdout), &out) != nil || !out.Ready || !out.DryRun || !slices.Equal(out.Deferred, []string{"tui.default_tab"}) || len(out.Unmapped) != 0 || out.RecoveryPublished || out.Receipt != nil {
					t.Fatal("compact preview lost truthful deferral state")
				}
			} else if human := strings.Join(strings.Fields(stdout), " "); !strings.Contains(human, "would convert") || !strings.Contains(human, "TUI settings deferred; retained only in original source/recovery: tui.default_tab") {
				t.Fatal("human preview omitted retention boundary")
			}
			for _, path := range []string{target, recovery} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("preview published local state")
				}
			}
			got, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("preview changed exact source", err)
			}
		})
	}
}
