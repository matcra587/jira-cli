package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

type failedCredentialStore struct{ err error }

func (s failedCredentialStore) Get(string) (string, error) { return "unusable-partial-record", s.err }
func (failedCredentialStore) Set(string, string) error     { panic("unexpected credential write") }
func (failedCredentialStore) Delete(string) error          { panic("unexpected credential deletion") }

func TestCredentialReadFailureRetainsEnvironmentFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "local preferences"},
		{name: "explicit environment token", token: "synthetic-env-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("fixture private backend detail")
			resolved, err := config.Load(config.Options{
				Path: writeConfig(t, "read_only=true\n"),
				Environ: env("JIRA_BASE_URL=https://fixture.example.invalid",
					"JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN="+tc.token),
				Credentials: failedCredentialStore{err: cause},
			})
			if err != nil {
				t.Fatalf("unavailable store prevented fallback resolution: %v", err)
			}
			if !resolved.Config.ReadOnly || resolved.Config.APIToken != tc.token {
				t.Fatal("fallback changed preferences or accepted the failed read's partial record")
			}
			if tc.token != "" && resolved.Source("api_token") != config.LayerEnv {
				t.Fatal("environment token lost provenance")
			}
			if len(resolved.Warnings) != 1 || !strings.Contains(resolved.Warnings[0], "credential store") {
				t.Fatal("backend failure did not produce one public diagnostic")
			}
			if strings.Contains(resolved.Warnings[0], cause.Error()) || strings.Contains(resolved.Warnings[0], "unusable-partial-record") {
				t.Fatal("public warning exposes backend detail or unusable data")
			}
		})
	}
}

func TestAbsentCredentialStoreRemainsSilent(t *testing.T) {
	resolved, err := config.Load(config.Options{
		Path: writeConfig(t, "read_only=true\n"), Environ: env(), Credentials: core.NoCredentialStore{},
	})
	if err != nil || len(resolved.Warnings) != 0 {
		t.Fatalf("ordinary credential absence became a backend warning: %v", err)
	}
}
