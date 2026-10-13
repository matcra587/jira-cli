package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLoginReservesProfileLocatorBeforeTokenAndReopensSettingsTarget(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{false, true} {
		name := "live"
		if preview {
			name = "preview"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.toml")
			original := "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://example.invalid'\nemail='person@example.invalid'\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			opts := config.Options{Path: path, Profile: "work", Environ: func() []string { return nil }}
			captured, err := config.CaptureOptions(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			store := newFakeStore()
			var slot string
			deps := Deps{
				LoginTarget: func(ctx context.Context, binding core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
					return config.OpenLoginIdentityTarget(ctx, opts, binding)
				},
				PrepareLoginLocation: func(ctx context.Context, selected core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error) {
					if preview {
						t.Fatal("preview reserved a locator")
					}
					return config.PrepareLoginCredentialLocation(ctx, captured, selected)
				},
				CredentialsForLogin: func(ctx context.Context, selected core.CredentialLocation) (core.CredentialStore, error) {
					fresh, err := config.Resolve(ctx, opts, nil)
					if err != nil || selected.Account == "" || config.CredentialSelection(*fresh) != selected {
						t.Fatalf("provider entered before durable locator: %v", err)
					}
					slot = selected.Account
					return credentials.Select(store, slot), nil
				},
			}
			ident := identity{site: "https://example.invalid", email: "person@example.invalid", location: core.CredentialLocation{Backend: core.CredentialBackendKeyring}}
			user := core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("verified-id")}}
			out, err := publishLogin(t.Context(), deps, ident, "synthetic-profile-token", preview, "", user)
			if err != nil {
				t.Fatal(err)
			}
			if preview {
				actual, err := os.ReadFile(path)
				if err != nil || string(actual) != original || store.setCall != 0 || out.LocatorPublication != nil {
					t.Fatal("preview changed public settings or credentials")
				}
				return
			}
			if out.LocatorPublication == nil || !out.LocatorPublication.Saved || !out.Settings.Saved || !out.Credential.Saved || store.setCall != 1 {
				t.Fatalf("publication outcomes: %+v", out)
			}
			if _, exists := store.secrets["default"]; exists || store.secrets[slot] == "" {
				t.Fatal("named login wrote the legacy slot or lost its token")
			}
			fresh, err := config.Resolve(t.Context(), opts, store)
			if err != nil || fresh.Config.CachedAccountID() != "verified-id" || fresh.Config.APIToken != "synthetic-profile-token" {
				t.Fatalf("fresh process could not use completed login: %v", err)
			}
		})
	}
}
