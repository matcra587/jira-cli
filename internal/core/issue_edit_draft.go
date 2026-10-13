package core

import "errors"

var (
	ErrIssueEditDraftInvalid = errors.New("invalid issue editing draft")
	ErrIssueEditDraftChanged = errors.New("issue editing draft changed")
	ErrIssueEditDraftIO      = errors.New("issue editing draft file operation failed")
)

// IssueEditDraftState reports actual local effects and recoverable paths.
// Publication flags record acknowledgment in this invocation; false does not
// prove absence after an attempted operation. Readiness can be observed on reopen
// and is not durability, authentication or a condition on a Jira write.
type IssueEditDraftState struct {
	Directory         string `json:"directory"`
	SourceFile        string `json:"source_file"`
	MarkdownFile      string `json:"markdown_file"`
	ManifestFile      string `json:"manifest_file"`
	DirectoryCreated  bool   `json:"directory_created"`
	SourcePublished   bool   `json:"source_published"`
	MarkdownPublished bool   `json:"markdown_published"`
	Ready             bool   `json:"ready"`
	MemberAttempted   string `json:"member_attempted,omitempty"`
	Durability        string `json:"durability"`
	EditorAttempted   bool   `json:"editor_attempted"`
	EditorSucceeded   bool   `json:"editor_succeeded"`
}
