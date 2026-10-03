//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAliasMutationFlagsHaveObservableFileEffects(t *testing.T) {
	t.Parallel()
	const original = "[aliases]\nmine='version'\n"
	for _, tc := range []struct{ leaf, flag string }{
		{"set", "dry-run"},
		{"delete", "dry-run"},
		{"delete", "force"},
		{"delete", "yes"},
		{"import", "dry-run"},
		{"import", "clobber"},
	} {
		t.Run(tc.leaf+"/"+tc.flag, func(t *testing.T) {
			t.Parallel()
			args := []string{"alias", tc.leaf, "--config=aliases.toml", "--output=json"}
			input := ""
			switch tc.leaf {
			case "set":
				args = append(args, "mine", "issue list")
			case "delete":
				args = append(args, "mine")
			case "import":
				input = "mine: 'issue list'\nnewName: version\n"
			}
			args = append(args, "--"+tc.flag)
			run := mx4Run(t, mx4Opts{Files: map[string]string{"aliases.toml": original}, Stdin: input}, args...)
			f := mx4OK(t, run)
			stored, err := os.ReadFile(filepath.Join(run.Workdir, "aliases.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.flag == "dry-run" {
				if string(stored) != original {
					t.Fatal("local alias preview changed file bytes")
				}
				var state envelope.MutationState
				mx4Data(t, f, &state)
				if !state.DryRun {
					t.Fatal("local preview omitted its explicit mutation state")
				}
			} else if tc.leaf == "delete" {
				var out envelope.AliasDeleteOutput
				mx4Data(t, f, &out)
				if !out.Deleted || !out.Changed || strings.Contains(string(stored), "mine") {
					t.Fatal("consent flag did not permit the explicit local removal")
				}
				refused := mx4Run(t, mx4Opts{Files: map[string]string{"aliases.toml": original}}, "alias", "delete", "mine", "--config=aliases.toml", "--output=json")
				mx4ErrCode(t, refused, "flag_required")
			} else {
				var out envelope.AliasImportOutput
				mx4Data(t, f, &out)
				if out.Imported != 2 || !out.Changed || len(out.Skipped) != 0 || !strings.Contains(string(stored), "issue list") {
					t.Fatal("clobber did not replace the collision")
				}
				kept := mx4Run(t, mx4Opts{Files: map[string]string{"aliases.toml": original}, Stdin: input}, "alias", "import", "--config=aliases.toml", "--output=json")
				var without envelope.AliasImportOutput
				mx4Data(t, mx4OK(t, kept), &without)
				if without.Imported != 1 || len(without.Skipped) != 1 {
					t.Fatal("without clobber, an existing alias was not kept")
				}
			}
			Prove(t, "jira alias "+tc.leaf, tc.flag, "actual file bytes and typed receipts prove preview, explicit removal consent or collision replacement; competing live/refusal behavior also checked")
		})
	}
}
