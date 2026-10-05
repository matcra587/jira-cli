//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigInitExplicitValuesAndPreviewControlStoredFile(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{true, false} {
		args := []string{"config", "init", "--base-url=https://explicit.example.invalid", "--email=explicit@example.invalid", "--config=new-parent/settings.toml", "--output=json", "--no-input"}
		if preview {
			args = append(args, "--dry-run")
		}
		run := mx4Run(t, mx4Opts{}, args...)
		var out envelope.ConfigInitOutput
		mx4Data(t, mx4OK(t, run), &out)
		path := filepath.Join(run.Workdir, "new-parent", "settings.toml")
		if out.Path != path || out.BaseURL != "https://explicit.example.invalid" || out.Email != "explicit@example.invalid" || out.DryRun != preview || !out.WouldCreate || out.Created == preview {
			t.Fatal("initialization flags did not control explicit values and publication")
		}
		if preview {
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("initialization preview created its parent")
			}
			continue
		}
		file, err := config.OpenFile(t.Context(), path)
		if err != nil || !file.Exists() {
			t.Fatalf("initialization did not publish: %v", err)
		}
		for key, want := range map[string]string{"base_url": "https://explicit.example.invalid", "email": "explicit@example.invalid"} {
			value, err := file.Setting(key)
			if err != nil || !value.Present || value.Value != want {
				t.Fatalf("explicit %s was not stored: %+v %v", key, value, err)
			}
		}
	}
	Prove(t, "jira config init", "base-url", "the explicit site is retained in the candidate receipt and stored declaration")
	Prove(t, "jira config init", "email", "the explicit email is retained in the candidate receipt and stored declaration")
	Prove(t, "jira config init", "dry-run", "preview creates no files or directories; the corresponding real invocation creates the complete explicit pair")
}
