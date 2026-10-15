//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// Select only the declared lane layout. Shape probing or fallback would allow
// a missing keyed result to be mistaken for an empty collection.
func parityReadData(stdout []byte, lane, family, key string) (json.RawMessage, error) {
	var frame struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return nil, fmt.Errorf("invalid read frame")
	}
	keyed := lane == "after" && family != "comment"
	if keyed {
		var body struct {
			Results []struct {
				Issue liveIssue       `json:"issue"`
				OK    bool            `json:"ok"`
				Data  json.RawMessage `json:"data"`
			} `json:"results"`
		}
		if json.Unmarshal(frame.Data, &body) != nil || len(body.Results) != 1 || !body.Results[0].OK {
			return nil, fmt.Errorf("missing successful single read result")
		}
		observed := body.Results[0].Issue.Key
		if observed != key {
			return nil, fmt.Errorf("read result targets another issue")
		}
		return body.Results[0].Data, nil
	}
	var body struct {
		Issue liveIssue `json:"issue"`
	}
	if json.Unmarshal(frame.Data, &body) != nil || body.Issue.Key != key {
		return nil, fmt.Errorf("flat read targets another issue")
	}
	return frame.Data, nil
}

func parityWatchersMatch(data json.RawMessage, lane string, want envelope.IssueWatchersListData) bool {
	var got struct {
		Watchers *[]struct {
			AccountID   *string `json:"account_id"`
			DisplayName *string `json:"display_name"`
			Active      *bool   `json:"active"`
			AccountType *string `json:"account_type"`
		} `json:"watchers"`
		Count    *int64 `json:"watch_count"`
		Watching *bool  `json:"is_watching"`
	}
	if json.Unmarshal(data, &got) != nil || got.Watchers == nil || got.Count == nil || got.Watching == nil || *got.Count != want.WatchCount || *got.Watching != want.IsWatching || len(*got.Watchers) != len(want.Watchers) {
		return false
	}
	seen := map[string]bool{}
	for _, user := range *got.Watchers {
		if user.AccountID == nil || *user.AccountID == "" || user.DisplayName == nil || seen[*user.AccountID] {
			return false
		}
		seen[*user.AccountID] = true
		index := slices.IndexFunc(want.Watchers, func(w envelope.WatcherView) bool { return w.AccountID == *user.AccountID })
		if index < 0 || want.Watchers[index].DisplayName != *user.DisplayName {
			return false
		}
		if lane == "after" && (user.Active == nil || user.AccountType == nil || *user.Active != want.Watchers[index].Active || *user.AccountType != want.Watchers[index].AccountType) {
			return false
		}
	}
	return true
}

func parityFreshLinkTypes(stdout []byte, lane string) ([]storedLinkType, error) {
	return parityLinkTypes(stdout, lane, false)
}

func parityLinkTypes(stdout []byte, lane string, wantCached bool) ([]storedLinkType, error) {
	var frame struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return nil, fmt.Errorf("invalid link type frame")
	}
	member, provenance := "types", "cached"
	if lane == "before" {
		member, provenance = "link_types", "from_cache"
	}
	var cached *bool
	var types []storedLinkType
	if json.Unmarshal(frame.Data[provenance], &cached) != nil || cached == nil || *cached != wantCached || json.Unmarshal(frame.Data[member], &types) != nil || types == nil {
		return nil, fmt.Errorf("link type inventory lacks the expected explicit provenance")
	}
	return types, nil
}

func parityEmptyComments(data json.RawMessage) bool {
	var page struct {
		Total   *int `json:"total"`
		StartAt *int `json:"startAt"`
	}
	return json.Unmarshal(data, &page) == nil && page.Total != nil && *page.Total == 0 && page.StartAt != nil && *page.StartAt == 0 && parityEmptyCollection(data, "comments")
}

func parityEmptyCollection(data json.RawMessage, member string) bool {
	var body map[string]json.RawMessage
	var values []json.RawMessage
	return json.Unmarshal(data, &body) == nil && json.Unmarshal(body[member], &values) == nil && values != nil && len(values) == 0
}

