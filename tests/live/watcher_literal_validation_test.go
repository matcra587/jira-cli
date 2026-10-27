//go:build live

package live

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveWatcherLiteralAccountPreflight(t *testing.T) {
	r := newLiveRun(t)
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var user jira.UserDetails
	if json.Unmarshal(self.Response.JSON, &user) != nil || !user.AccountID.Set || user.AccountID.Null || user.AccountID.Value == "" {
		t.Fatal("cannot independently bind current watcher account")
	}
	r.watcherAccountID = user.AccountID.Value
	key := r.create(t, "literal watcher preflight")
	for _, verb := range []string{"add", "remove"} {
		baseline := rawOwnedWatchers(t, r, key)
		for _, remote := range []bool{false, true} {
			args := []string{"issue", "watchers", verb, key, "--user=" + r.watcherAccountID, "--dry-run"}
			if remote {
				args = append(args, "--validate-remote")
			}
			if err := r.checkMutation([]string{key}, args); err != nil {
				t.Fatal(err)
			}
			frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
			var result struct {
				Data envelope.IssueWatchersAddOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || !result.Data.DryRun || result.Data.AccountID != r.watcherAccountID || result.Data.UserQuery != "" || len(result.Data.Results) != 1 {
				t.Fatal("literal preflight lost self account or preview state")
			}
			if !sameWatcherState(rawOwnedWatchers(t, r, key), baseline) {
				t.Fatal("literal account preview changed owned subscriptions")
			}
		}
		args := []string{"issue", "watchers", verb, key, "--user=" + r.watcherAccountID, "--validate-remote"}
		if err := r.checkMutation([]string{key}, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var result struct {
			Data envelope.IssueWatchersAddOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.AccountID != r.watcherAccountID || len(result.Data.Results) != 1 {
			t.Fatal("validated watcher mutation lost self account or acknowledgement")
		}
		row := result.Data.Results[0]
		if row.Issue.Key != key || !row.OK || row.Data == nil || row.Data.Readback == nil || row.Data.ReadbackError != nil {
			t.Fatal("validated watcher write lost acknowledgement or readback")
		}
		stored := rawOwnedWatchers(t, r, key)
		present := slices.ContainsFunc(stored.Watchers.Value, func(w envelope.WatcherView) bool { return w.AccountID == r.watcherAccountID })
		if present != (verb == "add") || stored.IsWatching != jira.NullableOf(verb == "add") {
			t.Fatal("independent watcher state differs from requested self subscription")
		}
	}
}
