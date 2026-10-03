//go:build live

package live

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Only the authenticated current account may change its subscription on
// active issues created by this run. Every target and option is explicit.
func (r *liveRun) checkWatcherMutation(keys, args []string) error {
	if !r.verified || r.watcherAccountID == "" || len(keys) == 0 || len(args) < 4 || args[0] != "issue" || args[1] != "watchers" || (args[2] != "add" && args[2] != "remove") {
		return errors.New("watcher mutation requires owned targets and a verified current account")
	}
	start := 3
	seenKeys := map[string]bool{}
	for start < len(args) && !strings.HasPrefix(args[start], "--") {
		key := args[start]
		if seenKeys[key] || !liveKey.MatchString(key) || r.ownedLiveIssue(key) == nil {
			return errors.New("watcher mutation targets an inactive, repeated or foreign issue")
		}
		seenKeys[key] = true
		start++
	}
	if !slices.Equal(args[3:start], keys) {
		return errors.New("watcher targets differ from their ownership declaration")
	}
	seen := map[string]bool{}
	for i := start; i < len(args); i++ {
		flag, value, hasValue := strings.Cut(args[i], "=")
		if seen[flag] {
			return errors.New("watcher mutation repeats an option")
		}
		seen[flag] = true
		switch flag {
		case "--user":
			if !hasValue {
				i++
				if i == len(args) {
					return errors.New("watcher mutation omits its account value")
				}
				value = args[i]
			}
			if value != "@me" && value != r.watcherAccountID {
				return errors.New("watcher mutation cannot change another account's subscription")
			}
		case "--parallelism":
			if !hasValue || (value != "1" && value != "2") {
				return errors.New("watcher mutation changes its authored worker bound")
			}
		case "--dry-run", "--validate-remote", "--no-readback":
			if hasValue {
				return errors.New("watcher mutation changes an authored boolean option")
			}
		default:
			return errors.New("watcher mutation contains an unauthored option")
		}
	}
	if !seen["--user"] {
		return errors.New("watcher mutation must name the current account")
	}
	return nil
}

func rawOwnedWatchers(t *testing.T, r *liveRun, key string) envelope.IssueWatchersListData {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("watcher comparison requires an active owned issue")
	}
	response := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/watchers")
	var wire jira.Watchers
	if json.Unmarshal(response.Response.JSON, &wire) != nil || !wire.WatchCount.Set || wire.WatchCount.Null || !wire.IsWatching.Set || wire.IsWatching.Null || !wire.Watchers.Set || wire.Watchers.Null {
		t.Fatal("raw watcher read cannot establish current-account state, count and visible identities")
	}
	views := make([]envelope.WatcherView, 0, len(wire.Watchers.Value))
	seen := map[string]bool{}
	for _, watcher := range wire.Watchers.Value {
		if !watcher.AccountID.Set || watcher.AccountID.Null || watcher.AccountID.Value == "" || seen[watcher.AccountID.Value] {
			t.Fatal("raw watcher read has no stable account identity")
		}
		seen[watcher.AccountID.Value] = true
		views = append(views, envelope.WatcherView{AccountID: watcher.AccountID.Value, DisplayName: watcher.DisplayName.Value, Active: watcher.Active.Value, AccountType: watcher.AccountType.Value})
	}
	return envelope.IssueWatchersListData{Watchers: views, WatchCount: wire.WatchCount.Value, IsWatching: wire.IsWatching.Value}
}

func sameWatcherState(a, b envelope.IssueWatchersListData) bool {
	if a.WatchCount != b.WatchCount || a.IsWatching != b.IsWatching || len(a.Watchers) != len(b.Watchers) {
		return false
	}
	left, right := slices.Clone(a.Watchers), slices.Clone(b.Watchers)
	slices.SortFunc(left, func(a, b envelope.WatcherView) int { return strings.Compare(a.AccountID, b.AccountID) })
	slices.SortFunc(right, func(a, b envelope.WatcherView) int { return strings.Compare(a.AccountID, b.AccountID) })
	return slices.Equal(left, right)
}

