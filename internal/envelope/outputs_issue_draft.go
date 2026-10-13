package envelope

import (
	"encoding/json"

	"github.com/matcra587/jira-cli/internal/core"
)

// IssueEditDraftResult retains native recovery data independently of file paths.
// Description is a local candidate, never acknowledgment of a Jira write.
type IssueEditDraftResult struct {
	Draft       core.IssueEditDraftState       `json:"draft"`
	Source      *core.IssueDescriptionSnapshot `json:"source,omitempty"`
	Current     *core.IssueDescriptionSnapshot `json:"current,omitempty"`
	Description json.RawMessage                `json:"description,omitempty"`
	Changed     *bool                          `json:"changed,omitempty"`
}

// IssueEditDraftOutput describes local preparation or resumed editing.
type IssueEditDraftOutput struct {
	MutationState
	IssueEditDraftResult
	Editor         []string `json:"editor,omitempty"`
	RequestedIssue string   `json:"requested_issue,omitempty"`
}

// IssueEditInspectOutput describes a local candidate or a freshly read merge.
type IssueEditInspectOutput struct {
	IssueEditDraftResult
	Refreshed bool `json:"refreshed"`
}

func init() {
	for _, op := range []string{"issue.edit.draft", "issue.edit.resume"} {
		Register(op, IssueEditDraftOutput{}, map[string]any{
			"description": "Prepare or resume a recoverable local description draft. Original native bytes and saved Markdown are retained after failure. No Jira write or server-enforced condition is supplied.",
		})
	}
	Register("issue.edit.inspect", IssueEditInspectOutput{}, map[string]any{
		"description": "Inspect a local description candidate; optional refresh reads current native state and refuses conflicting edits. Changed compares native candidate bytes. This read neither publishes artifacts nor writes Jira.",
	})
}
