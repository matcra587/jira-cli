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
