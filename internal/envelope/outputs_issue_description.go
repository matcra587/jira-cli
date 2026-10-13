package envelope

import "github.com/matcra587/jira-cli/internal/core"

// IssueEditPrepareOutput pairs a bound native backup with its anchored projection.
// Preparation is a read; its snapshot and timestamp never authorize a Jira write.
type IssueEditPrepareOutput struct {
	Issue    IssueRef                      `json:"issue"`
	Snapshot core.IssueDescriptionSnapshot `json:"snapshot"`
	Markdown string                        `json:"markdown"`
	Regions  []ADFEditRegion               `json:"regions"`
}

func init() {
	Register("issue.edit.prepare", IssueEditPrepareOutput{}, map[string]any{
		"description": "Read one bound native issue description and prepare lossless anchored Markdown; no editor, editing artifacts or Jira writes. The snapshot is not a server-enforced write condition.",
		"properties": map[string]any{
			"snapshot": map[string]any{"properties": map[string]any{
				"description":       map[string]any{"type": []string{"object", "null"}, "description": "Native ADF view or explicit null; insignificant JSON whitespace may be compacted. Exact source bytes are retained in description_bytes."},
				"description_bytes": map[string]any{"type": "string", "contentEncoding": "base64", "description": "Exact native description bytes encoded as base64. Keep the complete snapshot with its anchors; reopening uses this preimage and checks it agrees with the native view."},
				"updated":           map[string]any{"description": "Native diagnostic timestamp only; Jira does not enforce it as compare-and-swap."},
			}},
		},
	})
}
