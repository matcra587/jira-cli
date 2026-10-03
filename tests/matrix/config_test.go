//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigSetDryRunHasObservableFileAndReceiptEffects(t *testing.T) {
	t.Parallel()
	const original = "# retain during preview\nread_only=true\n"
	for _, preview := range []bool{true, false} {
		args := []string{"config", "set", "read_only", "false", "--config=settings.toml", "--output=json", "--no-input"}
		if preview {
			args = append(args, "--dry-run")
		}
		run := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": original}}, args...)
		var out envelope.ConfigSetOutput
		mx4Data(t, mx4OK(t, run), &out)
		contents, err := os.ReadFile(filepath.Join(run.Workdir, "settings.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if out.DryRun != preview || !out.WouldChange || out.Changed == preview || out.Created || !out.PreviousPresent || out.PreviousValue != "true" || out.Value != "false" {
			t.Fatal("preview flag did not control truthful setting publication")
		}
		if preview && string(contents) != original || !preview && string(contents) != "read_only = false\n" {
			t.Fatalf("preview flag did not govern the actual file bytes: %q", contents)
		}
	}
	Prove(t, "jira config set", "dry-run", "preview retains exact authored bytes and reports intent without publication; the competing live run stores false and reports the change")
}
