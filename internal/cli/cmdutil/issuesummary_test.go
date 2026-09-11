package cmdutil

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueSummaryAlwaysIncludesSpecKeys(t *testing.T) {
	got := IssueSummary(&jira.Issue{
		Key: new("PROJ-1"),
		Fields: &jira.IssueFields{
			Summary: new("Hello"),
			Status:  &jira.Status{Name: new("In Progress")},
			Updated: new("2026-05-03T10:00:00Z"),
		},
	})
	for _, key := range []string{"key", "summary", "status", "status_category", "updated", "assignee", "priority"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("issueSummary missing %q: %#v", key, got)
		}
	}
}

// status_category carries the workflow category key when the status object
// includes one, and stays an empty string otherwise (stable shape).
func TestIssueSummaryStatusCategory(t *testing.T) {
	withCat := IssueSummary(&jira.Issue{Fields: &jira.IssueFields{
		Status: &jira.Status{
			Name: new("Done"),
			StatusCategory: &jira.StatusCategory{
				Key:       new("done"),
				ColorName: new("green"),
			},
		},
	}})
	if withCat["status_category"] != "done" {
		t.Fatalf("status_category = %#v, want \"done\"", withCat["status_category"])
	}
	// status_color carries Jira's own color designation when present, used to
	// match the badge to the Jira UI.
	if withCat["status_color"] != "green" {
		t.Fatalf("status_color = %#v, want \"green\"", withCat["status_color"])
	}
	withoutCat := IssueSummary(&jira.Issue{Fields: &jira.IssueFields{
		Status: &jira.Status{Name: new("Open")},
	}})
	if withoutCat["status_category"] != "" {
		t.Fatalf("status_category = %#v, want empty string", withoutCat["status_category"])
	}
	// Absent colorName leaves status_color unset rather than empty, keeping the
	// envelope free of a meaningless field.
	if _, ok := withoutCat["status_color"]; ok {
		t.Fatalf("status_color = %#v, want absent", withoutCat["status_color"])
	}
}

func TestIssueSummaryAssigneeIsNilWhenUnassigned(t *testing.T) {
	got := IssueSummary(&jira.Issue{
		Key:    new("PROJ-1"),
		Fields: &jira.IssueFields{Summary: new("Hi")},
	})
	if got["assignee"] != nil {
		t.Fatalf("assignee = %#v, want nil", got["assignee"])
	}
}

func TestIssueSummaryAssigneeUsesOnlyAccountIDAndDisplayName(t *testing.T) {
	got := IssueSummary(&jira.Issue{
		Key: new("PROJ-1"),
		Fields: &jira.IssueFields{
			Assignee: &jira.User{
				AccountID:    new("acc-1"),
				DisplayName:  new("Riley Chen"),
				EmailAddress: new("riley@example.com"),
			},
		},
	})
	assignee, ok := got["assignee"].(map[string]any)
	if !ok {
		t.Fatalf("assignee = %#v, want map[string]any", got["assignee"])
	}
	if assignee["account_id"] != "acc-1" {
		t.Fatalf("account_id = %#v", assignee["account_id"])
	}
	if assignee["display_name"] != "Riley Chen" {
		t.Fatalf("display_name = %#v", assignee["display_name"])
	}
	for _, leaked := range []string{"email_address", "emailAddress", "accountId", "displayName"} {
		if _, ok := assignee[leaked]; ok {
			t.Fatalf("assignee leaked %q: %#v", leaked, assignee)
		}
	}
}

func TestIssueSummaryPriorityIsNilWhenAbsent(t *testing.T) {
	got := IssueSummary(&jira.Issue{Key: new("PROJ-1"), Fields: &jira.IssueFields{}})
	if got["priority"] != nil {
		t.Fatalf("priority = %#v, want nil", got["priority"])
	}
}

func TestIssueSummaryPriorityIsStringWhenPresent(t *testing.T) {
	got := IssueSummary(&jira.Issue{
		Key:    new("PROJ-1"),
		Fields: &jira.IssueFields{Priority: &jira.Priority{Name: new("High")}},
	})
	if got["priority"] != "High" {
		t.Fatalf("priority = %#v, want \"High\"", got["priority"])
	}
}
