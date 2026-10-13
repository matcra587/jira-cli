//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestWhoamiSaveAndDryRunHaveCompetingObservableEffects(t *testing.T) {
	t.Parallel()
	for _, selected := range []bool{false, true} {
		for _, preview := range []bool{false, true} {
			stub := newMx4Stub(t)
			args := []string{"auth", "whoami", "--config=settings.toml", "--output=json"}
			if selected {
				args = append(args, "--save")
			}
			if preview {
				args = append(args, "--dry-run")
			}
			const original = "theme='dark'\n"
			run := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"settings.toml": original}}, args...)
			frame := mx4OK(t, run)
			var out envelope.AuthWhoamiSaveOutput
			mx4Data(t, frame, &out)
			requests := stub.requests()
			contents, err := os.ReadFile(filepath.Join(run.Workdir, "settings.toml"))
			if err != nil || len(requests) != 1 || requests[0].Method != "GET" || requests[0].Path != "/rest/api/3/myself" || out.User.AccountID.Value != "5b10a2844c20165700ede21g" {
				t.Fatalf("identity verification did not run: %v requests=%+v", err, requests)
			}
			if selected {
				if frame.Meta.Command != "auth.whoami.save" || out.DryRun != preview || out.Settings.Saved == preview || out.Settings.Changed == preview || out.Scope.Email != mx4Email {
					t.Fatal("save or preview flag ignored")
				}
			} else if frame.Meta.Command != "auth.whoami" || out.Settings.Saved || out.Settings.Changed {
				t.Fatal("ordinary identity read acquired mutation shape")
			}
			if selected && !preview {
				if string(contents) == original {
					t.Fatal("selected live save did not publish")
				}
				file, err := cfg.ReadFile(t.Context(), filepath.Join(run.Workdir, "settings.toml"))
				if err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"account_id": "5b10a2844c20165700ede21g", "account_id_site": stub.URL(), "account_id_email": mx4Email, "account_id_cloud_id": ""} {
					setting, err := file.Setting(key)
					if err != nil || !setting.Present || setting.Secret || setting.Value != want {
						t.Fatalf("matrix save lacks actual bound %s: %+v %v", key, setting, err)
					}
				}
			} else if string(contents) != original {
				t.Fatal("ordinary read or preview published identity")
			}
		}
	}
	Prove(t, "jira auth whoami", "save", "ordinary read preserves authored bytes; selecting save publishes the independently verified bound ID as a typed local mutation")
	Prove(t, "jira auth whoami", "dry-run", "selected preview verifies Jira without changing the selected file; competing selected live execution publishes the verified ID")
}