func TestLiveWatchers(t *testing.T) {
	r := newLiveRun(t)
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var user jira.User
	if json.Unmarshal(self.Response.JSON, &user) != nil || !user.AccountID.Set || user.AccountID.Null || user.AccountID.Value == "" {
		t.Fatal("cannot bind watcher mutations to the authenticated current account")
	}
	r.watcherAccountID = user.AccountID.Value
	keys := []string{r.create(t, "watcher lifecycle first"), r.create(t, "watcher lifecycle second")}
	for _, key := range keys {
		baseline := rawOwnedWatchers(t, r, key)
		for _, verb := range []string{"add", "remove"} {
			for _, options := range []struct{ preview, remote bool }{{true, false}, {true, true}, {false, false}} {
				args := []string{"issue", "watchers", verb, key, "--user=@me"}
				extra := []string{"JIRA_READ_ONLY=true"}
				if options.preview {
					args = append(args, "--dry-run")
					extra = nil
				}
				if options.remote {
					args = append(args, "--validate-remote")
				}
				if err := r.checkMutation([]string{key}, args); err != nil {
					t.Fatal(err)
				}
				frame, stdout, exit := r.invoke(t, t.Context(), extra, args...)
				var out struct {
					Data struct {
						DryRun    bool                                                    `json:"dry_run"`
						AccountID string                                                  `json:"account_id"`
						Results   []envelope.KeyedResult[envelope.IssueWatchersWriteData] `json:"results"`
					} `json:"data"`
				}
				if json.Unmarshal(stdout, &out) != nil || len(out.Data.Results) != 1 || out.Data.Results[0].Issue.Key != key {
					t.Fatal("watcher preview/refusal lost its explicit target receipt")
				}
				if options.preview {
					row := out.Data.Results[0]
					expectedAccount := "@me"
					if options.remote {
						expectedAccount = r.watcherAccountID
					}
					if exit != 0 || !frame.OK || !out.Data.DryRun || out.Data.AccountID != expectedAccount || !row.OK || row.Data == nil || row.Data.Readback != nil || row.Data.ReadbackError != nil {
						t.Fatal("local watcher preview fabricated resolution, state or writes")
					}
				} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" || out.Data.AccountID != r.watcherAccountID || out.Data.Results[0].OK {
					t.Fatal("watcher mutation bypassed read-only policy or lost its resolved account")
				}
				if !sameWatcherState(rawOwnedWatchers(t, r, key), baseline) {
					t.Fatal("watcher preview/refusal changed stored subscriptions")
				}
			}
		}
	}
	previous := map[string]envelope.IssueWatchersListData{}
	for _, step := range []struct {
		verb       string
		account    string
		noReadback bool
		watching   bool
	}{
		{"add", "@me", false, true},
		{"add", user.AccountID.Value, true, true},
		{"remove", "@me", false, false},
		{"remove", user.AccountID.Value, true, false},
	} {
		args := append([]string{"issue", "watchers", step.verb}, keys...)
		args = append(args, "--user="+step.account, "--parallelism=2")
		if step.noReadback {
			args = append(args, "--no-readback")
		}
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var receipt struct {
			Data json.RawMessage `json:"data"`
		}
		var out struct {
			AccountID string                                                  `json:"account_id"`
			Results   []envelope.KeyedResult[envelope.IssueWatchersWriteData] `json:"results"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &receipt) != nil || json.Unmarshal(receipt.Data, &out) != nil || out.AccountID != user.AccountID.Value || len(out.Results) != len(keys) {
			t.Fatal("watcher fan-out lost resolved account or ordered outcomes")
		}
		for i, key := range keys {
			row := out.Results[i]
			if row.Issue.Key != key || !row.OK || row.Data == nil || row.Data.ReadbackError != nil || (row.Data.Readback == nil) != step.noReadback {
				t.Fatal("watcher fan-out lost acknowledgement or changed readback policy")
			}
			stored := rawOwnedWatchers(t, r, key)
			found := false
			for _, watcher := range stored.Watchers {
				found = found || watcher.AccountID == user.AccountID.Value
			}
			if stored.IsWatching != step.watching || found != step.watching {
				t.Fatal("acknowledged watcher write does not match independently stored state")
			}
			if step.noReadback && !sameWatcherState(previous[key], stored) {
				t.Fatal("repeated watcher operation changed the stored subscription set or count")
			}
			previous[key] = stored
			if row.Data.Readback != nil && !sameWatcherState(*row.Data.Readback, stored) {
				t.Fatal("watcher write readback differs from independent raw storage")
			}
		}
		read, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append([]string{"issue", "watchers", "list"}, keys...)...)
		var listing struct {
			Data json.RawMessage `json:"data"`
		}
		var list envelope.IssueWatchersListOutput
		if exit != 0 || !read.OK || json.Unmarshal(stdout, &listing) != nil || json.Unmarshal(listing.Data, &list) != nil || len(list.Results) != len(keys) {
			t.Fatal("watcher list lost the requested domain snapshot")
		}
		for i, key := range keys {
			row := list.Results[i]
			if row.Issue.Key != key || !row.OK || row.Data == nil || !sameWatcherState(*row.Data, rawOwnedWatchers(t, r, key)) {
				t.Fatal("watcher list differs from independent raw identities/count/state")
			}
		}
	}
}
