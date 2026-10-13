//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveMigratedAccountIdentityServesOnlyMatchingLocalPreviews(t *testing.T) {
	r := newLiveRun(t)
	native := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account jira.User
	if json.Unmarshal(native.Response.JSON, &account) != nil || !account.AccountID.Set || account.AccountID.Null || strings.TrimSpace(account.AccountID.Value) == "" {
		t.Fatal("cached identity acceptance needs an independent native account")
	}
	_, status, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var scope struct {
		Data struct {
			BaseURL   string `json:"base_url"`
			Email     string `json:"email"`
			AccountID string `json:"account_id"`
			CloudID   string `json:"cloud_id"`
		} `json:"data"`
	}
	if exit != 0 || json.Unmarshal(status, &scope) != nil || scope.Data.BaseURL != r.manifest.BaseURL || scope.Data.Email != r.account || scope.Data.AccountID != account.AccountID.Value {
		t.Fatal("verified public cloud scope unavailable")
	}
	key := r.create(t, "cached account preview target")
	source, target := filepath.Join(r.dir, "legacy-account.toml"), filepath.Join(r.dir, "cached-account.toml")
	data := []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url=" + strconv.Quote(r.manifest.BaseURL) + "\nemail=" + strconv.Quote(r.account) + "\ncloud_id=" + strconv.Quote(scope.Data.CloudID) + "\naccount_id=" + strconv.Quote(account.AccountID.Value) + "\nsecret_backend='env'\n")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	offline := cachedAccountOfflineEnvironment(os.Environ(), r.manifest.BaseURL, r.account, scope.Data.CloudID)
	frame, stored, exit := r.invoke(t, t.Context(), offline, "config", "migrate", "--source", source, "--config", target, "--recovery-dir", filepath.Join(r.dir, "account-recovery"), "--yes")
	var migration struct {
		Data envelope.ConfigMigrateOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stored, &migration) != nil || migration.Data.Receipt == nil || migration.Data.Receipt.Status != "applied" || migration.Data.Receipt.CredentialState != "external" {
		t.Fatal("owned public identity migration did not apply without provider access")
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(before)
	file, err := config.ReadFile(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"account_id": account.AccountID.Value, "account_id_site": r.manifest.BaseURL, "account_id_email": r.account, "account_id_cloud_id": scope.Data.CloudID} {
		setting, err := file.Setting(key)
		if err != nil || !setting.Present || setting.Secret || setting.Value != want {
			t.Fatal("migrated stored identity does not retain the independently observed public binding", key)
		}
	}
	for _, consumer := range []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"issue", "create", "--project", r.manifest.Project, "--type", r.issueType, "--summary", "Local cached draft", "--assignee=@me", "--reporter=@me"}},
		{name: "edit", args: []string{"issue", "edit", key, "--assignee=@me", "--reporter=@me"}},
		{name: "clone", args: []string{"issue", "clone", key, "--assignee=@me", "--reporter=@me"}},
		{name: "watchers add", args: []string{"issue", "watchers", "add", key, "--user=@me"}},
		{name: "watchers remove", args: []string{"issue", "watchers", "remove", key, "--user=@me"}},
	} {
		t.Run(consumer.name, func(t *testing.T) {
			for _, matching := range []bool{true, false} {
				environment := append([]string{}, offline...)
				if !matching {
					environment = append(environment, "JIRA_EMAIL=other@example.invalid")
				}
				args := append(append([]string{}, consumer.args...), "--config", target, "--dry-run")
				frame, stdout, exit := r.invoke(t, t.Context(), environment, args...)
				var result struct {
					Warnings []envelope.Warning `json:"warnings"`
					Data     any                `json:"data"`
				}
				if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil {
					t.Fatal("offline migrated identity preview failed")
				}
				wanted := "account_id_unresolved"
				if matching {
					wanted = "account_id_cached"
				}
				count := 0
				for _, warning := range result.Warnings {
					if warning.Type == wanted {
						count++
					}
					if warning.Type == "account_id_cached" && !matching || warning.Type == "account_id_unresolved" && matching {
						t.Fatal("migrated cached identity crossed its account binding")
					}
				}
				if count != 1 {
					t.Fatal("cached/unresolved provenance missing")
				}
				wantedID, forbidden := account.AccountID.Value, "@me"
				if !matching {
					wantedID, forbidden = forbidden, wantedID
				}
				if !cachedAccountLeaf(result.Data, wantedID) || cachedAccountLeaf(result.Data, forbidden) {
					t.Fatal("local composer changed the bound native account ID")
				}
				if consumer.name == "clone" {
					var clone struct {
						Data envelope.IssueCloneOutput `json:"data"`
					}
					if json.Unmarshal(stdout, &clone) != nil || len(clone.Data.AccountOverrides) != 2 || clone.Data.AccountOverrides["assignee"] != wantedID || clone.Data.AccountOverrides["reporter"] != wantedID {
						t.Fatal("clone preview omitted either prepared account override")
					}
				}
				current, err := os.ReadFile(target)
				if err != nil || sha256.Sum256(current) != digest {
					t.Fatal("local preview changed migrated metadata")
				}
			}
		})
	}
	frame, restored, exit := r.invoke(t, t.Context(), offline, "config", "rollback", "--journal", migration.Data.Receipt.Journal, "--yes")
	var rollback struct {
		Data envelope.ConfigRollbackOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(restored, &rollback) != nil || !rollback.Data.Restored {
		t.Fatal("owned public metadata rollback did not complete")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("identity rollback did not restore target absence")
	}
	current, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(current) != sha256.Sum256(data) {
		t.Fatal("identity migration changed exact public source preimage")
	}
}

