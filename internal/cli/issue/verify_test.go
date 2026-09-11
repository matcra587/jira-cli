package issue

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

// fetchedIssue builds the re-fetched issue the diverging-server tests share:
// labels [gamma], no parent, assignee acc-1, priority High, one component,
// and a select custom field with option id 10001.
func fetchedIssue() *jira.Issue {
	return &jira.Issue{
		Key: new("PROJ-1"),
		Fields: &jira.IssueFields{
			Summary:    new("A summary"),
			Labels:     []string{"gamma"},
			Assignee:   &jira.User{AccountID: new("acc-1")},
			Priority:   &jira.Priority{Name: new("High")},
			IssueType:  &jira.IssueType{Name: new("Task")},
			Components: []jira.Component{{Name: new("core")}},
			CustomFields: map[string]json.RawMessage{
				"customfield_10010": json.RawMessage(`{"id":"10001","value":"Blue","self":"https://x"}`),
			},
		},
	}
}

// TestVerifyAppliedFieldsSurfacesDrops is the ticket's regression scenario:
// requested labels [alpha beta] where the server kept [gamma], and a
// requested parent the server nulled, must surface as drops — never as a
// clean success.
func TestVerifyAppliedFieldsSurfacesDrops(t *testing.T) {
	requested := map[string]any{
		"labels": []string{"alpha", "beta"},
		"parent": map[string]any{"key": "PROJ-100"},
	}
	result := verifyAppliedFields(requested, fetchedIssue())

	if len(result.Dropped) != 2 {
		t.Fatalf("dropped = %+v, want exactly labels and parent", result.Dropped)
	}
	byField := map[string]fieldDrop{}
	for _, d := range result.Dropped {
		byField[d.Field] = d
	}
	labels, ok := byField["labels"]
	if !ok {
		t.Fatalf("labels drop missing: %+v", result.Dropped)
	}
	if !reflect.DeepEqual(labels.Requested, []string{"alpha", "beta"}) || !reflect.DeepEqual(labels.Applied, []string{"gamma"}) {
		t.Errorf("labels drop = %+v, want requested [alpha beta] applied [gamma]", labels)
	}
	parent, ok := byField["parent"]
	if !ok {
		t.Fatalf("parent drop missing: %+v", result.Dropped)
	}
	if parent.Requested != "PROJ-100" || parent.Applied != nil {
		t.Errorf("parent drop = %+v, want requested PROJ-100 applied <nil>", parent)
	}
	if len(result.Unverified) != 0 {
		t.Errorf("unverified = %v, want none", result.Unverified)
	}
}

// TestVerifyAppliedFieldsMatchingResultHasZeroDrops is the false-positive
// guard: every requested field matches by its identity — including a label
// subset beside server-added extras, case-insensitive parent/priority, an
// option custom field the server echoes with extra sub-fields, and a
// component subset — so the diff must report NOTHING dropped.
func TestVerifyAppliedFieldsMatchingResultHasZeroDrops(t *testing.T) {
	issue := fetchedIssue()
	issue.Fields.Labels = []string{"alpha", "beta", "automation-added"}
	issue.Fields.Parent = &jira.Issue{Key: new("PROJ-100")}

	requested := map[string]any{
		"summary":           "A summary",
		"labels":            []string{"alpha", "beta"},
		"parent":            map[string]any{"key": "proj-100"},
		"assignee":          map[string]any{"accountId": "acc-1"},
		"priority":          map[string]any{"name": "high"},
		"issuetype":         map[string]any{"name": "Task"},
		"components":        []any{map[string]any{"name": "CORE"}},
		"project":           map[string]any{"key": "PROJ"},
		"customfield_10010": map[string]any{"id": "10001"},
	}
	result := verifyAppliedFields(requested, issue)

	if len(result.Dropped) != 0 {
		t.Fatalf("false positive: dropped = %+v, want none", result.Dropped)
	}
	if len(result.Unverified) != 0 {
		t.Errorf("unverified = %v, want none", result.Unverified)
	}
	if got := result.Applied["labels"]; !reflect.DeepEqual(got, []string{"alpha", "beta", "automation-added"}) {
		t.Errorf("applied labels = %v, want the server's full list echoed", got)
	}
}