// v0.15 published only id/name/hasScreen. The current lane retains the
// complete native response, including destination, fields and future members.
// Declare that legacy loss explicitly instead of dropping it from both lanes.
func parityTransitionRead(data, native json.RawMessage, lane string) bool {
	if lane == "after" {
		return compareNativeTransitions(data, native) == nil
	}
	if lane != "before" {
		return false
	}
	var actual, expected struct {
		Transitions *[]map[string]json.RawMessage `json:"transitions"`
	}
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(native, &expected) != nil || actual.Transitions == nil || expected.Transitions == nil || len(*actual.Transitions) != len(*expected.Transitions) {
		return false
	}
	seen := map[string]bool{}
	for i, wanted := range *expected.Transitions {
		var id, name string
		var screen *bool
		if json.Unmarshal(wanted["id"], &id) != nil || id == "" || seen[id] || json.Unmarshal(wanted["name"], &name) != nil || name == "" || json.Unmarshal(wanted["hasScreen"], &screen) != nil || screen == nil {
			return false
		}
		seen[id] = true
		projection := map[string]json.RawMessage{"id": wanted["id"], "name": wanted["name"], "hasScreen": wanted["hasScreen"]}
		want, wantErr := json.Marshal(projection)
		got, gotErr := json.Marshal((*actual.Transitions)[i])
		if wantErr != nil || gotErr != nil || !nativeMetadataMatches(got, want, nil) {
			return false
		}
	}
	return true
}

func TestParityReadLayouts(t *testing.T) {
	for _, tc := range []struct{ lane, family, frame string }{
		{"before", "comment", `{"data":{"issue":{"key":"PAIR-1"},"comments":[]}}`},
		{"after", "comment", `{"data":{"issue":{"key":"PAIR-1"},"comments":[]}}`},
		{"before", "attachment", `{"data":{"issue":{"key":"PAIR-1"},"attachments":[]}}`},
		{"after", "attachment", `{"data":{"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"attachments":[]}}]}}`},
	} {
		data, err := parityReadData([]byte(tc.frame), tc.lane, tc.family, "PAIR-1")
		member := "attachments"
		if tc.family == "comment" {
			member = "comments"
		}
		if err != nil || !parityEmptyCollection(data, member) {
			t.Fatal("independent declared read layout refused")
		}
		if _, err := parityReadData([]byte(tc.frame), tc.lane, tc.family, "PAIR-2"); err == nil {
			t.Fatal("foreign read result admitted")
		}
	}
	for _, sparse := range []string{`{}`, `null`, `{"attachments":null}`, `{"attachments":[{}]}`} {
		if parityEmptyCollection(json.RawMessage(sparse), "attachments") {
			t.Fatal("sparse or nonempty collection admitted as empty")
		}
	}
}

func TestParityReadPresence(t *testing.T) {
	empty := envelope.IssueWatchersListData{Watchers: []envelope.WatcherView{}}
	for _, lane := range []string{"before", "after"} {
		if !parityWatchersMatch(json.RawMessage(`{"watchers":[],"watch_count":0,"is_watching":false}`), lane, empty) {
			t.Fatal("complete empty watcher state refused")
		}
		for _, sparse := range []string{`{}`, `null`, `{"watchers":[]}`, `{"watchers":null,"watch_count":0,"is_watching":false}`, `{"watchers":[],"watch_count":null,"is_watching":false}`, `{"watchers":[],"watch_count":0,"is_watching":null}`} {
			if parityWatchersMatch(json.RawMessage(sparse), lane, empty) {
				t.Fatal("missing/null watcher state admitted")
			}
		}
	}
	want := envelope.IssueWatchersListData{Watchers: []envelope.WatcherView{{AccountID: "owned", DisplayName: "Fixture", Active: true, AccountType: "atlassian"}}, WatchCount: 1, IsWatching: true}
	legacy := json.RawMessage(`{"watchers":[{"account_id":"owned","display_name":"Fixture"}],"watch_count":1,"is_watching":true}`)
	if !parityWatchersMatch(legacy, "before", want) || parityWatchersMatch(legacy, "after", want) {
		t.Fatal("legacy metadata omission lost its declared lane meaning")
	}
	for _, lane := range []string{"before", "after"} {
		member, source := "types", "cached"
		if lane == "before" {
			member, source = "link_types", "from_cache"
		}
		for _, cached := range []string{"null", "true"} {
			frame := []byte(fmt.Sprintf(`{"data":{%q:[],%q:%s}}`, member, source, cached))
			if _, err := parityFreshLinkTypes(frame, lane); err == nil {
				t.Fatal("missing or stale cache provenance admitted")
			}
		}
		if _, err := parityFreshLinkTypes([]byte(fmt.Sprintf(`{"data":{%q:[]}}`, member)), lane); err == nil {
			t.Fatal("absent cache provenance admitted")
		}
	}
	for _, page := range []string{`{"comments":[]}`, `{"comments":[],"startAt":1,"total":0}`, `{"comments":[],"startAt":0,"total":1}`} {
		if parityEmptyComments(json.RawMessage(page)) {
			t.Fatal("empty comment page mistaken for complete absence")
		}
	}
}

