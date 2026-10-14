package envelope

import "encoding/json"

// IssueInputPreparationOutput is local authoring against an explicit snapshot.
// No handling result establishes a permission or successful Jira submission.
type IssueInputPreparationOutput struct {
	Payload     json.RawMessage                   `json:"payload"`
	Fields      []IssueInputPreparedField         `json:"fields"`
	Diagnostics []IssueInputPreparationDiagnostic `json:"diagnostics"`
}

// IssueInputPreparationDiagnostic retains the converter's field and source context.
type IssueInputPreparationDiagnostic struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Field    string `json:"field,omitempty"`
	Path     string `json:"path,omitempty"`
	NodeType string `json:"node_type,omitempty"`
	MarkType string `json:"mark_type,omitempty"`
	Lossy    bool   `json:"lossy"`
}

// IssueInputPreparedField distinguishes changed, shape-checked and unchecked values.
type IssueInputPreparedField struct {
	Field    string `json:"field"`
	Type     string `json:"type,omitempty"`
	Handling string `json:"handling"`
}

func init() {
	Register("issue.input.prepare", IssueInputPreparationOutput{}, map[string]any{
		"description": "Credential-free, all-or-nothing input authoring using an explicitly supplied native metadata snapshot. Native mutation routes retain their existing lossless contract.",
		"properties": map[string]any{
			"payload":     map[string]any{"description": "Prepared native fields/update request. Known scalar identities are explicitly lifted; already-native and unknown values retain their bytes. Extract this object before supplying --json-input."},
			"fields":      map[string]any{"description": "Sorted per-field local handling: encoded, validated or unverified. Validated means known local shape only; clearing, native update values and preserved native ADF remain unverified. Metadata is a snapshot, not permission, requiredness or accepted-value proof."},
			"diagnostics": map[string]any{"description": "Preserved Markdown authoring diagnostics, including non-lossy shape changes. Content-dropping conversion refuses preparation."},
		},
	})
}
