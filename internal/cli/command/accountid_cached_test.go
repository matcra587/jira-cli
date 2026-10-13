package command

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestCachedAccountResolutionUsesLocalPreferencesOnlyForBarePreview(t *testing.T) {
	// These successful previews publish to the process-wide warning queue.
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })
	for _, mode := range []struct {
		name            string
		preview, remote bool
		cached          string
	}{
		{name: "matching bare preview", preview: true, cached: "cached-id"},
		{name: "empty bare preview", preview: true},
		{name: "live", cached: "must-not-authorize"},
		{name: "remote preview", preview: true, remote: true, cached: "must-not-authorize"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			account, reporter, literal := MeSentinel, MeSentinel, "literal"
			preferences, constructors := 0, 0
			client := &accountIdentityClient{user: &jira.UserDetails{AccountID: jira.NullableOf("remote-id")}}
			result, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructors++; return client, nil }, func() (core.IssuePreferences, error) {
				preferences++
				return core.IssuePreferences{CachedAccountID: mode.cached}, nil
			}, mode.preview, mode.remote, []AccountField{{Flag: "assignee", Value: &account}, {Flag: "reporter", Value: &reporter}, {Flag: "user", Value: &literal}})
			if err != nil {
				t.Fatal(err)
			}
			want := "remote-id"
			if mode.preview && !mode.remote {
				want = mode.cached
				if want == "" {
					want = MeSentinel
				}
				if preferences != 1 || constructors != 0 || client.calls != 0 || result != nil {
					t.Fatal("local preview entered a remote owner or omitted preferences")
				}
			} else if preferences != 0 || constructors != 1 || client.calls != 1 || result != client {
				t.Fatal("live/validated preview used cached identity or repeated lookup")
			}
			if account != want || reporter != want || literal != "literal" {
				t.Fatal("cached/remote resolution changed the wrong fields")
			}
			warnings := cli.TakeWarnings()
			if mode.preview && !mode.remote {
				warningType := "account_id_unresolved"
				if mode.cached != "" {
					warningType = "account_id_cached"
				}
				if len(warnings) != 1 || warnings[0].Type != warningType {
					t.Fatalf("preview warning lost or contaminated: %+v", warnings)
				}
			} else if len(warnings) != 0 {
				t.Fatalf("remote verification published cached provenance: %+v", warnings)
			}
		})
	}
}

func TestCachedAccountPreferenceFailureAndCancellationLeaveFieldsUnchanged(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"failure", "cancel before", "cancel during", "blank"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("preference lookup failed")
			if mode == "cancel before" {
				cancel()
			}
			account := MeSentinel
			reads, constructors := 0, 0
			_, err := ResolveAccounts(ctx, func(context.Context) (core.JiraClient, error) { constructors++; return nil, failure }, func() (core.IssuePreferences, error) {
				reads++
				if mode == "failure" {
					return core.IssuePreferences{}, failure
				}
				if mode == "cancel during" {
					cancel()
				}
				id := "cached-id"
				if mode == "blank" {
					id = " \t"
				}
				return core.IssuePreferences{CachedAccountID: id}, nil
			}, true, false, []AccountField{{Flag: "assignee", Value: &account}})
			if err == nil || account != MeSentinel || constructors != 0 {
				t.Fatal("invalid/canceled preference became a resolved field", err)
			}
			if mode == "failure" && !errors.Is(err, failure) || (mode == "cancel before" || mode == "cancel during") && !errors.Is(err, context.Canceled) || mode == "blank" && cli.MapError(err).Code != cli.CodeConfigInvalid {
				t.Fatal("preference failure lost its classification", err)
			}
			want := 1
			if mode == "cancel before" {
				want = 0
			}
			if reads != want {
				t.Fatal("canceled resolution entered preferences")
			}
		})
	}
}

func TestLiteralAccountResolutionDoesNotReadCachedPreferences(t *testing.T) {
	t.Parallel()
	account := "literal"
	_, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { t.Fatal("literal constructed client"); return nil, nil }, func() (core.IssuePreferences, error) {
		t.Fatal("literal read cached preferences")
		return core.IssuePreferences{}, nil
	}, true, false, []AccountField{{Flag: "assignee", Value: &account}})
	if err != nil || account != "literal" {
		t.Fatal("literal resolution changed", err)
	}
}
