package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

type userSearchFixture struct {
	read func(context.Context, string, int, int) (UserSearchRead, error)
}

func (f userSearchFixture) SearchUsers(ctx context.Context, q string, start, limit int) (UserSearchRead, error) {
	return f.read(ctx, q, start, limit)
}

func searchUser(id string, active bool) *jira.User {
	return &jira.User{AccountID: jira.NullableOf(id), Active: jira.NullableOf(active)}
}

func TestVisibleUserResolutionRequiresCompleteUniqueActiveSearch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		pages   [][]*jira.User
		want    string
		failure error
	}{
		{name: "hidden email and short page", pages: [][]*jira.User{{searchUser("opaque@future", true)}, {}}, want: "opaque@future"},
		{name: "inactive before visible", pages: [][]*jira.User{{searchUser("inactive", false)}, {searchUser("selected", true)}, {}}, want: "selected"},
		{name: "missing", pages: [][]*jira.User{{}}, failure: ErrUserNotFound},
		{name: "multiple pages", pages: [][]*jira.User{{searchUser("one", true)}, {searchUser("two", true)}}, failure: ErrUserAmbiguous},
		{name: "repeated identity", pages: [][]*jira.User{{searchUser("one", true)}, {searchUser("one", true)}}, failure: ErrUserSearchResponseInvalid},
		{name: "null array", pages: [][]*jira.User{nil}, failure: ErrUserSearchResponseInvalid},
		{name: "blank identity", pages: [][]*jira.User{{searchUser(" \t", true)}}, failure: ErrUserSearchResponseInvalid},
		{name: "unknown activity", pages: [][]*jira.User{{{AccountID: jira.NullableOf("unknown")}}, {}}, failure: ErrUserNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page := 0
			offset := 0
			client := userSearchFixture{read: func(ctx context.Context, q string, start, limit int) (UserSearchRead, error) {
				if ctx != t.Context() || q != "fixture@example.com" || start != offset || limit != 50 {
					t.Error("search changed context/query/offset/page size")
				}
				if page >= len(tc.pages) {
					t.Fatal("unexpected extra page")
				}
				rows := tc.pages[page]
				page++
				offset += len(rows)
				return UserSearchRead{Users: rows}, nil
			}}
			got, err := ResolveVisibleUser(t.Context(), client, "fixture@example.com")
			if got != tc.want || !errors.Is(err, tc.failure) {
				t.Fatalf("resolution=%q,%v; want=%q,%v", got, err, tc.want, tc.failure)
			}
		})
	}
}

func TestVisibleUserResolutionRefusesCapAndCanceledReply(t *testing.T) {
	t.Parallel()
	calls := 0
	client := userSearchFixture{read: func(_ context.Context, _ string, start, limit int) (UserSearchRead, error) {
		calls++
		rows := make([]*jira.User, limit)
		for i := range rows {
			rows[i] = searchUser(string(rune(start+i+1000)), false)
		}
		if start == 0 {
			rows[0].Active = jira.NullableOf(true)
		}
		return UserSearchRead{Users: rows}, nil
	}}
	if id, err := ResolveVisibleUser(t.Context(), client, "fixture@example.com"); id != "" || !errors.Is(err, ErrUserResolutionIncomplete) || calls != 20 {
		t.Fatalf("capped search selected one account: %q %v calls=%d", id, err, calls)
	}
	for name, rows := range map[string][]*jira.User{
		"valid":     {searchUser("one", true)},
		"empty":     {},
		"ambiguous": {searchUser("one", true), searchUser("two", true)},
		"malformed": {nil},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := userSearchFixture{read: func(context.Context, string, int, int) (UserSearchRead, error) {
				cancel()
				return UserSearchRead{Users: rows}, nil
			}}
			if id, err := ResolveVisibleUser(ctx, client, "fixture@example.com"); id != "" || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled reply established identity or lost cancellation", id, err)
			}
		})
	}
}

func TestActiveUserSearchPageFailureDoesNotPublishSeenIdentities(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{"earlier": {}}
	rows := []*jira.User{searchUser("next", true), searchUser("earlier", false)}
	if got, err := ActiveUserSearchPage(rows, 50, seen); got != nil || !errors.Is(err, ErrUserSearchResponseInvalid) || !reflect.DeepEqual(seen, map[string]struct{}{"earlier": {}}) {
		t.Fatal("invalid page partially published identities", got, err, seen)
	}
}
