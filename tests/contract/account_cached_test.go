package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCachedAccountPreviewBindsScopeWithoutCredentialsOrNetwork(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const original = "base_url='https://site.example.invalid/jira'\nemail='public@example.invalid'\ncloud_id='bound-cloud'\naccount_id='cached,account'\naccount_id_site='https://site.example.invalid/jira'\naccount_id_email='public@example.invalid'\naccount_id_cloud_id='bound-cloud'\nsecret_backend='env'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct {
		name        string
		environment []string
		cached      bool
	}{
		{name: "matching", cached: true},
		{name: "equivalent", cached: true, environment: []string{"JIRA_BASE_URL=https://SITE.example.invalid/jira/", "JIRA_EMAIL=PUBLIC@example.invalid"}},
		{name: "different site", environment: []string{"JIRA_BASE_URL=https://other.example.invalid/jira"}},
		{name: "different account", environment: []string{"JIRA_EMAIL=other@example.invalid"}},
		{name: "different cloud", environment: []string{"JIRA_CLOUD_ID=other-cloud"}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			t.Parallel()
			for _, consumer := range []struct {
				name string
				args []string
			}{
				{name: "create", args: []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=Draft", "--assignee=@me", "--reporter=@me"}},
				{name: "edit", args: []string{"issue", "edit", "KEY-1", "--assignee=@me", "--reporter=@me"}},
				{name: "clone", args: []string{"issue", "clone", "KEY-1", "--assignee=@me", "--reporter=@me"}},
				{name: "watchers add", args: []string{"issue", "watchers", "add", "KEY-1", "--user=@me"}},
				{name: "watchers remove", args: []string{"issue", "watchers", "remove", "KEY-1", "--user=@me"}},
			} {
				t.Run(consumer.name, func(t *testing.T) {
					t.Parallel()
					args := append(consumer.args, "--config", path, "--dry-run", "--output=json", "--no-input")
					stdout, stderr, exit := runBinaryEnv(t, scope.environment, args...)
					var frame struct {
						OK       bool               `json:"ok"`
						Warnings []envelope.Warning `json:"warnings"`
						Data     json.RawMessage    `json:"data"`
					}
					if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK {
						t.Fatal("offline cached preview did not run successfully", exit, stdout, stderr)
					}
					wantWarning := "account_id_unresolved"
					if scope.cached {
						wantWarning = "account_id_cached"
					}
					found := 0
					for _, warning := range frame.Warnings {
						if warning.Type == wantWarning {
							found++
						}
						if warning.Type == "account_id_cached" && !scope.cached || warning.Type == "account_id_unresolved" && scope.cached {
							t.Fatal("scope mismatch silently selected cached identity")
						}
					}
					if found != 1 {
						t.Fatal("cached/unresolved preview provenance missing")
					}
					var data any
					if json.Unmarshal(frame.Data, &data) != nil {
						t.Fatal("invalid typed preview data")
					}
					wanted := "@me"
					if scope.cached {
						wanted = "cached,account"
					}
					if !cachedPreviewContainsAccount(data, wanted) || cachedPreviewContainsAccount(data, map[bool]string{true: "@me", false: "cached,account"}[scope.cached]) {
						t.Fatal("composed native account did not match cache binding")
					}
					if consumer.name == "clone" {
						var clone envelope.IssueCloneOutput
						if json.Unmarshal(frame.Data, &clone) != nil || len(clone.AccountOverrides) != 2 || clone.AccountOverrides["assignee"] != wanted || clone.AccountOverrides["reporter"] != wanted {
							t.Fatal("clone preview omitted either prepared account override")
						}
					}
					contents, err := os.ReadFile(path)
					if err != nil || string(contents) != original {
						t.Fatal("read-only preview changed cached identity settings", err)
					}
				})
			}
		})
	}
}

// Inspect decoded leaf values, independently of indentation and payload layout.
func cachedPreviewContainsAccount(value any, want string) bool {
	switch value := value.(type) {
	case string:
		return value == want
	case map[string]any:
		for _, member := range value {
			if cachedPreviewContainsAccount(member, want) {
				return true
			}
		}
	case []any:
		for _, member := range value {
			if cachedPreviewContainsAccount(member, want) {
				return true
			}
		}
	}
	return false
}
