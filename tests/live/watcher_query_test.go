//go:build live

package live

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveWatcherQuery(t *testing.T) {
	r := newLiveRun(t)
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var user jira.User
	if json.Unmarshal(self.Response.JSON, &user) != nil || !user.AccountID.Set || user.AccountID.Null || user.AccountID.Value == "" {
		t.Fatal("cannot independently establish watcher account")
	}
	r.watcherAccountID = user.AccountID.Value
	keys := []string{r.create(t, "watcher query first"), r.create(t, "watcher query second")}
	for _, verb := range []string{"add", "remove"} {
		bindLiveWatcherQuery(t, r)
		baseline := []envelope.IssueWatchersListData{rawOwnedWatchers(t, r, keys[0]), rawOwnedWatchers(t, r, keys[1])}
		for _, remote := range []bool{false, true} {
			args := append([]string{"issue", "watchers", verb}, keys...)
			args = append(args, "--user-query="+r.watcherQuery, "--parallelism=2", "--dry-run")
			if remote {
				args = append(args, "--validate-remote")
			}
			if err := r.checkMutation(keys, args); err != nil {
				t.Fatal(err)
			}
			frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
			var preview struct {
				Data     envelope.IssueWatchersAddOutput `json:"data"`
				Warnings []envelope.Warning              `json:"warnings"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(stdout, &preview) != nil || !preview.Data.DryRun || preview.Data.UserQuery != r.watcherQuery || len(preview.Data.Results) != 2 {
				t.Fatal("query preview lost authored target context")
			}
			if remote {
				if preview.Data.AccountID != r.watcherAccountID || len(preview.Warnings) != 0 {
					t.Fatal("remote query preview failed current-account resolution")
				}
			} else if preview.Data.AccountID != "" || len(preview.Warnings) != 1 || preview.Warnings[0].Type != "account_query_unresolved" {
				t.Fatal("local query preview fabricated identity")
			}
			for i, key := range keys {
				if !sameWatcherState(rawOwnedWatchers(t, r, key), baseline[i]) {
					t.Fatal("query preview changed owned watcher state")
				}
			}
		}
		bindLiveWatcherQuery(t, r)
		args := append([]string{"issue", "watchers", verb}, keys...)
		args = append(args, "--user-query="+r.watcherQuery, "--parallelism=2")
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var result struct {
			Data envelope.IssueWatchersAddOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.AccountID != r.watcherAccountID || result.Data.UserQuery != r.watcherQuery || len(result.Data.Results) != len(keys) {
			t.Fatal("watcher query result lost resolved identity or owned target receipts")
		}
		for i, key := range keys {
			row := result.Data.Results[i]
			if row.Issue.Key != key || !row.OK || row.Data == nil || row.Data.Readback == nil || row.Data.ReadbackError != nil {
				t.Fatal("watcher query lost ordered acknowledgement or readback")
			}
			stored := rawOwnedWatchers(t, r, key)
			found := slices.ContainsFunc(stored.Watchers.Value, func(w envelope.WatcherView) bool { return w.AccountID == r.watcherAccountID })
			if found != (verb == "add") || stored.IsWatching != jira.NullableOf(verb == "add") {
				t.Fatal("independent stored watchers disagree with query action")
			}
		}
	}
}

// Mutation admission uses an independently observed one-row search followed by
// an empty page. A different or ambiguous visible account authorizes no query.
func bindLiveWatcherQuery(t *testing.T, r *liveRun) {
	t.Helper()
	users := liveRawUsers(t, r, r.account, 0, 50)
	if len(users) != 1 || users[0].AccountID.Value != r.watcherAccountID || !users[0].Active.Set || users[0].Active.Null || !users[0].Active.Value {
		t.Fatal("public watcher query does not uniquely select current active account")
	}
	if raw, ok := users[0].Extra["deleted"]; ok {
		var deleted bool
		if json.Unmarshal(raw, &deleted) != nil || deleted {
			t.Fatal("public watcher query selects deleted or malformed identity")
		}
	}
	if len(liveRawUsers(t, r, r.account, 1, 50)) != 0 {
		t.Fatal("public watcher query has more candidates")
	}
	r.watcherQuery = r.account
}
