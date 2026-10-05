package envelope

import "encoding/json"

// IssueCreateInputOutput is live project/type-specific input discovery, not a
// mutation preview. Complete describes metadata collection, not submit readiness.
// Null values in Payload are placeholders; callers fill wanted values and remove
// unwanted optional fields or update operations before using --json-input.
type IssueCreateInputOutput struct {
	Complete bool `json:"complete"`
	// Project and IssueType are whole native records. Project's actual ID seeds
	// the request even when the caller selected it by key.
	Project   json.RawMessage `json:"project,omitempty"`
	IssueType json.RawMessage `json:"issue_type,omitempty"`
	// IssueTypes retains native candidates when local type resolution fails.
	// No candidate is silently chosen when a name is unknown or ambiguous.
	IssueTypes *[]json.RawMessage          `json:"issue_types,omitempty"`
	Fields     *map[string]json.RawMessage `json:"fields,omitempty"`
	TypePages  []json.RawMessage           `json:"type_pages,omitempty"`
	FieldPages []json.RawMessage           `json:"field_pages,omitempty"`
	Payload    json.RawMessage             `json:"payload,omitempty"`
}

// IssueEditInputOutput retains one independently collected editmeta context per
// requested issue. Selectors use the same bounded read owner as ordinary edits;
// this operation never asks for mutation confirmation or performs an edit.
type IssueEditInputOutput struct {
	SelectorState
	KeyedResults[IssueEditInputData]
	// ReadStarted distinguishes joined metadata work from requested-key rows
	// retained after a client setup refusal. It is an internal observation fact.
	ReadStarted bool `json:"-"`
}

// IssueReadStarted reports whether metadata work began, independently of the
// error category returned after that work.
func (out IssueEditInputOutput) IssueReadStarted() bool { return out.ReadStarted }

// RecentIssueKeys retains the joined operation's target identities only after
// metadata work starts. Client setup refusals must not become local history.
func (out IssueEditInputOutput) RecentIssueKeys() []string {
	if !out.ReadStarted {
		return nil
	}
	return out.KeyedResults.RecentIssueKeys()
}

// IssueEditInputData preserves complete native edit metadata alongside a fillable
// request. Values/defaults from one issue are never applied to another issue.
type IssueEditInputData struct {
	Metadata json.RawMessage            `json:"metadata"`
	Fields   map[string]json.RawMessage `json:"fields"`
	Payload  json.RawMessage            `json:"payload"`
}

func init() {
	Register("issue.create.input", IssueCreateInputOutput{}, map[string]any{
		"description": "Live create-input discovery for one actual project and resolved issue type. Complete means the metadata walk finished, not that the placeholder payload can be submitted unchanged.",
		"properties": map[string]any{
			"project":     map[string]any{"description": "Whole native project response, with its actual ID and key. Retained when a later metadata read fails."},
			"issue_type":  map[string]any{"description": "Whole native project-specific issue type selected by exact ID or one case-insensitive name match."},
			"issue_types": map[string]any{"description": "Native available or matching type candidates retained on local resolution failure. These are not an HTTP 404."},
			"fields":      map[string]any{"description": "Complete native definitions keyed by actual field IDs, including requiredness, operations, allowedValues, schema, defaults and future members."},
			"type_pages":  map[string]any{"description": "Every complete native issue-type page used for resolution."},
			"field_pages": map[string]any{"description": "Every complete native field page for this project/type context."},
			"payload":     map[string]any{"description": "Native fields/update skeleton. Actual project/type IDs are filled. Remaining null values are placeholders. Fill wanted values and remove unwanted optional fields or update operations before submitting."},
		},
	})
	Register("issue.edit.input", IssueEditInputOutput{}, map[string]any{
		"description": "Live edit-input discovery in requested issue order. Each successful row retains that issue's native editmeta, actual field-ID definitions and its own placeholder fields/update skeleton. Partial and total failures retain ordered outcomes.",
		"properties": map[string]any{
			"results": map[string]any{"description": "One context per requested key; contexts are never merged. Null payload values are placeholders. Fill wanted values and remove unwanted fields or update operations before using --json-input."},
		},
	})
}
