package unit

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestPointerFieldsAndPaginationMetadata(t *testing.T) {
	summary := "zero is meaningful"
	issue := jira.Issue{Key: new("PROJ-1"), Fields: &jira.IssueFields{Summary: &summary}}
	if issue.Key == nil || *issue.Key != "PROJ-1" {
		t.Fatalf("issue key pointer not preserved")
	}
	opts := jira.ListOptions{StartAt: 10, MaxResults: 25}
	if got := opts.QueryValues().Get("startAt"); got != "10" {
		t.Fatalf("startAt = %q", got)
	}
	resp := jira.Response{StartAt: 10, MaxResults: 25, Total: 100, IsLast: false}
	if resp.NextCursor() == "" {
		t.Fatalf("NextCursor() empty")
	}
}

func TestCustomFieldRawPreserved(t *testing.T) {
	var issue jira.Issue
	if err := json.Unmarshal([]byte(`{"key":"PROJ-1","fields":{"customfield_10001":{"value":"x"}}}`), &issue); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if issue.Fields == nil || len(issue.Fields.CustomFields) != 1 {
		t.Fatalf("custom fields = %+v", issue.Fields)
	}
	if string(issue.Fields.CustomFields["customfield_10001"]) != `{"value":"x"}` {
		t.Fatalf("custom field raw = %s", issue.Fields.CustomFields["customfield_10001"])
	}
}
