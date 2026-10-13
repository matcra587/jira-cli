//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

const legacyMatrixFixture = "default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://work.invalid'\nemail='work@example.invalid'\n[[profiles]]\nname='other'\nbase_url='https://other.invalid'\nemail='other@example.invalid'\n"

func TestConfigMigrationFlagsControlPreviewAndConfirmationBeforeProviders(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"work", "other"} {
		run := mx4Run(t, mx4Opts{Files: map[string]string{"old.toml": legacyMatrixFixture}}, "config", "migrate", "--source=old.toml", "--source-profile="+profile, "--config=new.toml", "--recovery-dir=planned-recovery", "--legacy-keyring-service=historical-matrix-service", "--dry-run", "--output=json")
		var out envelope.ConfigMigrateOutput
		mx4Data(t, mx4OK(t, run), &out)
		if out.Profile != profile || out.SourceCredential.Service != "historical-matrix-service" || out.SourceCredential.Account != profile+".invalid/"+profile || out.Recovery.Directory != filepath.Join(run.Workdir, "planned-recovery") || out.Receipt != nil || out.RecoveryPublished || !out.DryRun {
			t.Fatal("migration flags lost selected metadata or published during preview")
		}
		if _, err := os.Stat(out.Recovery.Directory); !os.IsNotExist(err) {
			t.Fatal("preview created recovery directory")
		}
	}
	for _, yes := range []bool{false, true} {
		args := []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=blocked-recovery", "--no-input", "--output=json"}
		if yes {
			args = append(args, "--yes")
		}
		// Collision stops the confirmed path before any native provider entry.
		run := mx4Run(t, mx4Opts{Files: map[string]string{"old.toml": legacyMatrixFixture, "blocked-recovery": "not a directory"}}, args...)
		frame := mx4Frame(t, run.Stdout)
		if frame.OK || len(frame.Errors) != 1 {
			t.Fatal("invalid recovery did not refuse")
		}
		want := "flag_required"
		if yes {
			want = "config_migration_failed"
		}
		if frame.Errors[0].Code != want {
			t.Fatal("yes did not control the pre-I/O confirmation gate")
		}
	}
	for flag, evidence := range map[string]string{"source": "explicit legacy file supplies the selected profile metadata", "source-profile": "two names select different source namespaces without changing runtime profile selection", "recovery-dir": "explicit recovery path is retained in preview without directory creation", "legacy-keyring-service": "explicit historical service is retained in planned credential scope", "dry-run": "preview reads metadata and publishes no configuration, recovery or credentials", "yes": "headless refusal changes to the real recovery validation boundary before any provider entry"} {
		Prove(t, "jira config migrate", flag, evidence)
	}
}

func TestConfigRollbackFlagsSelectJournalAndGateBeforeReads(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"config", "rollback", "--journal=first.json", "--dry-run"}, {"config", "rollback", "--journal=first.json", "--yes", "--no-input"}, {"config", "rollback", "--journal=first.json", "--no-input"}} {
		run := mx4Run(t, mx4Opts{Files: map[string]string{"first.json": "{}"}}, append(args, "--output=json")...)
		frame := mx4Frame(t, run.Stdout)
		if frame.OK || len(frame.Errors) != 1 {
			t.Fatal("invalid rollback journal was accepted")
		}
		want := "config_recovery_invalid"
		if len(args) == 4 && args[3] == "--no-input" {
			want = "flag_required"
		}
		if frame.Errors[0].Code != want {
			t.Fatal("rollback flags did not select read/refusal path")
		}
	}
	Prove(t, "jira config rollback", "journal", "explicit invalid journal is read and classified; no default recovery is guessed")
	Prove(t, "jira config rollback", "yes", "unconfirmed headless run refuses before journal reads; confirmation reaches the selected record")
	Prove(t, "jira config rollback", "dry-run", "preview reaches local journal validation without confirmation or providers")
}
