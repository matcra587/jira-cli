package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type contextCredentialStore struct {
	matches func(context.Context) bool
	raw     string
	reads   int
}

func (s *contextCredentialStore) Get(ctx context.Context, _ string) (string, error) {
	s.reads++
	if !s.matches(ctx) {
		return "", errors.New("configuration lost caller context")
	}
	return s.raw, nil
}

func (*contextCredentialStore) Set(context.Context, string, string) error {
	panic("unexpected credential write")
}

func (*contextCredentialStore) Delete(context.Context, string) error {
	panic("unexpected credential removal")
}

func TestCredentialResolutionPreservesContextAndReadsOnce(t *testing.T) {
	for _, differentSite := range []bool{false, true} {
		name := "same site"
		if differentSite {
			name = "different site"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "public@example.invalid", Token: "fixture-token"})
			if err != nil {
				t.Fatal(err)
			}
			store := &contextCredentialStore{matches: func(candidate context.Context) bool { return candidate == ctx }, raw: string(raw)}
			environment := func() []string { return nil }
			if differentSite {
				environment = func() []string { return []string{"JIRA_BASE_URL=https://other.example.invalid"} }
			}
			resolved, err := config.Resolve(ctx, config.Options{Path: filepath.Join(t.TempDir(), "missing.toml"), Environ: environment}, store)
			if err != nil {
				t.Fatal(err)
			}
			if store.reads != 1 || resolved.CredentialError != nil {
				t.Fatalf("credential resolution did not retain context or single read: reads=%d failure=%v", store.reads, resolved.CredentialError)
			}
			if differentSite {
				if resolved.Config.APIToken != "" || resolved.Config.Email != "" {
					t.Fatal("stored identity crossed site boundary")
				}
			} else if resolved.Config.APIToken != "fixture-token" || resolved.Source("api_token") != config.LayerKeyring {
				t.Fatal("credential layer was lost")
			}
		})
	}
}
