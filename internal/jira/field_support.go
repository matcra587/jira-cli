package jira

import "encoding/json"

// FieldTypeExample describes a native field value, not a tenant field schema.
// Names identify familiar field families; metadata supplies the actual field
// IDs, plugin types, allowed values and operations for a particular issue.
type FieldTypeExample struct {
	Name        string          `json:"name"`
	NativeShape string          `json:"native_shape"`
	Example     json.RawMessage `json:"example"`
	Notes       string          `json:"notes"`
}

// FieldTypeExamples returns independent examples for the native IssueWriteRequest
// boundary. No registry lookup, scalar lifting or type-specific validation runs
// there: even unknown marketplace field values remain native JSON.
func FieldTypeExamples() []FieldTypeExample {
	return []FieldTypeExample{
		{"cascadingselect", "object: id, optional child.id", json.RawMessage(`{"id":"10001","child":{"id":"10002"}}`), "Use parent and child option IDs from the field's context."},
		{"components", "array of objects: id", json.RawMessage(`[{"id":"10001"}]`), "System field components; use project component IDs."},
		{"date", "string: YYYY-MM-DD", json.RawMessage(`"2026-10-01"`), "Use a calendar date; the CLI does not validate its range."},
		{"datetime", "string: ISO-8601 with timezone", json.RawMessage(`"2026-10-01T12:00:00.000+0000"`), "Use the format accepted by the field; values are not normalized."},
		{"fixversions", "array of objects: id", json.RawMessage(`[{"id":"10001"}]`), "System field fixVersions; use project version IDs."},
		{"group", "object: name", json.RawMessage(`{"name":"example-group"}`), "Use a group name allowed by the field; native input does not resolve names."},
		{"labels", "array of strings", json.RawMessage(`["triage","ready"]`), "A fields value replaces the list; update operations depend on metadata."},
		{"multigroup", "array of objects: name", json.RawMessage(`[{"name":"example-group"}]`), "Use group names allowed by the field; native input does not resolve names."},
		{"multiselect", "array of objects: id", json.RawMessage(`[{"id":"10001"},{"id":"10002"}]`), "Use option IDs from the field's context."},
		{"multiuser", "array of objects: accountId", json.RawMessage(`[{"accountId":"example-account-id"}]`), "Use Atlassian account IDs; native input does not resolve names or @me."},
		{"multiversionpicker", "array of objects: id", json.RawMessage(`[{"id":"10001"}]`), "Use version IDs allowed by the custom field."},
		{"number", "number", json.RawMessage(`9007199254740993`), "Native numeric tokens are preserved without float64 conversion."},
		{"parent", "object: key", json.RawMessage(`{"key":"KEY-123"}`), "System field parent; use an existing permitted parent issue."},
		{"projectpicker", "object: id", json.RawMessage(`{"id":"10001"}`), "Use a project ID allowed by the custom field."},
		{"select", "object: id", json.RawMessage(`{"id":"10001"}`), "Use an option ID from the field's context; scalar labels are not wrapped."},
		{"string", "string", json.RawMessage(`"Example text"`), "Plain text fields retain their native string value."},
		{"textarea", "ADF document object", json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Example text"}]}]}`), "Jira REST v3 multiline text uses ADF; convert Markdown with adf convert before composing the payload."},
		{"user", "object: accountId", json.RawMessage(`{"accountId":"example-account-id"}`), "Use an Atlassian account ID; native input does not resolve names or @me."},
		{"version", "array of objects: id", json.RawMessage(`[{"id":"10001"}]`), "System field versions (affects versions); use project version IDs."},
		{"versionpicker", "object: id", json.RawMessage(`{"id":"10001"}`), "Use a version ID allowed by the custom field."},
	}
}
