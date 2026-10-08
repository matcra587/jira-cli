package auth

import (
	"errors"
	"io"
	"net/http"
	"strings"
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
				calls := 0
				transport := identityResponseTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != http.MethodGet || req.URL.String() != "https://example.invalid/rest/api/3/myself" {
						t.Fatalf("unexpected identity request: %s %s", req.Method, req.URL)
					}
					email, candidate, ok := req.BasicAuth()
					if !ok || email != "person@example.com" || candidate != "fixture" {
						t.Fatal("identity verification lost candidate credentials")
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})
				opened := false
				store := newFakeStore()
				deps := Deps{
					Credentials: func() (core.CredentialStore, error) { opened = true; return store, nil },
					ClientFor: func(site, email, candidate string) (core.JiraClient, error) {
						return adapterjira.New(adapterjira.Config{BaseURL: site, Email: email, APIToken: candidate, HTTPClient: &http.Client{Transport: transport}})
					},
				}
				out, err := login(t.Context(), deps, identity{site: "https://example.invalid", email: "person@example.com"}, "fixture", mode.dryRun)
				if !errors.Is(err, adapterjira.ErrCurrentUserResponseInvalid) || calls != 1 || opened || store.setCall != 0 || out.AccountID != "" {
					t.Fatalf("malformed account accepted or credential store touched: %+v %v calls=%d opened=%t writes=%d", out, err, calls, opened, store.setCall)
				}
			})
		}
	}
}

type identityResponseTransport func(*http.Request) (*http.Response, error)

func (f identityResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
