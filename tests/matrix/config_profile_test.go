//go:build flagmatrix

package matrix

import (
	"testing"

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
