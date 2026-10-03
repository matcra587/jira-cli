//go:build live

package live

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveUserSearch compares account discovery with independent raw endpoint
// reads. The query comes from the authenticated account's visible display name;
// no account or issue is created, changed or selected for assignment.
func TestLiveUserSearch(t *testing.T) {
	r := newLiveRun(t)
	myself := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var self jira.User
	if json.Unmarshal(myself.Response.JSON, &self) != nil || self.AccountID.Value == "" || self.DisplayName.Value == "" {
		t.Fatal("cannot establish a visible query and current account identity")
	}
	query := self.DisplayName.Value
	var expected []envelope.UserSearchMatch
	fetched, skipped := 0, 0
	complete := false
	for fetched < 1000 {
		rows := liveRawUsers(t, r, query, fetched, min(50, 1000-fetched))
		if len(rows) == 0 {
			complete = true
			break
		}
		for _, user := range rows {
			var deleted bool
			if raw, ok := user.Extra["deleted"]; ok && string(raw) != "null" {
				if json.Unmarshal(raw, &deleted) != nil {
					t.Fatal("raw user has an invalid deleted state")
				}
			}
			if !user.Active.Set || user.Active.Null || !user.Active.Value || deleted {
				skipped++
				continue
			}
			expected = append(expected, envelope.UserSearchMatch{AccountID: user.AccountID.Value, DisplayName: user.DisplayName.Value, EmailAddress: user.EmailAddress})
		}
		fetched += len(rows)
	}
	if expected == nil {
		t.Fatal("live permissions yielded no usable identity; user-search acceptance is unproven")
	}
	foundSelf := false
	for _, user := range expected {
		foundSelf = foundSelf || user.AccountID == self.AccountID.Value
	}
	if !foundSelf {
		t.Fatal("visible self-query did not resolve the authenticated account")
	}
	out := liveSearchUsers(t, r, query, "--all")
	if !reflect.DeepEqual(out.Users, expected) || out.Count != len(expected) || out.Skipped != skipped || out.Pagination.Fetched != fetched || out.Pagination.Complete != complete || out.Truncated != !complete {
		t.Fatal("domain search differs from independent raw pages or completion observations")
	}
	for _, start := range []int{0, 1} {
		raw := liveRawUsers(t, r, query, start, 1)
		page := liveSearchUsers(t, r, query, "--start="+strconv.Itoa(start), "--limit=1")
		if page.Pagination.StartAt != start || page.Pagination.MaxResults != 1 || page.Pagination.Fetched != len(raw) || page.Pagination.Complete != (len(raw) == 0) {
			t.Fatal("bounded user page invented completion or changed its offset")
		}
		if len(raw) > 0 {
			if page.Pagination.NextStart == nil || *page.Pagination.NextStart != start+len(raw) {
				t.Fatal("user continuation lost fetched offset")
			}
			if raw[0].Active.Set && !raw[0].Active.Null && raw[0].Active.Value && len(raw[0].Extra["deleted"]) == 0 {
				if len(page.Users) != 1 || page.Users[0].AccountID != raw[0].AccountID.Value || !reflect.DeepEqual(page.Users[0].EmailAddress, raw[0].EmailAddress) {
					t.Fatal("bounded discovery changed identity or email visibility")
				}
			}
		}
	}
}

func liveRawUsers(t *testing.T, r *liveRun, query string, start, limit int) []*jira.User {
	t.Helper()
	response := rawRead(t, r, "api", "get", "/rest/api/3/user/search", "--query=query="+query, "--query=startAt="+strconv.Itoa(start), "--query=maxResults="+strconv.Itoa(limit))
	var users []*jira.User
	if json.Unmarshal(response.Response.JSON, &users) != nil || users == nil || len(users) > limit {
		t.Fatal("raw user search has no bounded array")
	}
	for _, user := range users {
		if user == nil || user.AccountID.Value == "" {
			t.Fatal("raw user has no account identity")
		}
	}
	return users
}

func liveSearchUsers(t *testing.T, r *liveRun, query string, flags ...string) envelope.UserSearchOutput {
	t.Helper()
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append([]string{"user", "search", query}, flags...)...)
	var result struct {
		Data envelope.UserSearchOutput `json:"data"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Meta.Command != "user.search" || result.Data.Users == nil || result.Data.Query != query {
		t.Fatal("live account discovery failed; inspect local receipts")
	}
	return result.Data
}
