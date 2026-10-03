//go:build live

package live

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestSavedQueryNativeComparisonRequiresCompleteOrderedRows(t *testing.T) {
	t.Parallel()
	expected := []jira.IssueBean{
		{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("First")})},
		{Key: jira.NullableOf("ABC-2"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("Second")})},
	}
	actual := []liveIssue{{Key: "ABC-1", Fields: json.RawMessage(`{"summary":"First"}`)}, {Key: "ABC-2", Fields: json.RawMessage(`{"summary":"Second"}`)}}
	if !savedNativeRowsMatch(actual, expected) || !savedNativeRowsMatch([]liveIssue{}, []jira.IssueBean{}) {
		t.Fatal("valid native comparison failed")
	}
	for _, tc := range []struct {
		name string
		rows []liveIssue
	}{
		{"absent rows", nil},
		{"missing row", actual[:1]},
		{"reordered rows", []liveIssue{actual[1], actual[0]}},
		{"changed key", []liveIssue{{Key: "ABC-3", Fields: actual[0].Fields}, actual[1]}},
		{"changed summary", []liveIssue{{Key: "ABC-1", Fields: json.RawMessage(`{"summary":"Changed"}`)}, actual[1]}},
		{"missing summary", []liveIssue{{Key: "ABC-1", Fields: json.RawMessage(`{}`)}, actual[1]}},
		{"null summary", []liveIssue{{Key: "ABC-1", Fields: json.RawMessage(`{"summary":null}`)}, actual[1]}},
		{"invalid fields", []liveIssue{{Key: "ABC-1", Fields: json.RawMessage(`{`)}, actual[1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if savedNativeRowsMatch(tc.rows, expected) {
				t.Fatal("native saved-query comparison accepted changed or incomplete rows")
			}
		})
	}
}
