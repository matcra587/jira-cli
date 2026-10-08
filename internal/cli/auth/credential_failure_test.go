package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestLoginPreviewDoesNotRequireReadableStoredCredentials(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		name := "public identity fallback"
		if explicit {
			name = "explicit identity"
		}
		t.Run(name, func(t *testing.T) {
			const site = "https://fixture.example.invalid"
			const email = "fixture@example.invalid"
			const token = "synthetic-candidate-token"
			h := newHarness(t, SiteInfo{})
			h.deps.Site = func() (SiteInfo, error) {
				return SiteInfo{}, &cli.CodedError{
					Code:    cli.CodeAuthStoreUnavailable,
					Message: "fixture store unavailable", Cause: errors.New("fixture backend denied"),
				}
			}
			h.deps.SiteWithoutStore = func() (SiteInfo, error) {
				if explicit {
					return SiteInfo{}, nil
				}
				return SiteInfo{BaseURL: site, Email: email}, nil
			}
			h.deps.Credentials = func() (core.CredentialStore, error) {
				t.Fatal("login preview opened a store for mutation")
				return nil, nil
			}
			flags := loginFlags{withToken: true}
			if explicit {
				flags.site, flags.email = site, email
			}
			ident, candidate, err := gather(t.Context(), command.Invocation{Stdin: strings.NewReader(token + "\n")}, h.deps, flags)
			if err != nil {
				t.Fatalf("candidate login depended on the unavailable stored credential: %v", err)
			}
			out, err := login(t.Context(), h.deps, ident, candidate, true)
			if err != nil || !out.DryRun || out.AccountID == "" || out.BaseURL != site || out.Email != email || h.client.sawToken != token {
				t.Fatalf("preview did not verify the independent candidate: %+v %v", out, err)
			}
		})
	}
}

func TestLoginIdentityFallbackDoesNotHideOtherFailures(t *testing.T) {
	for _, cause := range []error{errors.New("fixture invalid config"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			h := newHarness(t, SiteInfo{})
			h.deps.Site = func() (SiteInfo, error) {
				if errors.Is(cause, context.Canceled) {
					return SiteInfo{}, &cli.CodedError{Code: cli.CodeAuthStoreUnavailable, Message: "canceled fixture lookup", Cause: cause}
				}
				return SiteInfo{}, cause
			}
			h.deps.SiteWithoutStore = func() (SiteInfo, error) {
				t.Fatal("login fell back after a config or cancellation failure")
				return SiteInfo{}, nil
			}
			_, _, err := gather(t.Context(), command.Invocation{}, h.deps, loginFlags{withToken: true})
			if !errors.Is(err, cause) {
				t.Fatalf("login lost its original failure: %v", err)
			}
		})
	}
}
