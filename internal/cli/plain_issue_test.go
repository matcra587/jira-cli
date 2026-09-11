package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueViewPlainRendersReadableIssue(t *testing.T) {
	doc, _, err := adf.FromMarkdownLossy("hello **world**")
	if err != nil {
		t.Fatalf("FromMarkdownLossy() error = %v", err)
	}

	var buf bytes.Buffer
	err = WriteCommandPlain(&buf, "issue.view", map[string]any{
		"issue": &jira.Issue{
			Key: new("PROJ-1"),
			Fields: &jira.IssueFields{
				Summary:     new("Readable issue"),
				Status:      &jira.Status{Name: new("In Progress")},
				Priority:    &jira.Priority{Name: new("High")},
				Description: &doc,
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `issue="{`) || strings.Contains(got, `\"fields\"`) {
		t.Fatalf("issue view rendered escape-encoded JSON:\n%s", got)
	}
	for _, want := range []string{"PROJ-1", "Readable issue", "In Progress", "High", "hello world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("issue view output missing %q:\n%s", want, got)
		}
	}
}

func TestIssueViewPlainRendersMultiKeySummary(t *testing.T) {
	type result struct {
		Key   string         `json:"key"`
		OK    bool           `json:"ok"`
		Issue *jira.Issue    `json:"issue,omitempty"`
		Error map[string]any `json:"error,omitempty"`
	}
	data := struct {
		Results   []result `json:"results"`
		Succeeded int      `json:"succeeded"`
		Failed    int      `json:"failed"`
	}{
		Results: []result{
			{
				Key: "PROJ-1",
				OK:  true,
				Issue: &jira.Issue{
					Key: new("PROJ-1"),
					Fields: &jira.IssueFields{
						Summary:  new("Readable issue"),
						Status:   &jira.Status{Name: new("Done")},
						Priority: &jira.Priority{Name: new("Medium")},
					},
				},
			},
			{
				Key:   "PROJ-2",
				OK:    false,
				Error: map[string]any{"code": "jira_not_found"},
			},
		},
		Succeeded: 1,
		Failed:    1,
	}

	var buf bytes.Buffer
	err := WriteCommandPlain(&buf, "issue.view", data, WithPlainThreads(2))
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}
	got := buf.String()
	for _, forbidden := range []string{"value=", "{...}", `\"fields\"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("multi-key issue view fell back to generic output %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"Viewed issues", "succeeded=1/2", "failed=1", "threads=2", "PROJ-1", "Readable issue", "Done", "Medium"} {
		if !strings.Contains(got, want) {
			t.Fatalf("multi-key issue view output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "failed keys:") {
		t.Fatalf("multi-key issue view stdout should omit failed-key diagnostics:\n%s", got)
	}
}

func TestIssueTransitionsPlainRendersReadableTable(t *testing.T) {
	var buf bytes.Buffer
	err := WriteCommandPlain(&buf, "issue.transitions", map[string]any{
		"issue": map[string]any{"key": "PROJ-1"},
		"transitions": []*jira.Transition{
			{ID: new("11"), Name: new("To Do")},
			{ID: new("21"), Name: new("In Progress")},
		},
	})
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `transitions="[{`) || strings.Contains(got, `\"id\"`) {
		t.Fatalf("transitions rendered escape-encoded JSON:\n%s", got)
	}
	for _, want := range []string{"Transitions on PROJ-1", "11", "To Do", "21", "In Progress"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transition output missing %q:\n%s", want, got)
		}
	}
}
