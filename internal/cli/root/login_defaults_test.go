package root

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type loginDefaultsStore struct {
	core.CredentialStore
	raw   string
	reads int
}

func (s *loginDefaultsStore) Get(context.Context, string) (string, error) {
	s.reads++
	return s.raw, nil
}

func TestLoginStoredDefaultsFillOnlyMissingPublicFields(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"site", "email"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			content := "email='public@example.invalid'\n"
			wantSite, wantEmail := "https://stored.invalid", "public@example.invalid"
			if missing == "email" {
				content = "base_url='https://public.invalid'\n"
				wantSite, wantEmail = "https://public.invalid", "stored@example.invalid"
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://stored.invalid", Email: "stored@example.invalid", Token: "synthetic-stored-token"})
			if err != nil {
				t.Fatal(err)
			}
			store := &loginDefaultsStore{raw: string(raw)}
			factories := 0
			site, err := resolveLoginSite(t.Context(), config.Options{Path: path, Environ: func() []string { return nil }}, authcmd.LoginDefaults{Site: true, Email: true}, func(config.Config) core.CredentialStore { factories++; return store })
			if err != nil || site.BaseURL != wantSite || site.Email != wantEmail || factories != 1 || store.reads != 1 {
				t.Fatalf("stored defaults replaced an already supplied public field: site=%q email=%q factories=%d reads=%d error=%v", site.BaseURL, site.Email, factories, store.reads, err)
			}
		})
	}
}