// TestVerifyAppliedFieldsUnobservableFieldsAreUnverified pins the safety rule
// for fields the typed issue model cannot carry (duedate, environment): they
// are reported unverified, never dropped — absence from the fetch proves
// nothing.
func TestVerifyAppliedFieldsUnobservableFieldsAreUnverified(t *testing.T) {
	requested := map[string]any{
		"duedate":     "2026-08-01",
		"environment": "staging",
	}
	result := verifyAppliedFields(requested, fetchedIssue())

	if len(result.Dropped) != 0 {
		t.Fatalf("unobservable fields reported as drops: %+v", result.Dropped)
	}
	if !reflect.DeepEqual(result.Unverified, []string{"duedate", "environment"}) {
		t.Errorf("unverified = %v, want [duedate environment]", result.Unverified)
	}
}

// TestVerifyAppliedFieldsCustomFieldIdentity covers the custom-field
// comparators: dropped option (requested id absent), matching scalar echoed
// as float64, array containment, and an explicit clear matching an absent
// value.
func TestVerifyAppliedFieldsCustomFieldIdentity(t *testing.T) {
	issue := fetchedIssue()
	issue.Fields.CustomFields["customfield_20020"] = json.RawMessage(`3`)
	issue.Fields.CustomFields["customfield_30030"] = json.RawMessage(`[{"value":"a","self":"x"},{"value":"b","self":"y"}]`)

	requested := map[string]any{
		"customfield_10010": map[string]any{"id": "99999"},                                     // applied option is 10001 -> drop
		"customfield_20020": float64(3),                                                        // scalar match
		"customfield_30030": []any{map[string]any{"value": "a"}, map[string]any{"value": "b"}}, // array containment
		"customfield_40040": nil,                                                               // explicit clear, absent on server -> match
		"customfield_50050": "anything",                                                        // absent on server -> drop
	}
	result := verifyAppliedFields(requested, issue)

	droppedFields := make([]string, 0, len(result.Dropped))
	for _, d := range result.Dropped {
		droppedFields = append(droppedFields, d.Field)
	}
	want := []string{"customfield_10010", "customfield_50050"}
	if !reflect.DeepEqual(droppedFields, want) {
		t.Fatalf("dropped fields = %v, want %v\nfull: %+v", droppedFields, want, result.Dropped)
	}
}

// TestVerifyAppliedFieldsUnassignEdit pins the explicit-unassign contract on
// edit: a null assignee request matches an unassigned issue and flags a
// surviving assignee.
func TestVerifyAppliedFieldsUnassignEdit(t *testing.T) {
	cleared := fetchedIssue()
	cleared.Fields.Assignee = nil
	if result := verifyAppliedFields(map[string]any{"assignee": nil}, cleared); len(result.Dropped) != 0 {
		t.Fatalf("unassign matching an unassigned issue reported drops: %+v", result.Dropped)
	}

	kept := fetchedIssue() // assignee acc-1 survived the unassign
	result := verifyAppliedFields(map[string]any{"assignee": nil}, kept)
	if len(result.Dropped) != 1 || result.Dropped[0].Field != "assignee" || result.Dropped[0].Applied != "acc-1" {
		t.Fatalf("surviving assignee not reported: %+v", result.Dropped)
	}
}

// TestVerifyAppliedFieldsSummaryMismatch pins the plain string comparator.
func TestVerifyAppliedFieldsSummaryMismatch(t *testing.T) {
	result := verifyAppliedFields(map[string]any{"summary": "Requested title"}, fetchedIssue())
	if len(result.Dropped) != 1 || result.Dropped[0].Field != "summary" || result.Dropped[0].Applied != "A summary" {
		t.Fatalf("summary mismatch not reported: %+v", result.Dropped)
	}
}
