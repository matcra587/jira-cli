package core

import "github.com/matcra587/jira-cli/internal/jira"

// IssueCreationInput carries authored standard fields for interactive creation.
// Unset labels/assignee retain Jira defaults; explicit null assignee clears it.
type IssueCreationInput struct {
	ProjectID, IssueTypeID, Summary, Markdown string
	Labels                                    jira.Nullable[[]string]
	Assignee                                  jira.Nullable[string]
}
