package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestCachedAccountIdentityRequiresIndependentMatchingPublicScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*config.Config)
		usable bool
	}{
		{name: "matching", usable: true},
		{name: "equivalent spelling", usable: true, change: func(c *config.Config) {
			c.BaseURL = "https://SITE.example.invalid/jira/"
			c.Email = " PUBLIC@example.invalid "
		}},
		{name: "other site", change: func(c *config.Config) { c.BaseURL = "https://other.example.invalid/jira" }},
		{name: "other path", change: func(c *config.Config) { c.BaseURL = "https://site.example.invalid/Jira" }},
		{name: "other account", change: func(c *config.Config) { c.Email = "other@example.invalid" }},
		{name: "other cloud", change: func(c *config.Config) { c.CloudID = "other-cloud" }},
		{name: "cleared cloud", change: func(c *config.Config) { c.CloudID = "" }},
		{name: "absent site binding", change: func(c *config.Config) { c.AccountIDSite = "" }},
		{name: "absent account binding", change: func(c *config.Config) { c.AccountIDEmail = "" }},
		{name: "absent account everywhere", change: func(c *config.Config) { c.AccountIDEmail, c.Email = "", "" }},
		{name: "classic scope", usable: true, change: func(c *config.Config) { c.CloudID, c.AccountIDCloudID = "", "" }},
		{name: "blank id", change: func(c *config.Config) { c.AccountID = " \t" }},
		{name: "empty id", change: func(c *config.Config) { c.AccountID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := config.Config{BaseURL: "https://site.example.invalid/jira", Email: "public@example.invalid", CloudID: "bound-cloud", AccountID: "literal,account", AccountIDSite: "https://site.example.invalid/jira", AccountIDEmail: "public@example.invalid", AccountIDCloudID: "bound-cloud"}
			if tc.change != nil {
				tc.change(&c)
			}
			want := ""
			if tc.usable {
				want = "literal,account"
			}
			if actual := c.CachedAccountID(); actual != want {
				t.Fatalf("cached account scope: got %q, want %q", actual, want)
			}
		})
	}
}

func TestCachedIdentityEnvironmentSnapshotSurvivesCredentialRebuild(t *testing.T) {
	t.Parallel()
	path := clientPreferenceFixture(t, "email='public@example.invalid'\naccount_id_site='https://target.example.invalid'\naccount_id_email='public@example.invalid'\n")
	environment := []string{"JIRA_BASE_URL=https://target.example.invalid", "JIRA_ACCOUNT_ID=original-id"}
	raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://stored.example.invalid", Email: "public@example.invalid", Token: "synthetic-value"})
	if err != nil {
		t.Fatal(err)
	}
	store := &tokenReferenceStore{raw: string(raw), afterRead: func() { environment[1] = "JIRA_ACCOUNT_ID=changed-id" }}
	reads := 0
	loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { reads++; return environment }}, store)
	if err != nil || reads != 1 || store.reads != 1 || loaded.Config.CachedAccountID() != "original-id" || loaded.Config.APIToken != "" || loaded.Source("account_id") != config.LayerEnv {
		t.Fatal("credential rebuild reopened or rebound cached identity environment", err)
	}
}

func TestMalformedCachedIdentityRefusesBeforeProviderAndCannotBeMasked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, data  string
		environment []string
	}{
		{name: "blank file id", data: "account_id=' '\n", environment: []string{"JIRA_ACCOUNT_ID=valid"}},
		{name: "typed file id", data: "account_id=42\n", environment: []string{"JIRA_ACCOUNT_ID=valid"}},
		{name: "typed file binding", data: "account_id_email=false\n", environment: []string{"JIRA_ACCOUNT_ID_EMAIL=public@example.invalid"}},
		{name: "unsafe file site", data: "account_id_site='https://discarded-marker@site.example.invalid'\n"},
		{name: "unsafe file cloud", data: "account_id_cloud_id='bad/cloud'\n"},
		{name: "blank environment id", environment: []string{"JIRA_ACCOUNT_ID= "}},
		{name: "unsafe environment site", environment: []string{"JIRA_ACCOUNT_ID_SITE=https://discarded-marker@site.example.invalid"}},
		{name: "blank environment account", environment: []string{"JIRA_ACCOUNT_ID_EMAIL= "}},
		{name: "unsafe environment cloud", environment: []string{"JIRA_ACCOUNT_ID_CLOUD_ID=bad/cloud"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := clientPreferenceFixture(t, tc.data)
			store := &tokenReferenceStore{}
			loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return tc.environment }}, store)
			if err == nil || loaded != nil || store.reads != 0 || strings.Contains(err.Error(), "discarded-marker") {
				t.Fatal("invalid cached identity entered a provider, was masked or exposed input", err)
			}
		})
	}
}

