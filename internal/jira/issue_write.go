package jira

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"slices"
	"strings"

	xslices "github.com/gechr/x/slices"
)

// IssueWriteRequest is the request body for POST /rest/api/3/issue and
// PUT /rest/api/3/issue/{issueIdOrKey}.
//
// The same native field and operation containers also back TransitionRequest.
// Endpoint owners compose their own required identities and validate workflow
// semantics without duplicating raw value or extension preservation.
//
// **Both halves are maps of raw JSON, and that is deliberate rather than
// unfinished.** A typed struct is the right model for a payload this package
// ORIGINATES (AddCommentRequest declares its two members and no more). This
// payload is not originated: `--json-input` hands over a whole document the
// caller wrote, including custom fields no model here could name — a tenant's
// customfield_10042 is unbounded by construction — and a typed struct would
// silently drop every member it does not declare. So this tier does for an
// issue payload exactly what it already does for an ADF body: it preserves
// bytes and lets the tier above own meaning.
//
// The map also carries the conflict check the CLI tier performs. §2.1's rule
// is that a field flag beside `--json-input` is fine until the two collide,
// and collision is a question about which KEYS are present — which a map
// answers directly and a struct of pointers answers only for the members it
// happens to declare.
type IssueWriteRequest struct {
	// Fields SETS values: each key is a field id and each value is that
	// field's whole new content. Native input retains an explicitly empty
	// object; CLI composition omits a container it never populated.
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
	// Update applies OPERATIONS to values — add, remove, set — rather than
	// replacing them. Jira keeps the two apart and so does this type: the
	// spec's own wording is that a field included in `fields` cannot also be
	// included in `update`, and honoring that separation is what lets `edit`
	// add one label without being told the other nine.
	Update map[string]json.RawMessage `json:"update,omitempty"`
	// Extra preserves other request members, including historyMetadata,
	// properties, transition and future API extensions. They are request
	// members rather than fields, so Keys never treats them as field conflicts.
	Extra map[string]json.RawMessage `json:"-"`

	fieldsPresent bool
	updatePresent bool
}

// The native containers are objects, not nullable values (IssueUpdateDetails
// in the pinned platform schema). Nullable records presence while decoding;
// UnmarshalJSON refuses null rather than silently turning it into absence.
type issueWriteRequestWire struct {
	Fields Nullable[map[string]json.RawMessage] `json:"fields,omitzero"`
	Update Nullable[map[string]json.RawMessage] `json:"update,omitzero"`
}

var issueWriteRequestKeys = []string{"fields", "update"}

// Clone gives a composer ownership of each map while retaining container
// presence. Raw values remain immutable and can be shared across requests.
func (v IssueWriteRequest) Clone() IssueWriteRequest {
	v.Fields = maps.Clone(v.Fields)
	v.Update = maps.Clone(v.Update)
	v.Extra = maps.Clone(v.Extra)
	return v
}

