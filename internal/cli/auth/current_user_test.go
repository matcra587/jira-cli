package auth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestLoginRejectsMalformedIdentityBeforeOpeningCredentialStore(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name   string
		dryRun bool
	}{
		{name: "live", dryRun: false},
		{name: "preview", dryRun: true},
	} {
		for _, body := range []string{`null`, `{}`, `{"accountId":""}`, `{"accountId":" \t"}`, `{"accountId":"fixture"} {}`} {
			t.Run(body+"/"+mode.name, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
				t.Cleanup(server.Close)
				opened := false
				store := newFakeStore()
				deps := Deps{
					Credentials: func() (core.CredentialStore, error) { opened = true; return store, nil },
					ClientFor: func(site, email, candidate string) (core.JiraClient, error) {
						return adapterjira.New(adapterjira.Config{BaseURL: site, Email: email, APIToken: candidate})
					},
				}
				out, err := login(t.Context(), deps, identity{site: server.URL, email: "person@example.com"}, "fixture", mode.dryRun)
				if !errors.Is(err, adapterjira.ErrCurrentUserResponseInvalid) || opened || store.setCall != 0 || out.AccountID != "" {
					t.Fatalf("malformed account accepted or credential store touched: %+v %v opened=%t writes=%d", out, err, opened, store.setCall)
				}
			})
		}
	}
}
