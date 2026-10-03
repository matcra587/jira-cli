package jira

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"maps"
	"slices"

	xslices "github.com/gechr/x/slices"
)

// Nullable wraps a wire field so three states the CLI must keep distinct
// survive a round-trip: absent (key missing), explicitly null, and present
// with a value. Jira uses all three with different meanings — an absent
// assignee was not requested, a null assignee is Unassigned — and a plain
// pointer collapses the first two. IsZero makes `omitzero` drop only truly
// absent fields on marshal, so a document round-trips with its null keys
// intact and nothing invented.
type Nullable[T any] struct {
	// Set records that the key was present in the payload at all.
	Set bool
	// Null records that the key was present and explicitly null.
	Null bool
	// Value is the decoded value when Set && !Null; the zero value otherwise.
	Value T
}

// IsZero reports absence, wiring Nullable into encoding/json's `omitzero`.
func (n Nullable[T]) IsZero() bool {
	return !n.Set
}

// NullableOf returns a present Nullable holding v. Write paths must build
// values through this (or NullableNull) — a hand-rolled Nullable{Value: v}
// leaves Set false and marshals to nothing at all.
func NullableOf[T any](v T) Nullable[T] {
	return Nullable[T]{Set: true, Value: v}
}

// NullableNull returns an explicit JSON null — the write-path way to say
// "clear this field" (a null assignee unassigns; an absent one is ignored).
func NullableNull[T any]() Nullable[T] {
	return Nullable[T]{Set: true, Null: true}
}

func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	// encoding/json calls UnmarshalJSON for explicit nulls too (unlike
	// UnmarshalText), which is exactly what makes this wrapper possible.
	n.Set = true
	if bytes.Equal(data, []byte("null")) {
		n.Null = true
		return nil
	}
	if err := json.Unmarshal(data, &n.Value); err != nil {
		return fmt.Errorf("nullable value: %w", err)
	}
	return nil
}

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Null {
		return []byte("null"), nil
	}
	b, err := MarshalNoEscape(n.Value)
	if err != nil {
		return nil, fmt.Errorf("nullable value: %w", err)
	}
	// A present value must never be wire-identical to explicit null — a nil
	// slice or map marshals to `null`, which would round-trip back as
	// Null:true and silently flip semantics (Jira reads null as "clear").
	// Loud beats guessed: the caller either wants NullableNull or a non-nil
	// (e.g. empty-slice) value, and only the caller knows which.
	if bytes.Equal(b, []byte("null")) {
		return nil, fmt.Errorf("nullable: present value of %T marshals to null; use NullableNull for explicit null or a non-nil value", n.Value)
	}
	return b, nil
}

// unmarshalKeepExtra decodes data into the methodless alias dst, then stows
// every key the model does not declare into *extra verbatim. This is the
// mechanism that kills the silently-dropped-field bug class: a plain
// generated struct discards what it does not know, and a field Jira adds
// tomorrow would vanish on the first read-modify-write.
func unmarshalKeepExtra[T any](data []byte, dst *T, extra *map[string]json.RawMessage, known []string) error {
	// Decode into a fresh alias so absent fields replace old facts, nested
	// Nullable values start clean, and failed decodes leave the caller intact.
	var next T
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	// Fold-compare: encoding/json matches struct fields case-insensitively
	// when no exact-case key exists, so a differently-cased payload key was
	// consumed by the model and must not ALSO survive in Extra (it would
	// re-emit as a duplicate alongside the declared tag on marshal).
	for k := range all {
		if xslices.ContainsFold(known, k) {
			delete(all, k)
		}
	}
	*dst = next
	if len(all) == 0 {
		// nil, not an empty map, so a document with no unmodeled fields
		// round-trips through a struct comparison cleanly.
		*extra = nil
		return nil
	}
	*extra = all
	return nil
}

// marshalMergeExtra marshals the methodless alias base (omitzero drops the
// absent fields), then folds the captured extras back in. Declared fields
// win on a key collision — Extra must never be able to shadow the typed
// model, so the guard tests the declared-key list, NOT the marshaled
// output: omitzero has already dropped every absent declared field there,
// and an Extra entry for an absent field would sail through. Key order is
// encoding/json's sorted map order; round-trip fidelity is per-value, not
// whole-document byte order.
func marshalMergeExtra(base any, extra map[string]json.RawMessage, known []string) ([]byte, error) {
	b, err := MarshalNoEscape(base)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return b, nil
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	for k, v := range extra {
		// Fold-compare for the same reason unmarshalKeepExtra does: a
		// case-variant of a declared key is still the declared field.
		if xslices.ContainsFold(known, k) {
			continue
		}
		all[k] = v
	}
	return encodeRawObject(all)
}