func TestCachedAccountIdentityDoesNotFollowCredentialScope(t *testing.T) {
	t.Parallel()
	path := clientPreferenceFixture(t, "base_url='https://file.example.invalid'\nemail='public@example.invalid'\naccount_id='file-id'\naccount_id_site='https://file.example.invalid'\naccount_id_email='public@example.invalid'\naccount_id_cloud_id=''\n")
	raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://stored.example.invalid", Email: "public@example.invalid", Token: "synthetic-value"})
	if err != nil {
		t.Fatal(err)
	}
	store := &tokenReferenceStore{raw: string(raw)}
	loaded, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return nil }}, store)
	if err != nil || store.reads != 1 || loaded.Config.BaseURL != "https://stored.example.invalid" || loaded.Config.AccountID != "file-id" || loaded.Config.CachedAccountID() != "" {
		t.Fatal("credential tuple silently rebound public cached identity", err)
	}
}

func TestCachedIdentitySettingsPublishOneTypedBoundCandidate(t *testing.T) {
	t.Parallel()
	path := clientPreferenceFixture(t, "# retain exact no-op bytes\nbase_url='https://site.example.invalid'\nemail='public@example.invalid'\n")
	file, err := config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"account_id": "literal,account", "account_id_site": "https://site.example.invalid", "account_id_email": "public@example.invalid", "account_id_cloud_id": ""}
	update, err := file.PrepareSettings(values)
	if err != nil {
		t.Fatal("cached identity settings are not writable", err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatal("bound identity did not publish", err)
	}
	loaded, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.CachedAccountID() != "literal,account" || loaded.Source("account_id") != config.LayerFile {
		t.Fatal("published bound identity or provenance changed", err)
	}
	file, err = config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := file.PrepareSettings(values)
	if err != nil || noop.WouldChange() {
		t.Fatal("equal bound identity changed authored bytes", err)
	}
	setting, err := file.Setting("account_id")
	if err != nil || !setting.Present || setting.Value != "literal,account" || setting.Secret {
		t.Fatal("public cached account ID was lost or redacted", err)
	}
}

func TestCachedIdentityRepairRetainsSafeStoredInspection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ key, bad, repaired string }{
		{"account_id", " ", "repaired-id"},
		{"account_id_site", "https://discarded-marker@site.example.invalid", "https://site.example.invalid"},
		{"account_id_email", " ", "public@example.invalid"},
		{"account_id_cloud_id", "bad/cloud", "bound-cloud"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			path := clientPreferenceFixture(t, tc.key+"='"+tc.bad+"'\n")
			file, err := config.ReadFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			setting, err := file.Setting(tc.key)
			if err != nil || !setting.Present || strings.Contains(setting.Value, "discarded-marker") {
				t.Fatal("malformed binding cannot be inspected safely", err)
			}
			if tc.key == "account_id_site" && !setting.Secret {
				t.Fatal("userinfo binding was not redacted")
			}
			prepared, err := file.PrepareSetting(tc.key, tc.repaired)
			if err != nil {
				t.Fatal("valid replacement cannot repair the cached identity declaration", err)
			}
			if changed, err := prepared.Save(t.Context()); err != nil || !changed {
				t.Fatal("cached identity repair did not publish", err)
			}
		})
	}
}
