//go:build live

package live

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveMineExpectedSetRejectsFalseAcceptance(t *testing.T) {
	valid := liveIssue{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":{"accountId":"current"}}`)}
	if !mineMatches([]liveIssue{valid}, []string{"KEY-1"}, "current") {
		t.Fatal("valid independently assigned fixture is rejected")
	}
	for _, tc := range []struct {
		name    string
		issues  []liveIssue
		keys    []string
		account string
	}{
		{"missing assigned issue", nil, []string{"KEY-1"}, "current"},
		{"control substituted", []liveIssue{valid}, []string{"KEY-2"}, "current"},
		{"duplicate issue", []liveIssue{valid, valid}, []string{"KEY-1", "KEY-2"}, "current"},
		{"foreign account", []liveIssue{valid}, []string{"KEY-1"}, "other"},
		{"unassigned", []liveIssue{{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":null}`)}}, []string{"KEY-1"}, "current"},
		{"missing account", []liveIssue{{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":{}}`)}}, []string{"KEY-1"}, "current"},
		{"malformed field", []liveIssue{{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":true}`)}}, []string{"KEY-1"}, "current"},
		{"unresolved identity", nil, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if mineMatches(tc.issues, tc.keys, tc.account) {
				t.Fatal("false current-account acceptance")
			}
		})
	}
}

func TestLiveMineIndexWaitAcceptsOnlyMissingOwnedRows(t *testing.T) {
	valid := liveIssue{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":{"accountId":"current"}}`)}
	keys := []string{"KEY-1", "KEY-2", "KEY-3"}
	for _, tc := range []struct {
		name    string
		issues  []liveIssue
		keys    []string
		account string
		want    bool
	}{
		{"empty index", []liveIssue{}, keys, "current", true},
		{"missing or null issue array", nil, keys, "current", false},
		{"missing assigned row", []liveIssue{valid}, keys, "current", true},
		{"complete result is not a wait", []liveIssue{valid}, keys[:1], "current", false},
		{"duplicate partial result", []liveIssue{valid, valid}, keys, "current", false},
		{"control partial result", []liveIssue{valid}, []string{"KEY-2", "KEY-3"}, "current", false},
		{"foreign account", []liveIssue{valid}, keys, "other", false},
		{"unassigned partial result", []liveIssue{{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":null}`)}}, keys, "current", false},
		{"unobserved partial assignment", []liveIssue{{Key: "KEY-1", Fields: json.RawMessage(`{}`)}}, keys, "current", false},
		{"missing identity", nil, keys, "", false},
		{"missing expected set", nil, nil, "current", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mineMissingOnly(tc.issues, tc.keys, tc.account); got != tc.want {
				t.Fatalf("missing-only index wait=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestLiveMineStoredAssignmentRequiresExplicitObservation(t *testing.T) {
	for _, tc := range []struct {
		name, fields     string
		unassigned, want bool
	}{
		{"explicitly unassigned", `{"assignee":null}`, true, true},
		{"missing control observation", `{}`, true, false},
		{"assigned control", `{"assignee":{"accountId":"current"}}`, true, false},
		{"matching assigned account", `{"assignee":{"accountId":"current"}}`, false, true},
		{"different assigned account", `{"assignee":{"accountId":"other"}}`, false, false},
		{"unassigned instead of current", `{"assignee":null}`, false, false},
		{"missing assigned observation", `{}`, false, false},
		{"null account identity", `{"assignee":{"accountId":null}}`, false, false},
		{"missing account identity", `{"assignee":{}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observed struct {
				Assignee jira.Nullable[jira.User] `json:"assignee"`
			}
			if err := json.Unmarshal([]byte(tc.fields), &observed); err != nil {
				t.Fatal(err)
			}
			if got := mineAssignmentMatches(observed.Assignee, "current", tc.unassigned); got != tc.want {
				t.Fatalf("native stored assignment acceptance=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestLiveMineBoundedPageRetainsNativeCursorAndOwnedSet(t *testing.T) {
	valid := liveIssue{Key: "KEY-1", Fields: json.RawMessage(`{"assignee":{"accountId":"current"}}`)}
	for _, tc := range []struct {
		name    string
		change  func(*liveFrame)
		ready   bool
		missing bool
	}{
		{name: "valid cursor", ready: true},
		{name: "valid sparse cursor chain", ready: true, change: func(f *liveFrame) {
			f.Data.SearchPages = append([]json.RawMessage{json.RawMessage(`{"isLast":false,"nextPageToken":"advance"}`)}, f.Data.SearchPages...)
		}},
		{name: "terminal page before resumed page", change: func(f *liveFrame) {
			f.Data.SearchPages = append([]json.RawMessage{json.RawMessage(`{"isLast":true}`)}, f.Data.SearchPages...)
		}},
		{name: "repeated native cursor", change: func(f *liveFrame) { f.Data.SearchPages = append(f.Data.SearchPages, f.Data.SearchPages[0]) }},
		{name: "terminal page carrying cursor", change: func(f *liveFrame) {
			f.Data.SearchPages = []json.RawMessage{json.RawMessage(`{"isLast":true,"nextPageToken":"resume"}`)}
			f.Data.Pagination.IsLast = true
		}},
		{name: "terminal page repeating cursor", change: func(f *liveFrame) {
			f.Data.SearchPages = append(f.Data.SearchPages, json.RawMessage(`{"isLast":true,"nextPageToken":"resume"}`))
			f.Data.Pagination.IsLast = true
		}},
		{name: "terminal missing assigned row", missing: true, change: func(f *liveFrame) {
			f.Data.SearchPages = []json.RawMessage{json.RawMessage(`{"isLast":true}`)}
			f.Data.Pagination.IsLast, f.Data.Pagination.NextPageToken = true, ""
		}},
		{name: "terminal empty assigned index", missing: true, change: func(f *liveFrame) {
			f.Data.SearchPages = []json.RawMessage{json.RawMessage(`{"isLast":true}`)}
			f.Data.Issues = []liveIssue{}
			f.Data.Pagination.IsLast, f.Data.Pagination.NextPageToken, f.Data.Pagination.Returned = true, "", 0
		}},
		{name: "lost native cursor", change: func(f *liveFrame) {
			f.Data.Pagination.IsLast, f.Data.Pagination.NextPageToken = true, ""
		}},
		{name: "changed native cursor", change: func(f *liveFrame) { f.Data.Pagination.NextPageToken = "changed" }},
		{name: "missing native metadata", change: func(f *liveFrame) { f.Data.SearchPages = nil }},
		{name: "malformed native metadata", change: func(f *liveFrame) { f.Data.SearchPages[0] = json.RawMessage(`{`) }},
		{name: "absent native terminal flag", change: func(f *liveFrame) { f.Data.SearchPages[0] = json.RawMessage(`{"nextPageToken":"resume"}`) }},
		{name: "null native terminal flag", change: func(f *liveFrame) {
			f.Data.SearchPages[0] = json.RawMessage(`{"isLast":null,"nextPageToken":"resume"}`)
		}},
		{name: "null native cursor", change: func(f *liveFrame) { f.Data.SearchPages[0] = json.RawMessage(`{"isLast":false,"nextPageToken":null}`) }},
		{name: "wrong account", change: func(f *liveFrame) { f.Data.Issues[0].Fields = json.RawMessage(`{"assignee":{"accountId":"other"}}`) }},
		{name: "foreign issue", change: func(f *liveFrame) { f.Data.Issues[0].Key = "FOREIGN-1" }},
		{name: "unassigned issue", change: func(f *liveFrame) { f.Data.Issues[0].Fields = json.RawMessage(`{"assignee":null}`) }},
		{name: "missing assignment", change: func(f *liveFrame) { f.Data.Issues[0].Fields = json.RawMessage(`{}`) }},
		{name: "failed command", change: func(f *liveFrame) { f.OK = false }},
		{name: "truncated command", change: func(f *liveFrame) { f.Data.Truncated = true }},
		{name: "wrong returned count", change: func(f *liveFrame) { f.Data.Pagination.Returned = 2 }},
		{name: "oversized bounded page", change: func(f *liveFrame) {
			f.Data.Issues = append(f.Data.Issues, liveIssue{Key: "KEY-2", Fields: valid.Fields})
			f.Data.Pagination.Returned = 2
		}},
		{name: "duplicate bounded row", change: func(f *liveFrame) {
			f.Data.Issues = append(f.Data.Issues, valid)
			f.Data.Pagination.Returned = 2
		}},
		{name: "missing or null issue array", change: func(f *liveFrame) { f.Data.Issues = nil }},
		{name: "empty nonterminal page", change: func(f *liveFrame) {
			f.Data.Issues = []liveIssue{}
			f.Data.Pagination.Returned = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := liveFrame{OK: true}
			frame.Data.Issues = []liveIssue{valid}
			frame.Data.SearchPages = []json.RawMessage{json.RawMessage(`{"isLast":false,"nextPageToken":"resume"}`)}
			frame.Data.Pagination.Returned, frame.Data.Pagination.NextPageToken = 1, "resume"
			if tc.change != nil {
				tc.change(&frame)
			}
			ready, missing := mineBoundedPageObservation(frame, []string{"KEY-1", "KEY-2"}, "current")
			if ready != tc.ready || missing != tc.missing {
				t.Fatalf("bounded page acceptance ready=%t missing=%t; want ready=%t missing=%t", ready, missing, tc.ready, tc.missing)
			}
		})
	}
}
