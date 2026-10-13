package root

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestLoginFactoriesRefuseChangedSelectionBeforeProviderConstruction(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"secret_backend", "onepassword_account", "onepassword_vault", "onepassword_item"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			original := "secret_backend='1password'\nonepassword_account='desktop'\nonepassword_vault='vault'\nonepassword_item='item'\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			opts := config.Options{Path: path, PermitMissingTarget: true, Environ: func() []string { return nil }}
			resolved, err := config.Resolve(t.Context(), opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			selected := config.CredentialSelection(*resolved)
			file, err := config.OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			value := "changed"
			if key == "secret_backend" {
				value = "keyring"
			}
			update, err := file.PrepareSetting(key, value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := update.Save(t.Context()); err != nil {
				t.Fatal(err)
			}
			calls := 0
			store, err := resolveLoginCredentials(t.Context(), opts, selected, func(config.Config) core.CredentialStore {
				calls++
				return core.NoCredentialStore{}
			})
			if err == nil || store != nil || calls != 0 || cli.MapError(err).Code != cli.CodeConfigChanged {
				t.Fatal("changed login destination entered provider or lost classification", err)
			}
		})
	}
}

func TestLoginFactoriesValidateTargetAndRemainPublicWhenDefaultsComplete(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"complete", "flags complete", "site flag", "email flag", "environment", "regular ancestor", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "absent", "settings.toml")
			env := []string{"JIRA_SECRET_BACKEND=1password", "JIRA_ONEPASSWORD_ACCOUNT=desktop", "JIRA_ONEPASSWORD_VAULT=vault", "JIRA_ONEPASSWORD_ITEM=item"}
			if kind != "flags complete" {
				if kind != "site flag" {
					env = append(env, "JIRA_BASE_URL=https://example.invalid")
				}
				if kind != "email flag" {
					env = append(env, "JIRA_EMAIL=person@example.invalid")
				}
			}
			if kind == "environment" {
				env = append(env, "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=synthetic-token")
			}
			if kind == "regular ancestor" {
				if err := os.WriteFile(filepath.Dir(path), []byte("regular ancestor"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			opts := config.Options{Path: path, PermitMissingTarget: true, Environ: func() []string { return env }}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			calls := 0
			defaults := authcmd.LoginDefaults{Site: kind != "flags complete" && kind != "site flag", Email: kind != "flags complete" && kind != "email flag"}
			site, err := resolveLoginSite(ctx, opts, defaults, func(config.Config) core.CredentialStore {
				calls++
				return core.NoCredentialStore{}
			})
			if calls != 0 {
				t.Fatal("public login resolution opened an unrelated provider")
			}
			switch kind {
			case "regular ancestor":
				if err == nil {
					t.Fatal("regular ancestor was admitted as an absent publication target")
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("login target lost cancellation", err)
				}
			default:
				if err != nil || kind != "flags complete" && kind != "site flag" && site.BaseURL != "https://example.invalid" {
					t.Fatal("public login defaults failed", err)
				}
			}
		})
	}
}
