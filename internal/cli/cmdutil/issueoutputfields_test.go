package cmdutil

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func projectionFixtureIssue() *jira.Issue {
	return &jira.Issue{
		Key: new("PROJ-1"),
		Fields: &jira.IssueFields{
			Summary: new("Hello"),
			Status: &jira.Status{
				Name: new("Done"),
				StatusCategory: &jira.StatusCategory{
					Key:       new("done"),
					ColorName: new("green"),
				},
			},
			Priority: &jira.Priority{Name: new("High")},
			Updated:  new("2026-05-03T10:00:00Z"),
		},
	}
}

// A field selector narrows the summary projection — it never renames or
// re-types the keys the default shape publishes. status keeps the flat string
// and drags its category companions along.
func TestIssueOutputFieldsNarrowsSummaryShape(t *testing.T) {
	got := IssueOutputFields([]*jira.Issue{projectionFixtureIssue()}, []string{"summary", "status"})
	want := []map[string]any{{
		"key":             "PROJ-1",
		"summary":         "Hello",
		"status":          "Done",
		"status_category": "done",
		"status_color":    "green",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IssueOutputFields = %#v, want %#v", got, want)
	}
}

// Projecting the default field set reproduces IssueSummary exactly, so the
// default and --fields paths can never disagree on names or types.
func TestIssueOutputFieldsDefaultFieldsMatchIssueSummary(t *testing.T) {
	issue := projectionFixtureIssue()
	got := IssueOutputFields([]*jira.Issue{issue}, jira.DefaultIssueListFields())
	want := []map[string]any{IssueSummary(issue)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IssueOutputFields = %#v, want IssueSummary %#v", got, want)
	}
}

// Requested fields outside the summary set ride top-level under their wire
// ids; a field Jira omitted is an explicit null, not a shape change.
func TestIssueOutputFieldsCarriesNonSummaryFields(t *testing.T) {
	issue := &jira.Issue{
		Key: new("PROJ-2"),
		Fields: &jira.IssueFields{
			Labels: []string{"regression"},
			CustomFields: map[string]json.RawMessage{
				"customfield_10010": json.RawMessage(`"Sprint 5"`),
			},
		},
	}
	got := IssueOutputFields([]*jira.Issue{issue}, []string{"labels", "customfield_10010", "duedate"})
	want := []map[string]any{{
		"key":               "PROJ-2",
		"labels":            []any{"regression"},
		"customfield_10010": "Sprint 5",
		"duedate":           nil,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IssueOutputFields = %#v, want %#v", got, want)
	}
}

// Null-safe like IssueSummary: a nil issue still yields a row with the key
// placeholder so the array shape never varies.
func TestIssueOutputFieldsNilIssue(t *testing.T) {
	got := IssueOutputFields([]*jira.Issue{nil}, []string{"summary", "duedate"})
	want := []map[string]any{{"key": "", "summary": "", "duedate": nil}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IssueOutputFields = %#v, want %#v", got, want)
	}
}
