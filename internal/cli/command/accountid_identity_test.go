package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type accountIdentityClient struct {
	core.JiraClient
	user   *jira.UserDetails
	calls  int
	cancel context.CancelFunc
}

func (c *accountIdentityClient) CurrentUser(context.Context) (core.UserRead, error) {
	c.calls++
	if c.cancel != nil {
		c.cancel()
	}
	return core.UserRead{User: c.user}, nil
}

func TestResolveMeRefusesInvalidNativeIdentityBeforeChangingFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		user *jira.UserDetails
	}{
		{name: "nil"},
		{name: "absent", user: &jira.UserDetails{}},
		{name: "absent populated", user: &jira.UserDetails{AccountID: jira.Nullable[string]{Value: "unusable-marker"}}},
		{name: "null populated", user: &jira.UserDetails{AccountID: jira.Nullable[string]{Set: true, Null: true, Value: "unusable-marker"}}},
		{name: "empty", user: &jira.UserDetails{AccountID: jira.NullableOf("")}},
		{name: "blank", user: &jira.UserDetails{AccountID: jira.NullableOf(" \t\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assignee, reporter, literal := MeSentinel, MeSentinel, "literal-account"
			client := &accountIdentityClient{user: tc.user}
			err := ResolveMe(t.Context(), client, []AccountField{
				{Flag: FlagAssignee, Value: &assignee},
				{Flag: FlagReporter, Value: &reporter},
				{Flag: "user", Value: &literal},
			})
			if !errors.Is(err, core.ErrCurrentUserResponseInvalid) || cli.MapError(err).Code != cli.CodeCurrentUserResponseInvalid {
				t.Fatalf("unusable account lost protocol classification: %v", err)
			}
			if client.calls != 1 || assignee != MeSentinel || reporter != MeSentinel || literal != "literal-account" || strings.Contains(err.Error(), "unusable-marker") {
				t.Fatal("unusable identity changed fields, repeated lookup or exposed its discarded value")
			}
		})
	}
}

func TestResolveMeUsesOneValidNativeIdentityForEveryPendingField(t *testing.T) {
	t.Parallel()
	assignee, reporter, literal := MeSentinel, MeSentinel, "literal-account"
	client := &accountIdentityClient{user: &jira.UserDetails{AccountID: jira.NullableOf("verified-account")}}
	err := ResolveMe(t.Context(), client, []AccountField{
		{Flag: FlagAssignee, Value: &assignee},
		{Flag: FlagReporter, Value: &reporter},
		{Flag: "user", Value: &literal},
	})
	if err != nil || client.calls != 1 || assignee != "verified-account" || reporter != "verified-account" || literal != "literal-account" {
		t.Fatal("valid identity did not resolve all pending fields once", err)
	}
}

func TestResolveMeCanceledBeforeLookupLeavesFieldsUnchanged(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	account := MeSentinel
	client := &accountIdentityClient{user: &jira.UserDetails{AccountID: jira.NullableOf("verified-account")}}
	err := ResolveMe(ctx, client, []AccountField{{Flag: FlagAssignee, Value: &account}})
	if !errors.Is(err, context.Canceled) || client.calls != 0 || account != MeSentinel {
		t.Fatalf("canceled resolution entered lookup or changed input: calls=%d err=%v", client.calls, err)
	}
}

func TestResolveMeCanceledReplyLeavesEveryFieldUnchanged(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	assignee, reporter := MeSentinel, MeSentinel
	client := &accountIdentityClient{user: &jira.UserDetails{AccountID: jira.NullableOf("verified-account")}, cancel: cancel}
	err := ResolveMe(ctx, client, []AccountField{{Flag: FlagAssignee, Value: &assignee}, {Flag: FlagReporter, Value: &reporter}})
	if !errors.Is(err, context.Canceled) || client.calls != 1 || assignee != MeSentinel || reporter != MeSentinel {
		t.Fatal("canceled joined reply became a resolved identity", err)
	}
}

func TestResolveAccountsCanceledBeforeConstructionOrPreview(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name     string
		preview  bool
		validate bool
	}{
		{name: "live"},
		{name: "local preview", preview: true},
		{name: "remote preview", preview: true, validate: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			account := MeSentinel
			calls := 0
			client, err := ResolveAccounts(ctx, func(context.Context) (core.JiraClient, error) {
				calls++
				return nil, errors.New("unexpected constructor")
			}, nil, mode.preview, mode.validate, []AccountField{{Flag: "user", Value: &account}})
			if !errors.Is(err, context.Canceled) || client != nil || calls != 0 || account != MeSentinel {
				t.Fatalf("canceled resolution entered constructor or preview: calls=%d err=%v", calls, err)
			}
		})
	}
}
