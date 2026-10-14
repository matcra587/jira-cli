package command

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type accountLiteralClient struct {
	core.JiraClient
	get    func(context.Context, string) (core.UserRead, error)
	me     func(context.Context) (core.UserRead, error)
	search func(context.Context, string, int, int) (core.UserSearchRead, error)
}

func (c *accountLiteralClient) GetUser(ctx context.Context, id string) (core.UserRead, error) {
	return c.get(ctx, id)
}

func (c *accountLiteralClient) CurrentUser(ctx context.Context) (core.UserRead, error) {
	return c.me(ctx)
}

func (c *accountLiteralClient) SearchUsers(ctx context.Context, q string, start, limit int) (core.UserSearchRead, error) {
	return c.search(ctx, q, start, limit)
}

func TestAccountLiteralValidationIsExplicitAndDemandDriven(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{false, true} {
		for _, remote := range []bool{false, true} {
			t.Run(map[bool]string{false: "live", true: "preview"}[preview]+map[bool]string{false: " ordinary", true: " remote"}[remote], func(t *testing.T) {
				t.Parallel()
				first, second := "literal", "literal"
				constructed, reads, preferences := 0, 0, 0
				client := &accountLiteralClient{get: func(_ context.Context, id string) (core.UserRead, error) {
					reads++
					return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf(id), Active: jira.NullableOf(true)}}, nil
				}}
				returned, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructed++; return client, nil }, func() (core.IssuePreferences, error) {
					preferences++
					return core.IssuePreferences{}, errors.New("unexpected cached identity")
				}, preview, remote, []AccountField{{Flag: "user", Value: &first, ValidateRemoteID: true}, {Flag: "other", Value: &second, ValidateRemoteID: true}})
				if err != nil || preferences != 0 || first != "literal" || second != "literal" {
					t.Fatal("literal account policy changed ID or read preferences", err)
				}
				if remote {
					if returned != client || constructed != 1 || reads != 1 {
						t.Fatal("exact-ID lookup not reused", constructed, reads)
					}
				} else if returned != nil || constructed != 0 || reads != 0 {
					t.Fatal("ordinary literal path opened lookup", constructed, reads)
				}
			})
		}
	}
}

func TestAccountLiteralAndQueryKindsNeverShareMatchesAndPublishAtomically(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"none", "wrong ID", "canceled", "search failure"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first, query, me := "literal", "literal", MeSentinel
			gets, searches := 0, 0
			client := &accountLiteralClient{me: func(context.Context) (core.UserRead, error) {
				return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("self")}}, nil
			}, get: func(context.Context, string) (core.UserRead, error) {
				gets++
				id := "literal"
				if failure == "wrong ID" {
					id = "other"
				}
				if failure == "canceled" {
					cancel()
				}
				return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf(id), Active: jira.NullableOf(true)}}, nil
			}, search: func(_ context.Context, q string, start, limit int) (core.UserSearchRead, error) {
				searches++
				if failure == "search failure" {
					return core.UserSearchRead{}, core.ErrUserNotFound
				}
				users := []*jira.User{}
				if start == 0 {
					users = append(users, &jira.User{AccountID: jira.NullableOf("query-match"), Active: jira.NullableOf(true)})
				}
				return core.UserSearchRead{Users: users}, nil
			}}
			_, err := ResolveAccounts(ctx, func(context.Context) (core.JiraClient, error) { return client, nil }, nil, false, true, []AccountField{{Flag: "user", Value: &first, ValidateRemoteID: true}, {Flag: "query", Value: &query, LookupQuery: true}, {Flag: "self", Value: &me}})
			if failure == "none" {
				if err != nil || first != "literal" || query != "query-match" || me != "self" || gets != 1 || searches != 2 {
					t.Fatal("literal ID shared query cache or lost atomically resolved fields", err, first, query, me, gets, searches)
				}
			} else if err == nil || first != "literal" || query != "literal" || me != MeSentinel {
				t.Fatal("failed mixed lookup partially published fields", err, first, query, me)
			}
		})
	}
}
