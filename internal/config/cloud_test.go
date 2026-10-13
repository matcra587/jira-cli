package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestCloudScopeDeclarationCannotReuseAnUnrelatedStoredToken(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stored, declared, token, scope string
		exists                               bool
		env                                  []string
		reads                                int
	}{
		{name: "inherit stored", stored: "cloud-a", scope: "cloud-a", token: "synthetic-stored", reads: 1},
		{name: "same authored", stored: "cloud-a", declared: "cloud-a", exists: true, scope: "cloud-a", token: "synthetic-stored", reads: 1},
		{name: "different authored", stored: "cloud-a", declared: "cloud-b", exists: true, scope: "cloud-b", reads: 1},
		{name: "explicit classic", stored: "cloud-a", exists: true, reads: 1},
		{name: "classic to scoped", declared: "cloud-b", exists: true, scope: "cloud-b", reads: 1},
		{name: "environment scope", stored: "cloud-a", scope: "cloud-b", env: []string{"JIRA_CLOUD_ID=cloud-b"}, reads: 1},
		{name: "new generic token and authored scope", stored: "cloud-a", declared: "cloud-b", exists: true, scope: "cloud-b", env: []string{"JIRA_API_TOKEN=synthetic-new"}, token: "synthetic-new", reads: 1},
		{name: "new generic token and explicit classic", stored: "cloud-a", exists: true, env: []string{"JIRA_API_TOKEN=synthetic-new"}, token: "synthetic-new", reads: 1},
		{name: "new generic token and env scope", stored: "cloud-a", scope: "cloud-b", env: []string{"JIRA_API_TOKEN=synthetic-new", "JIRA_CLOUD_ID=cloud-b"}, token: "synthetic-new", reads: 1},
		{name: "new named token", stored: "cloud-a", declared: "cloud-b", exists: true, scope: "cloud-b", env: []string{"JIRA_TOKEN_WORK=synthetic-new"}, token: "synthetic-new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := "base_url='https://example.invalid'\nemail='person@example.invalid'\napi_token_env='JIRA_TOKEN_WORK'\n"
			if tc.exists {
				body += "cloud_id='" + tc.declared + "'\n"
			}
			path := writeConfig(t, body)
			raw, err := json.Marshal(credentials.Record{Version: 2, Site: "https://example.invalid", Email: "stored@example.invalid", Token: "synthetic-stored", CloudID: tc.stored})
			if err != nil {
				t.Fatal(err)
			}
			store := &tokenReferenceStore{raw: string(raw)}
			resolved, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return tc.env }}, store)
			if err != nil || resolved.Config.CloudID != tc.scope || resolved.Config.APIToken != tc.token || store.reads != tc.reads {
				t.Fatal("scope declaration reused a token or lost public route", err)
			}
			if tc.token == "" && (resolved.Config.Email != "person@example.invalid" || len(resolved.Warnings) != 1) {
				t.Fatal("scope mismatch retained unrelated stored account or hid refusal")
			}
			if tc.name == "new named token" && resolved.Config.Email != "person@example.invalid" {
				t.Fatal("named token inherited stored public identity")
			}
		})
	}
}

func TestCloudScopeValidationPrecedesProvidersAndAllowsFileOnlyRepair(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{".", "..", "cloud/other", "cloud?other", "cloud#other", " "} {
		t.Run("invalid cloud", func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "cloud_id='"+scope+"'\n")
			store := &tokenReferenceStore{}
			_, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{"JIRA_CLOUD_ID=valid-cloud"} }}, store)
			if err == nil || store.reads != 0 {
				t.Fatal("environment masked invalid routing or provider opened")
			}
			file, err := config.ReadFile(t.Context(), path)
			if err != nil {
				t.Fatal("public scope could not be inspected for repair", err)
			}
			update, err := file.PrepareSetting("cloud_id", "valid-cloud")
			if err != nil {
				t.Fatal("scope could not be repaired", err)
			}
			if changed, err := update.Save(t.Context()); err != nil || !changed {
				t.Fatal("repaired scope not published", err)
			}
		})
	}
	path := writeConfig(t, "email='person@example.invalid'\n")
	store := &tokenReferenceStore{}
	if _, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{"JIRA_CLOUD_ID= "} }}, store); err == nil || store.reads != 0 {
		t.Fatal("whitespace environment scope bypassed validation")
	}
}

func TestLegacyCloudMappingRetainsClassicDefaultAndSelectedScope(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"", " fixture-cloud "} {
		body := "default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\ncloud_id='" + scope + "'\n[[profiles]]\nname='other'\ncloud_id='.'\n"
		prepared, err := config.PrepareLegacyMigration(t.Context(), []byte(body), "", "work")
		if err != nil || prepared.Selection().CloudID != strings.TrimSpace(scope) || len(prepared.Unmapped()) != 0 {
			t.Fatal("selected cloud mapping rejected unselected historical state", err)
		}
		resolved := legacyCandidate(t, prepared)
		if resolved.Config.CloudID != strings.TrimSpace(scope) || resolved.Source("cloud_id") != config.LayerFile {
			t.Fatal("candidate lost selected cloud routing")
		}
	}
}

func TestScopedLegacySitePrefixRemainsReadyAndUsableAtTheFixedGateway(t *testing.T) {
	t.Parallel()
	prepared, err := config.PrepareLegacyMigration(t.Context(), []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid/proxy'\ncloud_id='fixture-cloud'\nemail='person@example.invalid'\n"), "", "")
	if err != nil || len(prepared.Unmapped()) != 0 {
		t.Fatal("historical scoped site prefix lost readiness", err)
	}
	resolved := legacyCandidate(t, prepared)
	client, err := adapterjira.New(adapterjira.Config{BaseURL: resolved.Config.BaseURL, CloudID: resolved.Config.CloudID, Email: resolved.Config.Email, APIToken: "synthetic-prefix-token"})
	if err != nil || client.BaseURL() != "https://example.invalid/proxy" {
		t.Fatal("ready mapping cannot construct its scoped runtime", err)
	}
}
