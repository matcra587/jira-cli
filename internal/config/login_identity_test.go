package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestLoginIdentityPublishesCompletePublicScopeAndProviderWithoutSecrets(t *testing.T) {
	t.Parallel()
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendEnvironment, core.CredentialBackendOnePassword} {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "absent", "settings.toml")
			variables := []string{"JIRA_SECRET_BACKEND=" + string(backend)}
			if backend == core.CredentialBackendEnvironment {
				variables = append(variables, "JIRA_API_TOKEN_ENV=JIRA_TOKEN_FIXTURE", "JIRA_TOKEN_FIXTURE=synthetic-environment-token")
			}
			if backend == core.CredentialBackendOnePassword {
				variables = append(variables, "JIRA_ONEPASSWORD_ACCOUNT=desktop.example.invalid", "JIRA_ONEPASSWORD_VAULT=Owned Vault", "JIRA_ONEPASSWORD_ITEM=Owned Item")
			}
			opts := Options{Path: path, PermitMissingTarget: true, Environ: func() []string { return variables }}
			resolved, err := Resolve(t.Context(), opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			binding := core.LoginIdentityBinding{AccountIdentityScope: core.AccountIdentityScope{BaseURL: "https://example.invalid", Email: "credential@example.invalid", CloudID: "verified-cloud"}, CredentialLocation: CredentialSelection(*resolved)}
			target, err := OpenLoginIdentityTarget(t.Context(), opts, binding)
			if err != nil {
				t.Fatal(err)
			}
			update, err := target.Prepare("native-id")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preparation created directories")
			}
			if receipt, err := update.Save(t.Context()); err != nil || !receipt.Saved || !receipt.Created {
				t.Fatal("complete login metadata did not publish", err)
			}
			fresh, err := Resolve(t.Context(), Options{Path: path, Environ: func() []string { return nil }}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Config.BaseURL != binding.BaseURL || fresh.Config.Email != binding.Email || fresh.Config.CloudID != binding.CloudID || fresh.Config.CachedAccountID() != "native-id" || fresh.Config.SecretBackend != backend || fresh.Config.APIToken != "" {
				t.Fatal("fresh process lost public scope or persisted a token")
			}
			if backend == core.CredentialBackendEnvironment && fresh.Config.APITokenEnv != "JIRA_TOKEN_FIXTURE" {
				t.Fatal("named environment reference not retained")
			}
			if backend == core.CredentialBackendOnePassword && (fresh.Config.OnePasswordAccount != "desktop.example.invalid" || fresh.Config.OnePasswordVault != "Owned Vault" || fresh.Config.OnePasswordItem != "Owned Item") {
				t.Fatal("SDK coordinates not retained")
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			target, err = OpenLoginIdentityTarget(t.Context(), opts, binding)
			if err != nil {
				t.Fatal(err)
			}
			update, err = target.Prepare("native-id")
			if err != nil || update.Receipt().WouldChange {
				t.Fatal("unchanged candidate became a write", err)
			}
			if receipt, err := update.Save(t.Context()); err != nil || !receipt.Saved || receipt.Changed || receipt.Created {
				t.Fatal("unchanged receipt changed effects", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("unchanged login normalized bytes")
			}
		})
	}
}