func cachedAccountOfflineEnvironment(inherited []string, site, account, cloud string) []string {
	// exec deduplicates exact variable names before koanf folds JIRA_ keys.
	// Clear every inherited spelling by name; never inspect its private value.
	var cleared []string
	for _, variable := range inherited {
		name, _, _ := strings.Cut(variable, "=")
		canonical := name
		if xos.IsWindows() {
			canonical = strings.ToUpper(canonical)
		}
		if !strings.HasPrefix(canonical, config.EnvPrefix) {
			continue
		}
		switch strings.ToLower(strings.TrimPrefix(canonical, config.EnvPrefix)) {
		case "account_id", "account_id_site", "account_id_email", "account_id_cloud_id",
			"base_url", "email", "cloud_id", "api_token", "api_token_env", "token_work", "keyring", "secret_backend":
			cleared = append(cleared, name+"=")
		}
	}
	return append(cleared, "JIRA_ACCOUNT_ID=", "JIRA_ACCOUNT_ID_SITE=", "JIRA_ACCOUNT_ID_EMAIL=", "JIRA_ACCOUNT_ID_CLOUD_ID=", "JIRA_BASE_URL="+site, "JIRA_EMAIL="+account, "JIRA_CLOUD_ID="+cloud, "JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=", "JIRA_TOKEN_WORK=", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=", "no_proxy=")
}

func TestCachedAccountOfflineEnvironmentCannotSupplyMissingStoredIdentity(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"JIRA_ACCOUNT_ID", "JIRA_account_id", "JIRA_ACCOUNT_İD", "JIRA_ACCOUNT_ıD"} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			inherited := []string{spelling + "=pretend-native", spelling + "_SITE=https://site.example.invalid", spelling + "_EMAIL=public@example.invalid", spelling + "_CLOUD_ID=bound-cloud", "JIRA_API_TOKEN=synthetic-value", "JIRA_TOKEN_WORK=synthetic-value", "no_proxy=*"}
			child := exec.Command("not-executed")
			child.Env = append(inherited, cachedAccountOfflineEnvironment(inherited, "https://site.example.invalid", "public@example.invalid", "bound-cloud")...)
			loaded, err := config.Load(config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: child.Environ})
			if err != nil || loaded.Config.AccountID != "" || loaded.Config.AccountIDSite != "" || loaded.Config.AccountIDEmail != "" || loaded.Config.AccountIDCloudID != "" || loaded.Config.APIToken != "" {
				t.Fatal("inherited metadata or credentials can mask a missing migration result", err)
			}
		})
	}
}

func cachedAccountLeaf(value any, wanted string) bool {
	switch value := value.(type) {
	case string:
		return value == wanted
	case map[string]any:
		for _, member := range value {
			if cachedAccountLeaf(member, wanted) {
				return true
			}
		}
	case []any:
		for _, member := range value {
			if cachedAccountLeaf(member, wanted) {
				return true
			}
		}
	}
	return false
}
