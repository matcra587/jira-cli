//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigThemeNamePathAndPreviewControlActualStoredSelection(t *testing.T) {
	t.Parallel()
	const original = "theme='monokai'\ntheme_path=''\n"
	const palette = "base='light'\n[colors]\nhelp_command='#123456'\n"
	for _, preview := range []bool{true, false} {
		args := []string{"config", "theme", "--name=dracula", "--path=palette.toml", "--config=settings.toml", "--output=json", "--no-input"}
		if preview {
			args = append(args, "--dry-run")
		}
		run := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": original, "palette.toml": palette}}, args...)
		var out envelope.ConfigThemeOutput
		mx4Data(t, mx4OK(t, run), &out)
		if out.Name != "dracula" || out.ThemePath != filepath.Join(run.Workdir, "palette.toml") || !out.NamePresent || !out.PathPresent || out.DryRun != preview || !out.WouldChange || out.Changed == preview || out.Created {
			t.Fatal("theme flags did not control candidate and publication receipts")
		}
		contents, err := os.ReadFile(filepath.Join(run.Workdir, "settings.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if preview && string(contents) != original || !preview && (!strings.Contains(string(contents), "dracula") || !strings.Contains(string(contents), "palette.toml")) {
			t.Fatalf("theme flags did not govern the actual stored pair: %q", contents)
		}
	}
	Prove(t, "jira config theme", "name", "a selected preset appears in the candidate and actual stored pair instead of the previous monokai")
	Prove(t, "jira config theme", "path", "the validated relative custom palette is stored and reported as an absolute path")
	Prove(t, "jira config theme", "dry-run", "preview preserves exact original bytes; a competing live invocation publishes both declarations")
}
