//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func parityWatcherControl(stdout []byte, lane, key, account string, shortcut, preview bool) bool {
	var frame struct {
		Data     json.RawMessage `json:"data"`
		Warnings []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Lossy   *bool  `json:"lossy"`
		} `json:"warnings"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return false
	}
	if lane == "before" && !preview {
		return bytes.Equal(bytes.TrimSpace(frame.Data), []byte("null"))
	}
	var data struct {
		Issue        liveIssue `json:"issue"`
		DryRun       *bool     `json:"dry_run"`
		User         string    `json:"user"`
		UserResolved *bool     `json:"user_resolved"`
		ResolvedID   string    `json:"account_id_resolved"`
		AccountID    string    `json:"account_id"`
		Results      []struct {
			Issue liveIssue       `json:"issue"`
			OK    *bool           `json:"ok"`
			Data  json.RawMessage `json:"data"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"results"`
	}
	if json.Unmarshal(frame.Data, &data) != nil || data.DryRun == nil || *data.DryRun != preview {
		return false
	}
	if lane == "before" {
		user := "accountId:" + account
		if shortcut {
			user = "me"
		}
		if data.Issue.Key != key || data.User != user || data.UserResolved == nil || *data.UserResolved == shortcut {
			return false
		}
		if (shortcut && data.ResolvedID != "") || (!shortcut && data.ResolvedID != account) {
			return false
		}
		var members map[string]json.RawMessage
		if json.Unmarshal(frame.Data, &members) != nil {
			return false
		}
		for _, name := range []string{"watchers", "is_watching", "watch_count", "was_already_watching", "attempted", "account_id"} {
			if _, present := members[name]; present {
				return false
			}
		}
		return true
	}
	expectedAccount := account
	if preview && shortcut {
		warningType := "account_id_cached"
		if data.AccountID == "@me" {
			expectedAccount = "@me"
			warningType = "account_id_unresolved"
		}
		found := false
		for _, warning := range frame.Warnings {
			if warning.Type == warningType && warning.Lossy != nil && !*warning.Lossy && warning.Message != "" && (warningType != "account_id_cached" || strings.Contains(warning.Message, "unverified")) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if data.AccountID != expectedAccount || len(data.Results) != 1 || data.Results[0].Issue.Key != key || data.Results[0].OK == nil || *data.Results[0].OK != preview {
		return false
	}
	row := data.Results[0]
	if !preview {
		return row.Error != nil && row.Error.Code == "read_only_blocked" && (len(row.Data) == 0 || bytes.Equal(bytes.TrimSpace(row.Data), []byte("null")))
	}
	var result map[string]json.RawMessage
	return row.Error == nil && json.Unmarshal(row.Data, &result) == nil && result != nil && len(result) == 0
}

func parityWatcherRequests(lane string, shortcut, noReadback, refused, add bool) []string {
	var expected []string
	if shortcut {
		expected = append(expected, "GET /rest/api/3/myself")
	}
	if !noReadback && lane == "before" {
		expected = append(expected, "GET /rest/api/3/issue/PAIR-1/watchers")
	}
	if refused {
		return expected
	}
	method := "POST"
	if !add {
		method = "DELETE"
	}
	expected = append(expected, method+" /rest/api/3/issue/PAIR-1/watchers")
	if !noReadback {
		expected = append(expected, "GET /rest/api/3/issue/PAIR-1/watchers")
	}
	return expected
}

// Translation derives ownership grammar from the exact actual invocation.
// It cannot replace an unowned target or substitute another account.
func parityWatcherArgs(lane, key, account string, args []string) ([]string, error) {
	if account == "" || len(args) < 3 || args[0] != "issue" {
		return nil, fmt.Errorf("watcher recipe lacks verified identity")
	}
	var verb, user string
	var tail []string
	if lane == "before" && (args[1] == "watch" || args[1] == "unwatch") {
		if args[2] != key {
			return nil, fmt.Errorf("watcher shortcut targets another issue")
		}
		verb = "add"
		if args[1] == "unwatch" {
			verb = "remove"
		}
		user = "@me"
		tail = args[3:]
	} else {
		if (lane != "before" && lane != "after") || len(args) < 6 || args[1] != "watchers" || (args[2] != "add" && args[2] != "remove") || args[3] != key || args[4] != "--user" {
			return nil, fmt.Errorf("unsupported exact watcher recipe")
		}
		verb, user, tail = args[2], args[5], args[6:]
		if lane == "before" {
			switch user {
			case "me":
				user = "@me"
			case "accountId:" + account:
				user = account
			default:
				return nil, fmt.Errorf("legacy watcher recipe selects another account")
			}
		}
	}
	if user != "@me" && user != account {
		return nil, fmt.Errorf("watcher recipe selects another account")
	}
	seen := map[string]bool{}
	for _, flag := range tail {
		if seen[flag] || (flag != "--dry-run" && flag != "--no-readback") {
			return nil, fmt.Errorf("watcher recipe contains an unauthored option")
		}
		seen[flag] = true
	}
	return append([]string{"issue", "watchers", verb, key, "--user", user}, tail...), nil
}

func parityWatcherEffect(before, after envelope.IssueWatchersListData, account string, add bool) bool {
	if before.WatchCount != int64(len(before.Watchers)) || after.WatchCount != int64(len(after.Watchers)) {
		return false
	}
	without := func(state envelope.IssueWatchersListData) ([]envelope.WatcherView, bool, bool) {
		others := make([]envelope.WatcherView, 0, len(state.Watchers))
		found := false
		seen := map[string]bool{}
		for _, user := range state.Watchers {
			if user.AccountID == "" || seen[user.AccountID] {
				return nil, false, false
			}
			seen[user.AccountID] = true
			if user.AccountID == account {
				found = true
			} else {
				others = append(others, user)
			}
		}
		slices.SortFunc(others, func(a, b envelope.WatcherView) int {
			if a.AccountID < b.AccountID {
				return -1
			}
			if a.AccountID > b.AccountID {
				return 1
			}
			return 0
		})
		return others, found, true
	}
	left, wasWatching, validBefore := without(before)
	right, isWatching, validAfter := without(after)
	return account != "" && validBefore && validAfter && before.IsWatching == wasWatching && after.IsWatching == isWatching && isWatching == add && slices.Equal(left, right)
}

func TestParityActualBinaryWatcherWrites(t *testing.T) {
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			watching := false
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if user, password, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
					http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /rest/api/3/myself":
					_, _ = w.Write([]byte(`{"accountId":"owned","displayName":"Fixture","active":true,"accountType":"atlassian"}`))
				case "GET /rest/api/3/issue/PAIR-1/watchers":
					if watching {
						_, _ = w.Write([]byte(`{"watchCount":1,"isWatching":true,"watchers":[{"accountId":"owned","displayName":"Fixture","active":true,"accountType":"atlassian"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"watchCount":0,"isWatching":false,"watchers":[]}`))
					}
				case "POST /rest/api/3/issue/PAIR-1/watchers":
					body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
					var id string
					if err != nil || len(body) > 1024 || json.Unmarshal(body, &id) != nil || id != "owned" || r.URL.RawQuery != "" {
						http.Error(w, "wrong watcher account/body", http.StatusBadRequest)
						return
					}
					watching = true
					w.WriteHeader(http.StatusNoContent)
				case "DELETE /rest/api/3/issue/PAIR-1/watchers":
					body, err := io.ReadAll(io.LimitReader(r.Body, 1))
					query := r.URL.Query()
					if err != nil || len(body) != 0 || len(query) != 1 || len(query["accountId"]) != 1 || query.Get("accountId") != "owned" {
						http.Error(w, "wrong watcher delete identity", http.StatusBadRequest)
						return
					}
					watching = false
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected watcher fixture request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			guard := &liveRun{verified: true, watcherAccountID: "owned", manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "10001"}}}}
			for _, step := range []struct {
				verb, shortcut string
				add            bool
			}{{"add", "watch", true}, {"remove", "unwatch", false}, {"add", "", true}, {"remove", "", false}} {
				args := []string{"issue", "watchers", step.verb, "PAIR-1", "--user", "owned"}
				if lane.name == "before" {
					if step.shortcut != "" {
						args = []string{"issue", step.shortcut, "PAIR-1"}
					} else {
						args[5] = "accountId:owned"
					}
				} else if step.shortcut != "" {
					args[5] = "@me"
				}
				invoke := func(extra []string, flags ...string) (liveFrame, []byte, int, []string) {
					actual := append(slices.Clone(args), flags...)
					canonical, err := parityWatcherArgs(lane.name, "PAIR-1", "owned", actual)
					if err != nil {
						t.Fatal(err)
					}
					if err := guard.checkMutation([]string{"PAIR-1"}, canonical); err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					start := len(requests)
					mu.Unlock()
					frame, stdout, exit := parityFixtureRun(t, lane.name, lane.binary, server.URL, extra, actual...)
					if err := parityValidateFrame(stdout, lane.name, parityExpectedOperation(lane.name, actual, exit), exit); err != nil {
						t.Fatalf("watcher operation envelope changed: %v; %s", err, stdout)
					}
					mu.Lock()
					observed := append([]string(nil), requests[start:]...)
					mu.Unlock()
					return frame, stdout, exit, observed
				}
				frame, stdout, exit, observed := invoke(nil, "--dry-run")
				if exit != 0 || !frame.OK || !parityWatcherControl(stdout, lane.name, "PAIR-1", "owned", step.shortcut != "", true) || len(observed) != 0 {
					t.Fatal("actual watcher preview did not remain local")
				}
				if lane.name == "after" && step.shortcut != "" {
					frame, stdout, exit, observed = invoke([]string{"JIRA_ACCOUNT_ID=owned", "JIRA_ACCOUNT_ID_SITE=" + server.URL, "JIRA_ACCOUNT_ID_EMAIL=fixture@example.invalid", "JIRA_ACCOUNT_ID_CLOUD_ID="}, "--dry-run")
					var cached struct {
						Data struct {
							AccountID string `json:"account_id"`
						} `json:"data"`
					}
					if exit != 0 || !frame.OK || json.Unmarshal(stdout, &cached) != nil || cached.Data.AccountID != "owned" || !parityWatcherControl(stdout, lane.name, "PAIR-1", "owned", true, true) || len(observed) != 0 {
						t.Fatal("bound cached watcher preview lost its unverified provenance or performed a request")
					}
				}
				mu.Lock()
				pre := watching
				mu.Unlock()
				frame, stdout, exit, observed = invoke([]string{"JIRA_READ_ONLY=true"})
				code := "read_only_blocked"
				if lane.name == "before" {
					code = "read_only"
				}
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || !parityWatcherControl(stdout, lane.name, "PAIR-1", "owned", step.shortcut != "", false) || !slices.Equal(observed, parityWatcherRequests(lane.name, step.shortcut != "", false, true, step.add)) {
					t.Fatal("actual watcher read-only refusal changed")
				}
				mu.Lock()
				unchanged := watching == pre
				mu.Unlock()
				if !unchanged {
					t.Fatal("actual watcher refusal changed subscriptions")
				}
				for _, noReadback := range []bool{false, true} {
					var flags []string
					if noReadback {
						flags = []string{"--no-readback"}
					}
					frame, stdout, exit, observed = invoke(nil, flags...)
					want := envelope.IssueWatchersListData{Watchers: []envelope.WatcherView{}, IsWatching: step.add}
					if step.add {
						want.WatchCount = 1
						want.Watchers = []envelope.WatcherView{{AccountID: "owned", DisplayName: "Fixture", Active: true, AccountType: "atlassian"}}
					}
					if exit != 0 || !frame.OK || !parityWatcherWrite(stdout, lane.name, "PAIR-1", "owned", noReadback, want) || !slices.Equal(observed, parityWatcherRequests(lane.name, step.shortcut != "", noReadback, false, step.add)) {
						t.Fatalf("actual watcher request/receipt sequence changed: %v; %s", observed, stdout)
					}
					mu.Lock()
					correct := watching == step.add
					mu.Unlock()
					if !correct {
						t.Fatal("actual watcher state does not match the explicit account intent")
					}
				}
			}
		})
	}
}

func parityWatcherWrite(stdout []byte, lane, key, account string, noReadback bool, want envelope.IssueWatchersListData) bool {
	var frame struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return false
	}
	var data struct {
		Issue        liveIssue `json:"issue"`
		DryRun       *bool     `json:"dry_run"`
		AccountID    string    `json:"account_id"`
		ResolvedID   string    `json:"account_id_resolved"`
		UserResolved *bool     `json:"user_resolved"`
		Attempted    *bool     `json:"attempted"`
		Results      []struct {
			Issue liveIssue       `json:"issue"`
			OK    bool            `json:"ok"`
			Data  json.RawMessage `json:"data"`
		} `json:"results"`
	}
	if json.Unmarshal(frame.Data, &data) != nil || data.DryRun == nil || *data.DryRun {
		return false
	}
	if lane == "before" {
		if data.Issue.Key != key || data.UserResolved == nil || !*data.UserResolved || data.ResolvedID != account {
			return false
		}
		if noReadback {
			var members map[string]json.RawMessage
			if json.Unmarshal(frame.Data, &members) != nil {
				return false
			}
			for _, member := range []string{"watchers", "is_watching", "watch_count", "was_already_watching"} {
				if _, present := members[member]; present {
					return false
				}
			}
			return data.AccountID == account && data.Attempted != nil && *data.Attempted
		}
		return parityWatchersMatch(frame.Data, lane, want)
	}
	if data.AccountID != account || len(data.Results) != 1 || !data.Results[0].OK || data.Results[0].Issue.Key != key {
		return false
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(data.Results[0].Data, &row) != nil || row == nil {
		return false
	}
	if _, failed := row["readback_error"]; failed {
		return false
	}
	readback, present := row["readback"]
	if noReadback {
		return !present && len(row) == 0
	}
	return present && parityWatchersMatch(readback, lane, want)
}

func TestParityWatcherOwnership(t *testing.T) {
	for _, tc := range []struct {
		lane string
		args []string
	}{{"before", []string{"issue", "watch", "PAIR-1"}}, {"before", []string{"issue", "watchers", "remove", "PAIR-1", "--user", "accountId:owned"}}, {"after", []string{"issue", "watchers", "add", "PAIR-1", "--user", "@me"}}} {
		if _, err := parityWatcherArgs(tc.lane, "PAIR-1", "owned", tc.args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"issue", "watch", "PAIR-2"}, {"issue", "watch", "PAIR-1", "PAIR-2"}, {"issue", "watchers", "add", "PAIR-1", "--user", "accountId:foreign"}, {"issue", "watchers", "add", "PAIR-1", "--user", "@me"}} {
		if _, err := parityWatcherArgs("before", "PAIR-1", "owned", args); err == nil {
			t.Fatal("unsafe actual legacy watcher recipe admitted")
		}
	}
	before := envelope.IssueWatchersListData{Watchers: []envelope.WatcherView{{AccountID: "other"}}, WatchCount: 1}
	after := envelope.IssueWatchersListData{Watchers: []envelope.WatcherView{{AccountID: "other"}, {AccountID: "owned"}}, WatchCount: 2, IsWatching: true}
	if !parityWatcherEffect(before, after, "owned", true) || parityWatcherEffect(before, before, "owned", true) {
		t.Fatal("watcher effect admission changed")
	}
	after.Watchers = after.Watchers[1:]
	after.WatchCount = 1
	if parityWatcherEffect(before, after, "owned", true) {
		t.Fatal("another account's subscription disappeared")
	}
}

func TestParityWatcherControlReceipts(t *testing.T) {
	for _, tc := range []struct {
		lane              string
		shortcut, preview bool
		frame             string
	}{
		{"before", true, true, `{"data":{"issue":{"key":"PAIR-1"},"user":"me","user_resolved":false,"dry_run":true}}`},
		{"before", false, true, `{"data":{"issue":{"key":"PAIR-1"},"user":"accountId:owned","user_resolved":true,"account_id_resolved":"owned","dry_run":true}}`},
		{"before", true, false, `{"data":null}`},
		{"after", true, true, `{"data":{"account_id":"@me","dry_run":true,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]},"warnings":[{"type":"account_id_unresolved","message":"Unresolved local preview","lossy":false}]}`},
		{"after", true, true, `{"data":{"account_id":"owned","dry_run":true,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]},"warnings":[{"type":"account_id_cached","message":"Cached preview is unverified","lossy":false}]}`},
		{"after", false, false, `{"data":{"account_id":"owned","dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":false,"error":{"code":"read_only_blocked"}}]}}`},
	} {
		if !parityWatcherControl([]byte(tc.frame), tc.lane, "PAIR-1", "owned", tc.shortcut, tc.preview) {
			t.Fatal("independent control receipt refused")
		}
		for _, sparse := range []string{`{"data":{}}`, `{"data":{"dry_run":true}}`, `{"data":{"dry_run":false}}`} {
			if parityWatcherControl([]byte(sparse), tc.lane, "PAIR-1", "owned", tc.shortcut, tc.preview) {
				t.Fatal("sparse preview/refusal received target credit")
			}
		}
		if (tc.lane != "before" || tc.preview) && parityWatcherControl([]byte(tc.frame), tc.lane, "PAIR-2", "owned", tc.shortcut, tc.preview) {
			t.Fatal("foreign control target admitted")
		}
	}
	if parityWatcherControl([]byte(`{"data":null}`), "after", "PAIR-1", "owned", false, false) {
		t.Fatal("current refusal discarded its keyed/account receipt")
	}
}

func TestLiveParityWatcherWrites(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			for _, step := range []struct {
				id, verb, shortcut string
				add                bool
			}{{"I025", "add", "watch", true}, {"I023", "remove", "unwatch", false}, {"I026", "add", "", true}, {"I028", "remove", "", false}} {
				if !t.Run(step.id, func(t *testing.T) {
					args := []string{"issue", "watchers", step.verb, key, "--user", p.owner.watcherAccountID}
					if lane.Name == "before" {
						if step.shortcut != "" {
							args = []string{"issue", step.shortcut, key}
						} else {
							args[5] = "accountId:" + p.owner.watcherAccountID
						}
					} else if step.shortcut != "" {
						args[5] = "@me"
					}
					invoke := func(extra []string, flags ...string) (liveFrame, []byte, int) {
						actual := append(slices.Clone(args), flags...)
						canonical, err := parityWatcherArgs(lane.Name, key, p.owner.watcherAccountID, actual)
						if err != nil {
							t.Fatal(err)
						}
						if err := p.owner.checkMutation([]string{key}, canonical); err != nil {
							t.Fatal(err)
						}
						return p.invoke(t, lane, extra, actual...)
					}
					before := rawOwnedWatchers(t, p.owner, key)
					if before.WatchCount != int64(len(before.Watchers)) {
						t.Fatal("watcher fixture does not expose complete membership")
					}
					preimageKey := "watchers." + step.id
					if p.evidence.Preimages == nil {
						p.evidence.Preimages = make(map[string]json.RawMessage)
					}
					if lane.Name == "before" {
						encoded, err := json.Marshal(before)
						if err != nil {
							t.Fatal(err)
						}
						p.evidence.Preimages[preimageKey] = encoded
						p.save(t)
					} else {
						var expected envelope.IssueWatchersListData
						if json.Unmarshal(p.evidence.Preimages[preimageKey], &expected) != nil || !sameWatcherState(before, expected) {
							t.Fatal("paired watcher native preimages differ; no dependent write admitted")
						}
					}
					for _, preview := range []bool{true, false} {
						extra := []string{"JIRA_READ_ONLY=true"}
						var flags []string
						if preview {
							extra = nil
							flags = []string{"--dry-run"}
						}
						frame, stdout, exit := invoke(extra, flags...)
						if preview {
							if exit != 0 || !frame.OK || !parityWatcherControl(stdout, lane.Name, key, p.owner.watcherAccountID, step.shortcut != "", true) {
								t.Fatal("watcher preview did not return its local candidate")
							}
						} else {
							code := "read_only_blocked"
							if lane.Name == "before" {
								code = "read_only"
							}
							if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || !parityWatcherControl(stdout, lane.Name, key, p.owner.watcherAccountID, step.shortcut != "", false) {
								t.Fatal("watcher read-only refusal changed")
							}
						}
						if !sameWatcherState(rawOwnedWatchers(t, p.owner, key), before) {
							t.Fatal("watcher preview/refusal changed native subscriptions")
						}
					}
					frame, stdout, exit := invoke(nil)
					stored := rawOwnedWatchers(t, p.owner, key)
					if exit != 0 || !frame.OK || !parityWatcherEffect(before, stored, p.owner.watcherAccountID, step.add) || !parityWatcherWrite(stdout, lane.Name, key, p.owner.watcherAccountID, false, stored) {
						t.Fatal("watcher write or lane readback differs from independent native effect; never replay unknown writes")
					}
					p.check(t, lane, step.id+".native")
					frame, stdout, exit = invoke(nil, "--no-readback")
					repeated := rawOwnedWatchers(t, p.owner, key)
					if exit != 0 || !frame.OK || !sameWatcherState(stored, repeated) || !parityWatcherWrite(stdout, lane.Name, key, p.owner.watcherAccountID, true, repeated) {
						t.Fatal("acknowledged idempotent watcher request changed state or its no-readback contract")
					}
					p.check(t, lane, step.id+".idempotent")
				}) {
					t.Fatal("watcher step failed; stop dependent mutations without replay")
				}
			}
		})
	}
}
