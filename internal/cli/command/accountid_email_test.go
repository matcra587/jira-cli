package command

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type accountEmailClient struct {
	core.JiraClient
	search func(context.Context, string, int, int) (core.UserSearchRead, error)
	me     func(context.Context) (core.UserRead, error)
}

func (c *accountEmailClient) SearchUsers(ctx context.Context, q string, start, limit int) (core.UserSearchRead, error) {
	return c.search(ctx, q, start, limit)
}

func (c *accountEmailClient) CurrentUser(ctx context.Context) (core.UserRead, error) {
	return c.me(ctx)
}

func TestAssigneeEmailAccountResolutionPolicy(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, remote := range []bool{false, true} {
			t.Run("preview="+strconv.FormatBool(preview)+"/remote="+strconv.FormatBool(remote), func(t *testing.T) {
				cli.TakeWarnings()
				t.Cleanup(func() { cli.TakeWarnings() })
				email := "fixture@example.com"
				constructed, searches, preferences := 0, 0, 0
				client := &accountEmailClient{search: func(_ context.Context, q string, start, limit int) (core.UserSearchRead, error) {
					searches++
					if q != email || limit != 50 {
						t.Error("email query changed")
					}
					users := []*jira.User{}
					if start == 0 {
						users = append(users, &jira.User{AccountID: jira.NullableOf("opaque@future"), Active: jira.NullableOf(true)})
					}
					return core.UserSearchRead{Users: users}, nil
				}}
				got, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructed++; return client, nil }, func() (core.IssuePreferences, error) { preferences++; return core.IssuePreferences{}, nil }, preview, remote, []AccountField{{Flag: FlagAssignee, Value: &email, AllowEmail: true}})
				if err != nil {
					t.Fatal(err)
				}
				warnings := cli.TakeWarnings()
				if preview && !remote {
					if len(warnings) != 1 || warnings[0].Type != "account_email_unresolved" || warnings[0].Lossy {
						t.Fatal("local preview omitted its precise unresolved warning")
					}
					if email != "fixture@example.com" || got != nil || constructed != 0 || searches != 0 || preferences != 0 {
						t.Fatal("local email preview consulted credentials/preferences/network")
					}
				} else if email != "opaque@future" || got != client || constructed != 1 || searches != 2 || preferences != 0 {
					t.Fatal("unique complete email search did not resolve once", email, constructed, searches, preferences)
				}
			})
		}
	}
}

func TestAssigneeEmailMalformedInputRefusesBeforeClient(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"invalid@", "Name <fixture@example.com>", "fixture(comment)@example.com", "fixture@example.com\n", "two@example.com,other@example.com"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			constructed := 0
			original := value
			_, err := ResolveAccounts(t.Context(), func(context.Context) (core.JiraClient, error) { constructed++; return nil, nil }, nil, false, false, []AccountField{{Flag: FlagAssignee, Value: &value, AllowEmail: true}})
			if cli.MapError(err).Code != cli.CodeFlagValueInvalid || constructed != 0 || value != original {
				t.Fatal("invalid email reached construction or changed input", err)
			}
		})
	}
}

func TestAssigneeEmailFailureLeavesMixedAccountFieldsUnchanged(t *testing.T) {
	t.Parallel()
	for _, cancelAfterRead := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelAfterRead), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			email, reporter := "fixture@example.com", MeSentinel
			client := &accountEmailClient{me: func(context.Context) (core.UserRead, error) {
				return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("verified-self")}}, nil
			}, search: func(context.Context, string, int, int) (core.UserSearchRead, error) {
				if cancelAfterRead {
					cancel()
				}
				return core.UserSearchRead{Users: []*jira.User{}}, nil
			}}
			_, err := ResolveAccounts(ctx, func(context.Context) (core.JiraClient, error) { return client, nil }, nil, false, false, []AccountField{{Flag: FlagAssignee, Value: &email, AllowEmail: true}, {Flag: FlagReporter, Value: &reporter}})
			want := core.ErrUserNotFound
			if cancelAfterRead {
				want = context.Canceled
			}
			if !errors.Is(err, want) || email != "fixture@example.com" || reporter != MeSentinel {
				t.Fatal("failed mixed resolution partially changed account flags", err, email, reporter)
			}
		})
	}
}