// encodeRawObject writes a {key: rawvalue} object with the values copied
// byte for byte. Re-marshaling the map instead would re-escape every
// nested RawMessage — SetEscapeHTML(false) does not reach values inside a
// map the encoder is walking, only the top level it writes itself. Keys
// are sorted so output stays deterministic, matching what the encoder
// would have produced.
func encodeRawObject(fields map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range slices.Sorted(maps.Keys(fields)) {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := MarshalNoEscape(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(fields[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// MarshalNoEscape preserves raw JSON string spelling and leaves HTML and
// line-separator characters unescaped. Go 1.27's v1 Encoder escapes line
// separators even in RawMessage; SetEscapeHTML(false) is not enough.
// V1 options retain the existing nil, omitempty, method and map-order
// semantics. Only string encoding changes, not the wire data model.
func MarshalNoEscape(v any) ([]byte, error) {
	return jsonv2.Marshal(v,
		json.DefaultOptionsV1(),
		jsontext.EscapeForHTML(false),
		jsontext.EscapeForJS(false),
		jsontext.PreserveRawStrings(true),
	)
}

// IssueFields is the hand-curated model of an issue's `fields` container.
// The spec cannot generate this one: it declares `fields` as a free-form
// object because the set is tenant-dynamic (custom fields, screen config).
// Declared here are the system fields the issue read path consumes; every
// other field — custom fields included — rides in Extra untouched.
//
// Timestamps stay strings at the wire tier: Jira emits a non-RFC3339 format
// ("2006-01-02T15:04:05.000-0700"), and parsing is a domain concern, not a
// wire one. Description stays raw ADF; internal/adf owns its typed model.
//
// Byte fidelity: every marshal path in this package goes through
// marshalNoEscape, so preserved ADF bytes (<, >, &, U+2028) survive the
// wire tier intact. Callers must not re-marshal these structs with
// package-level json.Marshal — that would re-escape what this layer
// deliberately kept.
type IssueFields struct {
	Assignee       Nullable[UserDetails]      `json:"assignee,omitzero"`
	Attachment     Nullable[[]Attachment]     `json:"attachment,omitzero"`
	Created        Nullable[string]           `json:"created,omitzero"`
	Description    Nullable[json.RawMessage]  `json:"description,omitzero"`
	Duedate        Nullable[string]           `json:"duedate,omitzero"`
	Issuelinks     Nullable[[]IssueLink]      `json:"issuelinks,omitzero"`
	Issuetype      Nullable[IssueTypeDetails] `json:"issuetype,omitzero"`
	Labels         Nullable[[]string]         `json:"labels,omitzero"`
	Priority       Nullable[Priority]         `json:"priority,omitzero"`
	Project        Nullable[ProjectDetails]   `json:"project,omitzero"`
	Reporter       Nullable[UserDetails]      `json:"reporter,omitzero"`
	Resolution     Nullable[Resolution]       `json:"resolution,omitzero"`
	Resolutiondate Nullable[string]           `json:"resolutiondate,omitzero"`
	Status         Nullable[StatusDetails]    `json:"status,omitzero"`
	Summary        Nullable[string]           `json:"summary,omitzero"`
	Updated        Nullable[string]           `json:"updated,omitzero"`

	// Extra carries every field this model does not declare, verbatim.
	Extra map[string]json.RawMessage `json:"-"`
}

type issueFieldsAlias IssueFields

// issueFieldsKeys must list exactly the JSON tags declared above; a unit
// test enforces the pairing so the two cannot drift apart.
var issueFieldsKeys = []string{
	"assignee",
	"attachment",
	"created",
	"description",
	"duedate",
	"issuelinks",
	"issuetype",
	"labels",
	"priority",
	"project",
	"reporter",
	"resolution",
	"resolutiondate",
	"status",
	"summary",
	"updated",
}

func (v IssueFields) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(issueFieldsAlias(v), v.Extra, issueFieldsKeys)
}

func (v *IssueFields) UnmarshalJSON(data []byte) error {
	return unmarshalKeepExtra(data, (*issueFieldsAlias)(v), &v.Extra, issueFieldsKeys)
}
