//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const profileMatrixFixture = "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://work.invalid'\nemail='work@example.invalid'\nread_only=true\n[profiles.personal]\nbase_url='https://personal.invalid'\nemail='personal@example.invalid'\nread_only=false\n"

func TestProfileSelectorChangesAuthoredSettingWithoutProviders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ selector, name, want string }{{"--profile", "work", "true"}, {"-P", "personal", "false"}} {
		run := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": profileMatrixFixture}}, "config", "get", "read_only", tc.selector, tc.name, "--config=settings.toml", "--output=json")
		var out envelope.ConfigGetOutput
		mx4Data(t, mx4OK(t, run), &out)
		if out.Profile != tc.name || out.Value != tc.want || !out.Present {
			t.Fatal("selector did not change authored profile", out)
		}
	}
	Prove(t, "jira", "profile", "long and short selectors choose independently authored true/false settings through the real binary without credentials")
}

func TestProfileMutationFlagsControlActualPublication(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"add", "rename", "default", "remove"} {
		for _, preview := range []bool{true, false} {
			args := []string{"config", "profile", operation}
			switch operation {
			case "add":
				args = append(args, "third", "--base-url=https://third.invalid", "--email=third@example.invalid")
			case "rename":
				args = append(args, "personal", "private")
			case "default":
				args = append(args, "personal")
			case "remove":
				args = append(args, "personal", "--force")
			}
			if preview {
				args = append(args, "--dry-run")
			}
			args = append(args, "--config=settings.toml", "--output=json", "--no-input")
			run := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": profileMatrixFixture}}, args...)
			var out envelope.ConfigProfileChangeOutput
			mx4Data(t, mx4OK(t, run), &out)
			data, err := os.ReadFile(filepath.Join(run.Workdir, "settings.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if out.DryRun != preview || out.Changed == preview || !out.WouldChange {
				t.Fatal("incorrect publication receipt", out)
			}
			if preview {
				if string(data) != profileMatrixFixture {
					t.Fatal("preview changed exact file bytes")
				}
				continue
			}
			file, err := config.ReadFile(t.Context(), filepath.Join(run.Workdir, "settings.toml"))
			if err != nil {
				t.Fatal(err)
			}
			profiles, err := file.Profiles()
			if err != nil {
				t.Fatal(err)
			}
			names := map[string]config.Profile{}
			for _, profile := range profiles {
				names[profile.Name] = profile
			}
			switch operation {
			case "add":
				if len(names) != 3 || names["third"].BaseURL != "https://third.invalid" || names["third"].Email != "third@example.invalid" || names["third"].CredentialID == "" {
					t.Fatal("explicit account metadata or stable locator not published", profiles)
				}
			case "rename":
				if len(names) != 2 || names["private"].Email != "personal@example.invalid" || names["personal"].Name != "" {
					t.Fatal("rename did not preserve account", profiles)
				}
			case "default":
				if !names["personal"].Default || names["work"].Default {
					t.Fatal("default not changed", profiles)
				}
			case "remove":
				if len(names) != 1 || names["work"].Name != "work" {
					t.Fatal("remove changed wrong profile", profiles)
				}
			}
		}
		Prove(t, "jira config profile "+operation, "dry-run", "preview preserves exact authored bytes; live operation changes only the intended profile metadata and reports acknowledged publication")
	}
	refused := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": profileMatrixFixture}}, "config", "profile", "remove", "personal", "--config=settings.toml", "--output=json", "--no-input")
	mx4ErrCode(t, refused, "flag_required")
	data, err := os.ReadFile(filepath.Join(refused.Workdir, "settings.toml"))
	if err != nil || string(data) != profileMatrixFixture {
		t.Fatal("unconfirmed removal changed settings", err)
	}
	confirmed := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": profileMatrixFixture}}, "config", "profile", "remove", "personal", "--yes", "--config=settings.toml", "--output=json", "--no-input")
	var receipt envelope.ConfigProfileChangeOutput
	mx4Data(t, mx4OK(t, confirmed), &receipt)
	if !receipt.Changed || receipt.Name != "personal" {
		t.Fatal("yes alias did not acknowledge removal", receipt)
	}
	Prove(t, "jira config profile remove", "yes", "hidden alias acknowledges the same headless profile removal as force")
	Prove(t, "jira config profile remove", "force", "headless removal without force refuses and preserves bytes; confirmed removal deletes only the selected profile")
	Prove(t, "jira config profile add", "base-url", "explicit site is published in the new profile while both original profiles remain")
	Prove(t, "jira config profile add", "email", "explicit account is published in the new profile while both original profiles remain")
}
