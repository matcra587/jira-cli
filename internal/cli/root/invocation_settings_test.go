package root

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestInvocationSharesProfileSnapshotAcrossClientCacheAndPreferences(t *testing.T) {
	// The environment and global flags are deliberately changed after the first
	// local consumer. Every later dependency must retain the original selection.
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://work.example.invalid'\nemail='work@example.invalid'\nsecret_backend='env'\ndefault_project='DEMO'\n[profiles.personal]\nbase_url='https://personal.example.invalid'\nemail='personal@example.invalid'\nsecret_backend='env'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_PROFILE", "work")
	t.Setenv("JIRA_API_TOKEN", "synthetic-snapshot-token")
	t.Setenv("JIRA_BASE_URL", "")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_SECRET_BACKEND", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	config.SetFlagLayer(config.FlagLayer{Path: path})
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	deps := defaultDeps()
	deps.BindContext(func() context.Context { return t.Context() })
	prefs, err := deps.BuildPreferences()
	if err != nil || prefs.Account != "work@example.invalid" || prefs.DefaultProject != "DEMO" {
		t.Fatalf("initial preferences: %+v, %v", prefs, err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(original, "default_profile='work'", "default_profile='personal'")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_PROFILE", "personal")
	t.Setenv("JIRA_EMAIL", "overlay@example.invalid")
	t.Setenv("JIRA_API_TOKEN", "different-snapshot-token")
	site, err := deps.Site(t.Context())
	if err != nil || site.Email != prefs.Account || site.BaseURL != prefs.BaseURL || site.Token != "synthetic-snapshot-token" {
		t.Fatalf("auth read drifted from captured preferences: %+v, %v", site, err)
	}
	client, err := deps.JiraClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	account, ok := client.(core.JiraAccountClient)
	if !ok || client.BaseURL() != prefs.BaseURL || account.Account() != prefs.Account {
		t.Fatal("client drifted from captured identity")
	}
	cache, err := deps.Cache(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Set("snapshot-fixture", []byte("owned")); err != nil {
		t.Fatal(err)
	}
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct {
		site, email string
		present     bool
	}{
		{prefs.BaseURL, prefs.Account, true},
		{"https://personal.example.invalid", "personal@example.invalid", false},
		{prefs.BaseURL, "overlay@example.invalid", false},
	} {
		bound, err := adapters.NewScopedCache(disk, scope.site, scope.email)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, present := bound.Get("snapshot-fixture"); present != scope.present {
			t.Fatalf("cache scope %s/%s present=%v", scope.site, scope.email, present)
		}
	}
}
