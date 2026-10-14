//go:build flagmatrix

package matrix

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestUpdateAcknowledgementAndPreviewFlags(t *testing.T) {
	t.Parallel()
	executable := filepath.Join("Cellar", "jira", "owned", "bin", "jira")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	for _, tc := range []struct {
		name, flag, code string
		exit             int
		preview          bool
	}{
		{name: "no consent", code: "flag_required", exit: 3},
		{name: "force", flag: "--force", code: "update_failed", exit: 5},
		{name: "yes", flag: "--yes", code: "update_failed", exit: 5},
		{name: "yes false", flag: "--yes=false", code: "flag_required", exit: 3},
		{name: "preview", flag: "--dry-run", code: "update_failed", exit: 5, preview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := []string{"up", "--no-input", "--output=json"}
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			// No host Homebrew can be located. Authorized calls reach its
			// read-only preflight, while the control refuses before it.
			result := mx4Run(t, mx4Opts{Executable: executable, Env: map[string]string{"PATH": ""}}, args...)
			frame := mx4Frame(t, result.Stdout)
			if result.Exit != tc.exit || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code {
				t.Fatalf("flag %s produced exit%d/frame%+v", tc.flag, result.Exit, frame)
			}
			var out envelope.UpdateOutput
			mx4Data(t, frame, &out)
			if out.Channel != "brew" || out.DryRun != tc.preview || out.Attempted || out.Updated || out.UpdateAvailable != nil {
				t.Fatalf("authorization or preview invented an installation: %+v", out)
			}
			original, err := os.ReadFile(mx4Binary(t))
			if err != nil {
				t.Fatal(err)
			}
			copyBytes, err := os.ReadFile(filepath.Join(result.Workdir, executable))
			if err != nil || sha256.Sum256(copyBytes) != sha256.Sum256(original) {
				t.Fatalf("executable changed during refusal: %v", err)
			}
			switch tc.name {
			case "force", "yes":
				Prove(t, "jira update", tc.name, "headless control refuses for missing consent; acknowledgement reaches isolated read-only Homebrew preflight without changing executable bytes")
			case "preview":
				Prove(t, "jira update", "dry-run", "preview bypasses live acknowledgement, retains dry_run=true and reaches isolated read-only preflight with unchanged executable bytes")
			}
		})
	}
}
