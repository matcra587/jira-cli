package contract

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestOutputSchemasDescribeNestedEnvelopeAndIssueShapes(t *testing.T) {
	root := loadAgentSchemaShapes(t)

	// The tool-wide envelope and error shapes ride the schema root's
	// output_contract extension — they describe every response, not one
	// command.
	contract := decodeSchemaObjectMap(t, root.Extensions["output_contract"])

	envelope := contract["envelope"]
	// The envelope is lean: ok, meta, data, errors, warnings.
	for _, required := range []string{"ok", "meta", "data", "errors", "warnings"} {
		if !containsString(envelope.Required, required) {
			t.Fatalf("envelope schema missing required %q: %+v", required, envelope)
		}
	}
	meta := envelope.Properties["meta"]
	// Machine envelopes omit meta.profile entirely; meta requires only
	// command and timestamp.
	for _, required := range []string{"command", "timestamp"} {
		if !containsString(meta.Required, required) {
			t.Fatalf("envelope meta schema missing required %q: %+v", required, meta)
		}
	}
	if containsString(meta.Required, "profile") {
		t.Fatalf("envelope meta schema must not require profile: %+v", meta)
	}
	// The error schema carries the lean structured-error fields.
	errSchema := contract["error"]
	for _, required := range []string{"type", "code", "message", "hint", "retryable"} {
		if !containsString(errSchema.Required, required) {
			t.Fatalf("error schema missing required %q: %+v", required, errSchema)
		}
	}
	if !containsString(errSchema.Properties["type"].Enum, "io") {
		t.Fatalf("error type schema missing io: %+v", errSchema.Properties["type"])
	}
	if !containsString(errSchema.Properties["code"].Enum, "output_write_failed") {
		t.Fatalf("error code schema missing output_write_failed: %+v", errSchema.Properties["code"])
	}

	issueList := decodeSchemaObject(t, findSchemaCommand(root, "jira issue list").OutputSchema)
	issues := issueList.Properties["issues"]
	item := issues.Items
	for _, required := range []string{"key", "summary", "status", "updated"} {
		if !containsString(item.Required, required) {
			t.Fatalf("issue.list item schema missing required %q: %+v", required, item)
		}
	}
	// assignee and priority MUST be present in the shape but are nullable per spec.
	if _, ok := item.Properties["assignee"]; !ok {
		t.Fatalf("issue.list item schema missing assignee property: %+v", item)
	}
	if _, ok := item.Properties["priority"]; !ok {
		t.Fatalf("issue.list item schema missing priority property: %+v", item)
	}
	for _, required := range []string{"assignee", "priority"} {
		if containsString(item.Required, required) {
			t.Fatalf("issue.list spec marks %q as nullable; should not be required: %+v", required, item.Required)
		}
	}

	issueEdit := decodeSchemaObject(t, findSchemaCommand(root, "jira issue edit").OutputSchema)
	if !containsString(issueEdit.Required, "fields") {
		t.Fatalf("issue.edit schema missing fields requirement: %+v", issueEdit)
	}
}

type schemaObject struct {
	// Type may be a string ("object") or an array (["object", "null"]) per JSON Schema spec.
	Type       any                     `json:"type"`
	Required   []string                `json:"required"`
	Properties map[string]schemaObject `json:"properties"`
	Items      *schemaObject           `json:"items"`
	Enum       []string                `json:"enum"`
}

// decodeSchemaObject round-trips a decoded map[string]any schema value
// into the typed schemaObject shape the assertions use.
func decodeSchemaObject(t *testing.T, value any) schemaObject {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal schema value: %v", err)
	}
	var out schemaObject
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode schema value: %v", err)
	}
	return out
}

func decodeSchemaObjectMap(t *testing.T, value any) map[string]schemaObject {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal schema map: %v", err)
	}
	var out map[string]schemaObject
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode schema map: %v", err)
	}
	return out
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
