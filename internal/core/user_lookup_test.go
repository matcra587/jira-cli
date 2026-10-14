package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

type userLookupFixture struct {
	read func(context.Context, string) (UserRead, error)
}

func (f userLookupFixture) GetUser(ctx context.Context, id string) (UserRead, error) {
	return f.read(ctx, id)
}

func TestExactUserLookupRequiresNativeIdentityActivityAndBinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		user *jira.UserDetails
		want error
	}{
		{name: "valid", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.NullableOf(true), EmailAddress: jira.NullableNull[string]()}},
		{name: "nil", want: ErrUserLookupResponseInvalid},
		{name: "unset ID", user: &jira.UserDetails{AccountID: jira.Nullable[string]{Value: "literal"}, Active: jira.NullableOf(true)}, want: ErrUserLookupResponseInvalid},
		{name: "null ID", user: &jira.UserDetails{AccountID: jira.Nullable[string]{Set: true, Null: true, Value: "literal"}, Active: jira.NullableOf(true)}, want: ErrUserLookupResponseInvalid},
		{name: "wrong ID", user: &jira.UserDetails{AccountID: jira.NullableOf("other"), Active: jira.NullableOf(true)}, want: ErrUserLookupResponseInvalid},
		{name: "unset active", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.Nullable[bool]{Value: true}}, want: ErrUserLookupResponseInvalid},
		{name: "null active", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.Nullable[bool]{Set: true, Null: true, Value: true}}, want: ErrUserLookupResponseInvalid},
		{name: "inactive", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.NullableOf(false)}, want: ErrUserNotFound},
		{name: "deleted", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.NullableOf(true), Extra: map[string]json.RawMessage{"deleted": []byte("true")}}, want: ErrUserNotFound},
		{name: "malformed deleted", user: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.NullableOf(true), Extra: map[string]json.RawMessage{"deleted": []byte(`"true"`)}}, want: ErrUserLookupResponseInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			id, err := ResolveActiveAccount(t.Context(), userLookupFixture{read: func(_ context.Context, id string) (UserRead, error) {
				calls++
				if id != "literal" {
					t.Fatal("requested identity changed")
				}
				return UserRead{User: tc.user}, nil
			}}, "literal")
			if calls != 1 || !errors.Is(err, tc.want) || (err == nil && id != "literal") || (err != nil && id != "") {
				t.Fatal("exact-account validation lost native state or selected a different account", id, err)
			}
		})
	}
}

func TestExactUserLookupCancellationPrecedesEntryAndPublication(t *testing.T) {
	t.Parallel()
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "after reply", true: "before entry"}[early], func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			if early {
				cancel()
			}
			id, err := ResolveActiveAccount(ctx, userLookupFixture{read: func(context.Context, string) (UserRead, error) {
				calls++
				cancel()
				return UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("literal"), Active: jira.NullableOf(true)}}, nil
			}}, "literal")
			wantCalls := 1
			if early {
				wantCalls = 0
			}
			if id != "" || !errors.Is(err, context.Canceled) || calls != wantCalls {
				t.Fatal("canceled account lookup entered or published", id, err, calls)
			}
		})
	}
}
