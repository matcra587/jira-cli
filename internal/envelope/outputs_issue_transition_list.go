package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// IssueTransitionListOutput retains one native workflow document per requested
// issue. Screen field metadata and unmodeled response properties remain intact.
type IssueTransitionListOutput struct {
	KeyedResults[jira.Transitions]
}

func init() {
	Register("issue.transition.list", IssueTransitionListOutput{}, map[string]any{
		"description": "Available workflow transitions and returned screen field metadata, in requested issue order.",
		"properties": map[string]any{
			"results": map[string]any{"description": "Each data object is the complete native transitions response, including expansion metadata and extensions. Failed or interrupted reads retain joined per-key outcomes."},
		},
	})
}
