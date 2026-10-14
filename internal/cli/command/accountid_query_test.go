package command

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestAccountQueryDoesNotInterpretSentinelsOrOpenLocalPreferences(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "live"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			cli.TakeWarnings()
			t.Cleanup(func() { cli.TakeWarnings() })
			value, constructed, searched, preferences, me := "@me", 0, 0, 0, 0
			client := &accountEmailClient{search: func(_ context.Context, query string, start, limit int) (core.UserSearchRead, error) {
				searched++
				if query != "@me" {
					t.Fatal("explicit query text was changed")
				}
				users := []*jira.User{}
				if start == 0 {
					users = append(users, &jira.User{AccountID: jira.NullableOf("opaque@future"), Active: jira.NullableOf(true)})
				}
				return core.UserSearchRead{Users: users}, nil
			}, me: func(context.Context) (core.UserRead, error) {
				me++
				return core.UserRead{}, errors.New("explicit query must not read myself")
			}}
			got, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructed++; return client, nil }, func() (core.IssuePreferences, error) {
				preferences++
				return core.IssuePreferences{CachedAccountID: "cached-self"}, nil
			}, local, false, []AccountField{{Flag: "user-query", Value: &value, LookupQuery: true}})
			if err != nil || preferences != 0 || me != 0 {
				t.Fatal("explicit query was reinterpreted as current identity", err, preferences, me)
			}
			warnings := cli.TakeWarnings()
			if local {
				if got != nil || constructed != 0 || searched != 0 || value != "@me" || len(warnings) != 1 || warnings[0].Type != "account_query_unresolved" {
					t.Fatal("local query opened client or fabricated identity", value, constructed, searched, warnings)
				}
			} else if got != client || constructed != 1 || searched != 2 || value != "opaque@future" || len(warnings) != 0 {
				t.Fatal("explicit query did not resolve exactly once", value, constructed, searched, warnings)
			}
		})
	}
}

func TestAccountQueriesShareCompleteSearchAndPublishAtomically(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"none", "missing", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first, second, reporter := "fixture@example.com", "fixture@example.com", MeSentinel
			searches := 0
			client := &accountEmailClient{me: func(context.Context) (core.UserRead, error) {
				return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("self")}}, nil
			}, search: func(_ context.Context, q string, start, limit int) (core.UserSearchRead, error) {
				searches++
				if q != "fixture@example.com" {
					t.Fatal("query deduplication changed search identity")
				}
				if failure == "canceled" {
					cancel()
				}
				users := []*jira.User{}
				if start == 0 && failure != "missing" {
					users = append(users, &jira.User{AccountID: jira.NullableOf("resolved"), Active: jira.NullableOf(true)})
				}
				return core.UserSearchRead{Users: users}, nil
			}}
			_, err := ResolveAccounts(ctx, func(context.Context) (core.JiraClient, error) { return client, nil }, nil, false, false, []AccountField{{Flag: FlagAssignee, Value: &first, AllowEmail: true}, {Flag: "user-query", Value: &second, LookupQuery: true}, {Flag: FlagReporter, Value: &reporter}})
			if failure == "none" {
				if err != nil || first != "resolved" || second != "resolved" || reporter != "self" || searches != 2 {
					t.Fatal("identical query consumers repeated search or lost result", err, first, second, reporter, searches)
				}
			} else {
				want := core.ErrUserNotFound
				if failure == "canceled" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || first != "fixture@example.com" || second != "fixture@example.com" || reporter != MeSentinel {
					t.Fatal("failed query published some bound fields", err, first, second, reporter)
				}
			}
		})
	}
}

func TestAccountQueryBlankRefusesBeforeFactory(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", " \t\n"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			original, constructed := value, 0
			_, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructed++; return nil, nil }, nil, false, false, []AccountField{{Flag: "user-query", Value: &value, LookupQuery: true}})
			if cli.MapError(err).Code != cli.CodeFlagValueInvalid || constructed != 0 || value != original {
				t.Fatal("blank query reached client or changed bound value", err)
			}
		})
	}
}
