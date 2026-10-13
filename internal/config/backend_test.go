package config_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type backendRecordStore struct {
	core.CredentialStore
	raw   string
	reads int
}

func (*backendRecordStore) CredentialBackend() core.CredentialBackend {
	return core.CredentialBackendOnePassword
}

func (s *backendRecordStore) Get(context.Context, string) (string, error) {
	s.reads++
	return s.raw, nil
}

func TestOnePasswordCredentialLayerRetainsBindingPrecedenceAndProvenance(t *testing.T) {
	for _, mode := range []string{"stored", "environment token", "different site", "different account", "explicit environment credentials", "normalized account"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "stored@example.invalid", Token: "synthetic-stored-token"})
			if err != nil {
				t.Fatal(err)
			}
			store := &backendRecordStore{raw: string(raw)}
			env := []string{}
			if mode == "environment token" {
				env = []string{"JIRA_API_TOKEN=synthetic-env-token"}
			}
			if mode == "different site" {
				env = []string{"JIRA_BASE_URL=https://other.invalid"}
			}
			if mode == "different account" {
				env = []string{"JIRA_EMAIL=other@example.invalid"}
			}
			if mode == "explicit environment credentials" {
				env = []string{"JIRA_EMAIL=other@example.invalid", "JIRA_API_TOKEN=synthetic-env-token"}
			}
			if mode == "normalized account" {
				env = []string{"JIRA_EMAIL= STORED@EXAMPLE.INVALID "}
			}
			resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "missing.toml"), Environ: func() []string { return env }}, store)
			if err != nil {
				t.Fatal(err)
			}
			if store.reads != 1 {
				t.Fatal("credential record was resolved more than once")
			}
			switch mode {
			case "stored", "normalized account":
				if resolved.Source("api_token") != config.LayerOnePassword || resolved.Config.APIToken != "synthetic-stored-token" {
					t.Fatal("SDK credential layer missing or mislabeled")
				}
			case "environment token":
				if resolved.Source("api_token") != config.LayerEnv || resolved.Config.APIToken != "synthetic-env-token" || resolved.Config.Email != "stored@example.invalid" {
					t.Fatal("environment override or stored public identity lost")
				}
			case "different account":
				if resolved.Config.APIToken != "" || resolved.Config.BaseURL != "" || resolved.Config.Email != "other@example.invalid" {
					t.Fatal("stored SDK token crossed account boundary")
				}
			case "explicit environment credentials":
				if resolved.Source("api_token") != config.LayerEnv || resolved.Config.APIToken != "synthetic-env-token" || resolved.Config.Email != "other@example.invalid" {
					t.Fatal("explicit environment credential tuple was discarded")
				}
			case "different site":
				if resolved.Config.APIToken != "" || resolved.Config.Email != "" || resolved.Source("base_url") != config.LayerEnv {
					t.Fatal("SDK tuple crossed site boundary")
				}
			}
		})
	}
}

func TestOnePasswordSettingsLoadWithoutSDKAndValidateBackendVocabulary(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "supported"
		backend := "1password"
		if !valid {
			name = "unknown"
			backend = "shell-command"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			data := "secret_backend='" + backend + "'\nonepassword_account='Fixture Account'\nonepassword_vault='Fixture Vault'\nonepassword_item='Fixture Item'\n"
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
			if !valid {
				if err == nil {
					t.Fatal("unsupported backend was decoded")
				}
				return
			}
			if err != nil || resolved.Config.SecretBackend != core.CredentialBackendOnePassword || resolved.Config.OnePasswordVault != "Fixture Vault" || resolved.Source("secret_backend") != config.LayerFile {
				t.Fatalf("local SDK settings did not decode: %v", err)
			}
		})
	}
}

func TestKeyringCredentialTokenNeverPairsWithDifferentAccount(t *testing.T) {
	raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "stored@example.invalid", Token: "synthetic-stored-token"})
	if err != nil {
		t.Fatal(err)
	}
	// Hide optional metadata to exercise the historical keyring contract.
	store := struct{ core.CredentialStore }{&backendRecordStore{raw: string(raw)}}
	resolved, err := config.Resolve(t.Context(), config.Options{
		Path:    filepath.Join(t.TempDir(), "missing.toml"),
		Environ: func() []string { return []string{"JIRA_EMAIL=other@example.invalid"} },
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.APIToken != "" || resolved.Config.BaseURL != "" || resolved.Config.Email != "other@example.invalid" || len(resolved.Warnings) == 0 {
		t.Fatal("stored keyring token crossed account boundary or exclusion was silent")
	}
}
