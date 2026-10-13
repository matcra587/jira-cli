package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func clientPreferenceFixture(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClientPreferencesDefaultFileAndEnvironmentProvenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, data, interval, team string
		environment                []string
		source                     config.Layer
	}{
		{name: "default", interval: "30", team: "[]", source: config.LayerDefault},
		{name: "file", data: "refresh_interval=45\nteam_account_ids=['a,b',' a ','a,b','']\n", interval: "45", team: `["a,b"," a ","a,b",""]`, source: config.LayerFile},
		{name: "environment", data: "refresh_interval=45\nteam_account_ids=['file']\n", environment: []string{"JIRA_REFRESH_INTERVAL=60", `JIRA_TEAM_ACCOUNT_IDS=["env,1"," env ","env,1",""]`}, interval: "60", team: `["env,1"," env ","env,1",""]`, source: config.LayerEnv},
		{name: "environment clear", data: "refresh_interval=45\nteam_account_ids=['file']\n", environment: []string{"JIRA_REFRESH_INTERVAL=60", "JIRA_TEAM_ACCOUNT_IDS=[]"}, interval: "60", team: "[]", source: config.LayerEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := clientPreferenceFixture(t, tc.data)
			loaded, err := config.Load(config.Options{Path: path, Environ: func() []string { return tc.environment }})
			if err != nil || loaded.Config.Values()["refresh_interval"] != tc.interval || loaded.Config.Values()["team_account_ids"] != tc.team || loaded.Source("refresh_interval") != tc.source || loaded.Source("team_account_ids") != tc.source {
				t.Fatal("metadata value, list ownership or provenance changed", err)
			}
		})
	}
}

func TestClientPreferenceMalformedEnvironmentRefusesBeforeCredentials(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"JIRA_REFRESH_INTERVAL=0", "JIRA_REFRESH_INTERVAL=-1", "JIRA_REFRESH_INTERVAL=1.5", "JIRA_REFRESH_INTERVAL=9223372036854775808",
		"JIRA_TEAM_ACCOUNT_IDS=null", "JIRA_TEAM_ACCOUNT_IDS={}", "JIRA_TEAM_ACCOUNT_IDS=one,two", "JIRA_TEAM_ACCOUNT_IDS=[42]", "JIRA_TEAM_ACCOUNT_IDS=[null]", `JIRA_TEAM_ACCOUNT_IDS=["marker-never-echoed",]`,
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			path := clientPreferenceFixture(t, "")
			store := &tokenReferenceStore{}
			loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{value} }}, store)
			if err == nil || loaded != nil || store.reads != 0 || strings.Contains(err.Error(), "marker-never-echoed") {
				t.Fatal("malformed public preferences entered a provider or exposed input", err)
			}
		})
	}
}

func TestClientPreferenceBadFileCannotBeMaskedByEnvironment(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"refresh_interval=1.5\n", "refresh_interval=0\n", "refresh_interval=-1\n", "refresh_interval='30'\n",
		"team_account_ids='one'\n", "team_account_ids=[42]\n", "team_account_ids=[true]\n",
	} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			path := clientPreferenceFixture(t, data)
			store := &tokenReferenceStore{}
			loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{"JIRA_REFRESH_INTERVAL=30", "JIRA_TEAM_ACCOUNT_IDS=[]"} }}, store)
			if err == nil || loaded != nil || store.reads != 0 {
				t.Fatal("environment masked a bad declaration or opened a provider", err)
			}
		})
	}
}

func TestClientPreferenceEnvironmentSnapshotSurvivesCredentialRebuild(t *testing.T) {
	t.Parallel()
	path := clientPreferenceFixture(t, "base_url='https://target.example.invalid'\n")
	environment := []string{"JIRA_REFRESH_INTERVAL=45", `JIRA_TEAM_ACCOUNT_IDS=["original"]`, "JIRA_BASE_URL=https://target.example.invalid"}
	raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://stored.example.invalid", Email: "public@example.invalid", Token: "synthetic-value"})
	if err != nil {
		t.Fatal(err)
	}
	store := &tokenReferenceStore{raw: string(raw), afterRead: func() {
		environment[0] = "JIRA_REFRESH_INTERVAL=99"
		environment[1] = `JIRA_TEAM_ACCOUNT_IDS=["changed"]`
	}}
	calls := 0
	loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string {
		calls++
		return environment
	}}, store)
	if err != nil || calls != 1 || store.reads != 1 || loaded.Config.Values()["refresh_interval"] != "45" || loaded.Config.Values()["team_account_ids"] != `["original"]` || loaded.Config.APIToken != "" || len(loaded.Warnings) == 0 {
		t.Fatal("credential rebuild reopened preferences or retained a foreign tuple", err)
	}
	if loaded.Config.BaseURL != credentials.NormalizeSite("https://target.example.invalid") {
		t.Fatal("credential rebuild changed public site")
	}
}
