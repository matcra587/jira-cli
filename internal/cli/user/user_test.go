package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type searchFake struct {
	core.JiraClient
	search func(context.Context, string, int, int) (core.UserSearchRead, error)
}

func (f searchFake) SearchUsers(ctx context.Context, q string, start, limit int) (core.UserSearchRead, error) {
	return f.search(ctx, q, start, limit)
}

func searchDeps(fn func(context.Context, string, int, int) (core.UserSearchRead, error)) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return searchFake{search: fn}, nil }}
}

func match(id string, active bool) *jira.User {
	return &jira.User{AccountID: jira.NullableOf(id), Active: jira.NullableOf(active), DisplayName: jira.NullableOf("Sam " + id)}
}

func TestSearchWalkUsesFetchedOffsetsAndEmptyProof(t *testing.T) {
	t.Parallel()
	var offsets []int
	deps := searchDeps(func(ctx context.Context, query string, start, limit int) (core.UserSearchRead, error) {
		if ctx != t.Context() || query != "sam &?+" || limit != pageSize {
			t.Errorf("request changed context/query/page size: query=%q limit=%d", query, limit)
			return core.UserSearchRead{}, errors.New("unexpected query or page size")
		}
		offsets = append(offsets, start)
		switch start {
		case 0:
			deleted := match("deleted", true)
			deleted.Extra = map[string]json.RawMessage{"deleted": json.RawMessage(`true`)}
			unknown := match("unknown", true)
			unknown.Active = jira.NullableNull[bool]()
			return core.UserSearchRead{Users: []*jira.User{match("active", true), match("inactive", false), deleted, unknown}}, nil
		case 4:
			u := match("second", true)
			u.EmailAddress = jira.NullableNull[string]()
			return core.UserSearchRead{Users: []*jira.User{u}}, nil
		case 5:
			return core.UserSearchRead{Users: []*jira.User{}}, nil
		default:
			t.Errorf("walk skipped/repeated raw offsets: %d", start)
			return core.UserSearchRead{}, errors.New("unexpected continuation offset")
		}
	})
	out, err := runSearch(t.Context(), deps, "sam &?+", 0, pageSize, true)
	if err != nil || !reflect.DeepEqual(offsets, []int{0, 4, 5}) || out.Count != 2 || out.Skipped != 3 || out.Pagination.Fetched != 5 || !out.Pagination.Complete || out.Pagination.NextStart != nil || out.Truncated {
		t.Fatalf("walk lost candidates or invented completion: %+v, %v, offsets %v", out, err, offsets)
	}
	if out.Users[0].EmailAddress.Set || !out.Users[1].EmailAddress.Null {
		t.Fatal("privacy omission/null distinction lost")
	}
}

func TestSearchShortPageOffersProbe(t *testing.T) {
	t.Parallel()
	out, err := runSearch(t.Context(), searchDeps(func(_ context.Context, _ string, start, limit int) (core.UserSearchRead, error) {
		if start != 20 || limit != 10 {
			t.Errorf("wrong offset/limit %d/%d", start, limit)
			return core.UserSearchRead{}, errors.New("unexpected page request")
		}
		return core.UserSearchRead{Users: []*jira.User{match("inactive", false)}}, nil
	}), "sam", 20, 10, false)
	if err != nil || out.Users == nil || out.Count != 0 || out.Skipped != 1 || out.Pagination.Complete || out.Pagination.NextStart == nil || *out.Pagination.NextStart != 21 {
		t.Fatalf("short/excluded page falsely completed: %+v %v", out, err)
	}
}

func TestSearchCeilingBoundsPageAndStops(t *testing.T) {
	t.Parallel()
	calls := 0
	out, err := runSearch(t.Context(), searchDeps(func(_ context.Context, _ string, start, limit int) (core.UserSearchRead, error) {
		calls++
		if start != 995 || limit != 5 {
			t.Errorf("unbounded endpoint request %d/%d", start, limit)
			return core.UserSearchRead{}, errors.New("unbounded endpoint request")
		}
		rows := make([]*jira.User, 5)
		for i := range rows {
			rows[i] = match(fmt.Sprint(i), true)
		}
		return core.UserSearchRead{Users: rows}, nil
	}), "sam", 995, pageSize, true)
	if err != nil || calls != 1 || !out.Truncated || out.TruncatedReason != "server_limit" || out.Pagination.Complete || out.Pagination.NextStart != nil || out.Pagination.MaxResults != 5 {
		t.Fatalf("ceiling unreported or invented completion: %+v %v", out, err)
	}
}

func TestSearchRetainsValidatedPageOnLaterFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		second []*jira.User
		err    error
		code   string
	}{
		{name: "repeat", second: []*jira.User{match("first", true)}, code: cli.CodeUserSearchResponseInvalid},
		{name: "malformed", second: []*jira.User{nil}, code: cli.CodeUserSearchResponseInvalid},
		{name: "canceled", err: context.Canceled, code: "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := runSearch(t.Context(), searchDeps(func(_ context.Context, _ string, start, _ int) (core.UserSearchRead, error) {
				if start == 0 {
					return core.UserSearchRead{Users: []*jira.User{match("first", true)}}, nil
				}
				if start != 1 {
					t.Errorf("wrong continuation %d", start)
					return core.UserSearchRead{}, errors.New("unexpected continuation")
				}
				return core.UserSearchRead{Users: tc.second}, tc.err
			}), "sam", 0, pageSize, true)
			if err == nil || cli.MapError(err).Code != tc.code || out.Count != 1 || out.Users[0].AccountID != "first" || out.Pagination.Fetched != 1 || out.Pagination.Complete || out.Pagination.NextStart == nil || *out.Pagination.NextStart != 1 {
				t.Fatalf("partial failure lost accepted page: %+v %v", out, err)
			}
		})
	}
}

func TestSearchPageRejectsMalformedIdentitiesAtomically(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rows []*jira.User
	}{
		{name: "null array"},
		{name: "null row", rows: []*jira.User{nil}},
		{name: "missing id", rows: []*jira.User{{Active: jira.NullableOf(true)}}},
		{name: "empty id", rows: []*jira.User{match("", true)}},
		{name: "duplicate", rows: []*jira.User{match("same", true), match("same", false)}},
		{name: "deleted type", rows: []*jira.User{{AccountID: jira.NullableOf("id"), Extra: map[string]json.RawMessage{"deleted": json.RawMessage(`"true"`)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			seen := map[string]struct{}{"prior": {}}
			rows, err := projectPage(tc.rows, 50, seen)
			if err == nil || rows != nil || len(seen) != 1 || cli.MapError(err).Exit != cli.ExitServer {
				t.Fatalf("malformed page accepted or partially consumed: %v %v %v", rows, err, seen)
			}
		})
	}
}

func TestSearchBlankQueryBeforeClient(t *testing.T) {
	t.Parallel()
	called := false
	_, err := runSearch(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		called = true
		return nil, errors.New("unexpected constructor")
	}}, " \t\n", 0, 50, false)
	if called || cli.MapError(err).Code != cli.CodeArgValueInvalid {
		t.Fatalf("blank query reached client: %v", err)
	}
}

func TestSearchAllCeilingFromZero(t *testing.T) {
	t.Parallel()
	calls := 0
	out, err := runSearch(t.Context(), searchDeps(func(_ context.Context, _ string, start, limit int) (core.UserSearchRead, error) {
		if start != calls*50 || limit != 50 || start >= 1000 {
			t.Errorf("walk exceeded or skipped the server window: %d/%d", start, limit)
			return core.UserSearchRead{}, errors.New("unexpected server window")
		}
		calls++
		rows := make([]*jira.User, limit)
		for i := range rows {
			rows[i] = match(fmt.Sprint(start+i), true)
		}
		return core.UserSearchRead{Users: rows}, nil
	}), "sam", 0, 50, true)
	if err != nil || calls != 20 || out.Count != 1000 || out.Pagination.Fetched != 1000 || !out.Truncated || out.TruncatedReason != "server_limit" || out.Pagination.Complete || out.Pagination.NextStart != nil {
		t.Fatalf("--all falsely promised directory completeness: %+v %v calls=%d", out.Pagination, err, calls)
	}
}