// UnmarshalJSON retains unmodeled top-level values through the same raw-capture
// boundary as generated wire types. Decode into a fresh value so reused requests
// cannot retain fields or extensions from an earlier payload.
func (v *IssueWriteRequest) UnmarshalJSON(data []byte) error {
	if !jsontext.Value(data).IsValid() {
		return fmt.Errorf("jira: issue payload must contain valid JSON with unique member names")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("jira: issue payload must be a JSON object")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	// The shared wire decoder accepts case variants for compatibility. Two
	// spellings of one container would overwrite authored values, so refuse
	// that ambiguity before decoding into the typed wire representation.
	for _, known := range issueWriteRequestKeys {
		seen := false
		for name := range members {
			if strings.EqualFold(name, known) {
				if seen {
					return fmt.Errorf("jira: issue payload repeats %s with different casing; supply it once", known)
				}
				seen = true
			}
		}
	}
	var wire issueWriteRequestWire
	var extra map[string]json.RawMessage
	if err := unmarshalKeepExtra(data, &wire, &extra, issueWriteRequestKeys); err != nil {
		return err
	}
	if wire.Fields.Null || wire.Update.Null {
		return fmt.Errorf("jira: issue payload fields and update must be JSON objects when present")
	}
	*v = IssueWriteRequest{
		Fields: wire.Fields.Value, Update: wire.Update.Value, Extra: extra,
		fieldsPresent: wire.Fields.Set, updatePresent: wire.Update.Set,
	}
	return nil
}

// MarshalJSON routes the request through MarshalNoEscape so a description's
// ADF body and top-level extension values reach the wire with their native JSON
// spelling intact. Declared fields win over Extra, including case variants.
func (v IssueWriteRequest) MarshalJSON() ([]byte, error) {
	wire := issueWriteRequestWire{}
	if v.fieldsPresent || len(v.Fields) != 0 {
		wire.Fields = NullableOf(v.Fields)
	}
	if v.updatePresent || len(v.Update) != 0 {
		wire.Update = NullableOf(v.Update)
	}
	return marshalMergeExtra(wire, v.Extra, issueWriteRequestKeys)
}

// ApplyOverrides layers native input onto an existing request, preserving
// copied fields the caller did not name. Each destination owns its maps so
// composing one clone cannot change another clone's overrides. Raw JSON values
// are immutable, as they are in SetField and SetOps; maps.Clone is sufficient.
// The pinned gechr/x/maps has no clone or copy operation, so use the standard
// library operations rather than maintaining local equivalents.
func (v *IssueWriteRequest) ApplyOverrides(overrides IssueWriteRequest) {
	// One explicit route replaces the copied value in the other container.
	// Preserve an input that explicitly names both; Jira validates that input
	// rather than composition silently choosing one of its conflicting halves.
	for name := range overrides.Update {
		if _, explicit := overrides.Fields[name]; !explicit {
			delete(v.Fields, name)
		}
	}
	for name := range overrides.Fields {
		if _, explicit := overrides.Update[name]; !explicit {
			delete(v.Update, name)
		}
	}
	if v.Fields == nil {
		v.Fields = maps.Clone(overrides.Fields)
	} else {
		maps.Copy(v.Fields, overrides.Fields)
	}
	if v.Update == nil {
		v.Update = maps.Clone(overrides.Update)
	} else {
		maps.Copy(v.Update, overrides.Update)
	}
	if v.Extra == nil {
		v.Extra = maps.Clone(overrides.Extra)
	} else {
		maps.Copy(v.Extra, overrides.Extra)
	}
	v.fieldsPresent = v.fieldsPresent || overrides.fieldsPresent
	v.updatePresent = v.updatePresent || overrides.updatePresent
}

// Empty reports whether the request contains no fields, operations or other
// native request members. Properties can be edited without changing fields;
// Jira owns the meaning and validation of extension members.
func (v IssueWriteRequest) Empty() bool {
	if len(v.Fields) != 0 || len(v.Update) != 0 {
		return false
	}
	for name := range v.Extra {
		if !xslices.ContainsFold(issueWriteRequestKeys, name) {
			return false
		}
	}
	return true
}

// Keys returns every field id the request touches, from BOTH halves, sorted
// and deduplicated.
//
// Both halves, always. A field set through `update` is just as real as one in
// `fields`, so a conflict check that read only `fields` would let
// `update.priority[{"set":"High"}]` sit beside `--priority` and produce a
// request whose meaning depends on which half Jira applies last. That exact
// omission is the one §2.1 calls out by name.
//
// Sorted so a refusal naming the colliding keys reads the same on every run:
// Go randomizes map iteration, and an error message that reorders itself
// between two runs of the same command looks like two different errors.
func (v IssueWriteRequest) Keys() []string {
	seen := make(map[string]bool, len(v.Fields)+len(v.Update))
	for _, half := range []map[string]json.RawMessage{v.Fields, v.Update} {
		for k := range half {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// SetField records a SET of one field, marshaling value through this
// package's non-escaping encoder so an ADF description survives composition
// as intact as it survives the wire.
//
// It allocates the map on first use rather than requiring the caller to, so
// composing a payload one optional flag at a time reads as a sequence of
// conditional calls instead of a nil check before each.
func (v *IssueWriteRequest) SetField(key string, value any) error {
	raw, err := MarshalNoEscape(value)
	if err != nil {
		return fmt.Errorf("jira: encoding field %q: %w", key, err)
	}
	if v.Fields == nil {
		v.Fields = map[string]json.RawMessage{}
	}
	v.Fields[key] = raw
	return nil
}

// SetOps records the operations to apply to one field, in the `update` half.
// ops is the field's whole operation list, which is the endpoint's own shape:
// one field takes a sequence of add/remove/set verbs applied in order.
func (v *IssueWriteRequest) SetOps(key string, ops []FieldOp) error {
	raw, err := MarshalNoEscape(ops)
	if err != nil {
		return fmt.Errorf("jira: encoding update %q: %w", key, err)
	}
	if v.Update == nil {
		v.Update = map[string]json.RawMessage{}
	}
	v.Update[key] = raw
	return nil
}

// FieldOp is one operation on one field in the `update` half of a write.
//
// Every member is `any` and omitted when nil, because the operand's shape is
// the FIELD's, not the operation's: adding a label takes a bare string,
// adding a component takes {"name": …}, and setting a priority takes
// {"name": …} as well. A typed operand would have to be a union of every
// field shape Jira has.
//
// Only the three verbs this CLI emits are modeled. Jira's update grammar has
// more (edit, copy) and none of them is reachable from a flag, so declaring
// them would make a request expressible that nothing here composes.
type FieldOp struct {
	Add    any `json:"add,omitempty"`
	Remove any `json:"remove,omitempty"`
	Set    any `json:"set,omitempty"`
}

// NameRef names a Jira entity by name — a priority, a component, a version,
// a resolution. Jira accepts either a name or an id for all of them, and the
// CLI takes names because a name is what a person types and what Jira's own
// UI shows.
type NameRef struct {
	Name string `json:"name"`
}

// KeyRef names an entity by key. Projects and parents both take one, and both
// are what a caller already has in hand: an issue key is the CLI's own
// currency.
type KeyRef struct {
	Key string `json:"key"`
}

// TimeTracking is the `timetracking` field's operand.
//
// Two flags write into ONE key, which is why this type exists rather than two
// top-level fields: Jira has no `fields.originalEstimate` to write to, and a
// second SetField call for the second flag would overwrite the first's whole
// object. Both members are omitempty so setting one estimate does not send an
// empty string for the other, which Jira reads as a request to clear it.
type TimeTracking struct {
	OriginalEstimate  string `json:"originalEstimate,omitempty"`
	RemainingEstimate string `json:"remainingEstimate,omitempty"`
}

// CreatedIssue is the response to POST /rest/api/3/issue.
//
// This narrow identity projection does not represent stored fields or optional
// transition/watchers results. The creation receipt preserves the complete
// native object; reading stored values requires a separate issue read.
// ID and key must be usable before publishing an identity. Self is optional.
type CreatedIssue struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}