func TestLiveParityIssueReads(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			before := p.native(t, key)
			// Establish empty native resources independently before accepting any
			// empty domain output. Fresh creations are not proof of absence.
			native := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=attachment,issuelinks")
			var issue liveIssue
			if json.Unmarshal(native.Response.JSON, &issue) != nil || issue.Key != key || !parityEmptyCollection(issue.Fields, "attachment") || !parityEmptyCollection(issue.Fields, "issuelinks") {
				t.Fatal("native empty resource preimages unavailable")
			}
			comments := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/comment")
			if !parityEmptyComments(comments.Response.JSON) {
				t.Fatal("native empty comment preimage unavailable")
			}
			for _, tc := range []struct{ id, family, member string }{{"I004", "attachment", "attachments"}, {"I010", "comment", "comments"}, {"I016", "link", "links"}} {
				t.Run(tc.family, func(t *testing.T) {
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "issue", tc.family, "list", key)
					data, err := parityReadData(stdout, lane.Name, tc.family, key)
					if exit != 0 || !frame.OK || err != nil || !parityEmptyCollection(data, tc.member) {
						t.Fatal("domain read differs from independent empty native preimage")
					}
					p.check(t, lane, tc.id+".empty.native")
				})
			}
			t.Run("watchers", func(t *testing.T) {
				want := rawOwnedWatchers(t, p.owner, key)
				frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "issue", "watchers", "list", key)
				data, err := parityReadData(stdout, lane.Name, "watchers", key)
				if exit != 0 || !frame.OK || err != nil || !parityWatchersMatch(data, lane.Name, want) {
					t.Fatal("watcher output differs from independent native state")
				}
				p.check(t, lane, "I027.list.native")
			})
			t.Run("transition_discovery", func(t *testing.T) {
				native := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/transitions", "--query=expand=transitions.fields")
				args := []string{"issue", "transition", key}
				if lane.Name == "after" {
					args = []string{"issue", "transition", "list", key}
				}
				frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
				data, err := parityReadData(stdout, lane.Name, "transition", key)
				if exit != 0 || !frame.OK || err != nil || !parityTransitionRead(data, native.Response.JSON, lane.Name) {
					t.Fatal("relocated workflow discovery differs from its independently observed lane contract")
				}
				p.check(t, lane, "N008.discovery.native")
			})
			t.Run("link_types", func(t *testing.T) {
				native := rawRead(t, p.owner, "api", "get", "/rest/api/3/issueLinkType")
				var want struct {
					Types []storedLinkType `json:"issueLinkTypes"`
				}
				frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "issue", "link", "types", "--refresh")
				got, err := parityFreshLinkTypes(stdout, lane.Name)
				if json.Unmarshal(native.Response.JSON, &want) != nil || want.Types == nil || exit != 0 || !frame.OK || err != nil || !slices.Equal(got, want.Types) {
					t.Fatal("link type output differs from complete independent native inventory")
				}
				p.check(t, lane, "I017.types.native")
			})
			if !nativeMetadataMatches(p.native(t, key).Fields, before.Fields, nil) {
				t.Fatal("read cohort changed the owned issue native fields")
			}
		})
	}
}
