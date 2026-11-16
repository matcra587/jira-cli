package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/auth"
)

func TestListProfilesReadsAuthoredMetadataWithoutOpeningProviders(t *testing.T) {
	path := fixture(t, profiles)
	provider := &store{get: func(context.Context, string) (string, error) {
		t.Fatal("metadata discovery resolved a credential")
		return "", nil
	}}
	t.Setenv("JIRA_PROFILE", "missing")
	t.Setenv("JIRA_BASE_URL", "https://unselected.example.invalid")
	listed, err := auth.ListProfiles(t.Context(), auth.Options{ConfigPath: path, CredentialStore: provider})
	if err != nil {
		t.Fatal(err)
	}
	if listed.DefaultProfile != "work" || len(listed.Profiles) != 2 {
		t.Fatalf("discovery=%+v err=%v", listed, err)
	}
	work := listed.Profiles[1]
	savedDefault := work.Name == "work" && work.Default
	if !savedDefault || work.BaseURL != "https://work.example.invalid" {
		t.Fatal("authored profile metadata changed", work)
	}
	if len(provider.reads) != 0 {
		t.Fatal("discovery opened a credential provider")
	}
	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{firstID, "credential", "unselected"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("profile discovery exposed provider coordinates or environment overrides")
		}
	}
}

func TestListProfilesDistinguishesFlatEmptyAndLegacyFiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
		count    int
		migrate  bool
	}{
		{name: "flat", contents: "base_url = 'https://flat.example.invalid'\nemail = 'reader@example.invalid'\n", count: 1},
		{name: "empty named", contents: "format_version = 1\n[profiles]\n"},
		{
			name: "legacy", migrate: true,
			contents: "[[profiles]]\nname='default'\nsecret_backend='env'\n[tui]\npreview='left'\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fixture(t, tc.contents)
			listed, err := auth.ListProfiles(t.Context(), auth.Options{ConfigPath: path})
			if tc.migrate {
				if !errors.Is(err, auth.ErrMigrationRequired) {
					t.Fatal("legacy migration classification lost", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Profiles) != tc.count || listed.Profiles == nil {
				t.Fatalf("discovery=%+v err=%v", listed, err)
			}
			if tc.name == "flat" {
				profile := listed.Profiles[0]
				flatDefault := profile.Name == "" && profile.Default
				if !flatDefault || profile.BaseURL != "https://flat.example.invalid" {
					t.Fatal("flat selection metadata changed", listed)
				}
			}
		})
	}
}