func TestLoginIdentityRefusesAuthoredAndChangedPublicProviderReferences(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"secret_backend", "onepassword_account", "onepassword_vault", "onepassword_item", "api_token_env", "base_url", "email", "cloud_id"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			backend := core.CredentialBackendOnePassword
			if key == "api_token_env" {
				backend = core.CredentialBackendEnvironment
			}
			values := map[string]string{"secret_backend": "env", "onepassword_account": "old.example.invalid", "onepassword_vault": "Old Vault", "onepassword_item": "Old Item", "api_token_env": "JIRA_TOKEN_OLD", "base_url": "https://old.invalid", "email": "old@example.invalid", "cloud_id": "old-cloud"}
			original := key + "='" + values[key] + "'\n"
			path := aliasFixture(t, original)
			opts := Options{Path: path, Environ: func() []string {
				return []string{"JIRA_SECRET_BACKEND=" + string(backend), "JIRA_ONEPASSWORD_ACCOUNT=new.example.invalid", "JIRA_ONEPASSWORD_VAULT=New Vault", "JIRA_ONEPASSWORD_ITEM=New Item", "JIRA_API_TOKEN_ENV=JIRA_TOKEN_NEW", "JIRA_TOKEN_NEW=synthetic-token", "JIRA_BASE_URL=https://example.invalid", "JIRA_EMAIL=credential@example.invalid", "JIRA_CLOUD_ID=verified-cloud"}
			}}
			resolved, err := Resolve(t.Context(), opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			binding := core.LoginIdentityBinding{AccountIdentityScope: core.AccountIdentityScope{BaseURL: "https://example.invalid", Email: "credential@example.invalid", CloudID: "verified-cloud"}, CredentialLocation: CredentialSelection(*resolved)}
			if target, err := OpenLoginIdentityTarget(t.Context(), opts, binding); target != nil || !errors.Is(err, ErrAccountIdentityScope) {
				t.Fatal("authored scope/reference overlay accepted", err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != original {
				t.Fatal("reference refusal changed authored bytes")
			}
		})
	}
}

func TestLoginIdentityRefusesChangedSelectionAndCancellation(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"backend", "sdk account", "vault", "item", "environment reference"} {
		t.Run(changed, func(t *testing.T) {
			t.Parallel()
			backend := core.CredentialBackendOnePassword
			if changed == "environment reference" {
				backend = core.CredentialBackendEnvironment
			}
			variables := []string{"JIRA_SECRET_BACKEND=" + string(backend), "JIRA_ONEPASSWORD_ACCOUNT=desktop.example.invalid", "JIRA_ONEPASSWORD_VAULT=Owned Vault", "JIRA_ONEPASSWORD_ITEM=Owned Item", "JIRA_API_TOKEN_ENV=JIRA_TOKEN_OLD", "JIRA_TOKEN_OLD=old-synthetic-token", "JIRA_TOKEN_NEW=new-synthetic-token"}
			opts := Options{Path: filepath.Join(t.TempDir(), "absent.toml"), PermitMissingTarget: true, Environ: func() []string { return variables }}
			resolved, err := Resolve(t.Context(), opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			binding := core.LoginIdentityBinding{AccountIdentityScope: core.AccountIdentityScope{BaseURL: "https://example.invalid", Email: "credential@example.invalid"}, CredentialLocation: CredentialSelection(*resolved)}
			switch changed {
			case "backend":
				variables[0] = "JIRA_SECRET_BACKEND=env"
			case "sdk account":
				variables[1] = "JIRA_ONEPASSWORD_ACCOUNT=changed.example.invalid"
			case "vault":
				variables[2] = "JIRA_ONEPASSWORD_VAULT=Changed Vault"
			case "item":
				variables[3] = "JIRA_ONEPASSWORD_ITEM=Changed Item"
			case "environment reference":
				variables[4] = "JIRA_API_TOKEN_ENV=JIRA_TOKEN_NEW"
			}
			if target, err := OpenLoginIdentityTarget(t.Context(), opts, binding); target != nil || !errors.Is(err, ErrAccountIdentityScope) {
				t.Fatal("changed provider/reference selection accepted", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if target, err := OpenLoginIdentityTarget(ctx, opts, binding); target != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled login target continued", err)
			}
		})
	}
}

func TestLoginEnvironmentReferenceUsesActualCapturedPrecedence(t *testing.T) {
	t.Parallel()
	for _, generic := range []bool{false, true} {
		t.Run(map[bool]string{false: "named", true: "generic wins"}[generic], func(t *testing.T) {
			t.Parallel()
			calls := 0
			variables := []string{"JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN_ENV=JIRA_TOKEN_FIXTURE", "JIRA_TOKEN_FIXTURE=named-synthetic-token"}
			want := "JIRA_TOKEN_FIXTURE"
			if generic {
				variables = append(variables, "JIRA_API_TOKEN=generic-synthetic-token")
				want = "JIRA_API_TOKEN"
			}
			resolved, err := Resolve(t.Context(), Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string { calls++; return variables }}, nil)
			if err != nil || resolved.TokenEnvironment != want || CredentialSelection(*resolved).EnvironmentKey != want || calls != 1 {
				t.Fatal("token reference did not share actual captured selection", err)
			}
		})
	}
}
