package jira

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestUnmodeledFieldSurvivesRoundTrip pins the raw-capture guarantee on both
// the hand-curated fields container and a generated wire type: a field the
// model does not declare must come back byte-equivalent after an
// unmarshal/marshal round-trip. This is the regression fence for the
// dropped-`created` bug class — a plain struct silently discards what it
// does not know.
func TestUnmodeledFieldSurvivesRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value json.Marshaler
		doc   string
		field string
	}{
		{
			name:  "issue fields custom field",
			value: &IssueFields{},
			doc:   `{"customfield_10099":{"deep":{"flag":true},"nested":[1,2,"x"]},"summary":"s"}`,
			field: "customfield_10099",
		},
		{
			name:  "generated type unknown property",
			value: &StatusDetails{},
			doc:   `{"futureServerField":["a",null,3],"name":"Done"}`,
			field: "futureServerField",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.doc), tt.value); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var want, got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.doc), &want); err != nil {
				t.Fatalf("decode original: %v", err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode round-tripped: %v", err)
			}
			if string(got[tt.field]) != string(want[tt.field]) {
				t.Errorf("unmodeled %q = %s, want byte-equivalent %s", tt.field, got[tt.field], want[tt.field])
			}
		})
	}
}

// TestAbsentVersusExplicitNull pins the three-state contract on a field
// where the distinction carries meaning: an absent assignee was not
// requested, a null assignee is Unassigned. Both must decode distinctly and
// re-encode as themselves — null must not become absent, absent must not
// become null.
func TestAbsentVersusExplicitNull(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		wantSet  bool
		wantNull bool
		wantOut  string
	}{
		{name: "absent", doc: `{}`, wantSet: false, wantNull: false, wantOut: `{}`},
		{name: "explicit null", doc: `{"assignee":null}`, wantSet: true, wantNull: true, wantOut: `{"assignee":null}`},
		{
			name:    "present",
			doc:     `{"assignee":{"accountId":"abc123"}}`,
			wantSet: true,
			wantOut: `{"assignee":{"accountId":"abc123"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fields IssueFields
			if err := json.Unmarshal([]byte(tt.doc), &fields); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if fields.Assignee.Set != tt.wantSet || fields.Assignee.Null != tt.wantNull {
				t.Errorf("assignee = {Set:%v Null:%v}, want {Set:%v Null:%v}",
					fields.Assignee.Set, fields.Assignee.Null, tt.wantSet, tt.wantNull)
			}
			out, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tt.wantOut {
				t.Errorf("round-trip = %s, want %s", out, tt.wantOut)
			}
		})
	}
}

// TestModeledFieldWinsOverExtra pins the collision rule: Extra can never
// shadow a declared field on marshal, so captured stale bytes cannot
// override the typed model.
func TestModeledFieldWinsOverExtra(t *testing.T) {
	fields := IssueFields{
		Summary: Nullable[string]{Set: true, Value: "typed"},
		Extra:   map[string]json.RawMessage{"summary": json.RawMessage(`"stale"`)},
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"summary":"typed"}`; string(out) != want {
		t.Errorf("marshal = %s, want %s", out, want)
	}

	// The harder case: the declared field is ABSENT, so omitzero drops it
	// from the marshaled base — the guard must still block the Extra entry
	// (it tests the declared-key list, not the marshaled output).
	fields = IssueFields{
		Extra: map[string]json.RawMessage{"summary": json.RawMessage(`"injected"`)},
	}
	out, err = json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	if want := `{}`; string(out) != want {
		t.Errorf("absent-field shadow: marshal = %s, want %s", out, want)
	}
}

// TestIssueFieldsKeysMatchStructTags fences the hand-maintained pairing in
// fields.go: the declared-key list must exactly mirror the struct's JSON
// tags, or raw capture would either duplicate a declared field or drop an
// undeclared one.
func TestIssueFieldsKeysMatchStructTags(t *testing.T) {
	var tags []string
	for field := range reflect.TypeFor[IssueFields]().Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)

	keys := slices.Clone(issueFieldsKeys)
	slices.Sort(keys)
	if !slices.Equal(tags, keys) {
		t.Errorf("issueFieldsKeys = %q, struct tags = %q", keys, tags)
	}
}

func TestNullableConstructors(t *testing.T) {
	type payload struct {
		Assignee Nullable[string] `json:"assignee,omitzero"`
	}

	got, err := json.Marshal(payload{Assignee: NullableOf("acct-1")})
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	if string(got) != `{"assignee":"acct-1"}` {
		t.Errorf("NullableOf marshaled %s", got)
	}

	got, err = json.Marshal(payload{Assignee: NullableNull[string]()})
	if err != nil {
		t.Fatalf("marshal null: %v", err)
	}
	if string(got) != `{"assignee":null}` {
		t.Errorf("NullableNull marshaled %s", got)
	}

	// The footgun the constructors exist to prevent: Set left false
	// marshals to nothing.
	got, err = json.Marshal(payload{Assignee: Nullable[string]{Value: "x"}})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(got) != `{}` {
		t.Errorf("hand-rolled Nullable marshaled %s", got)
	}
}

func TestNullablePresentNilSliceRefusesToMarshalNull(t *testing.T) {
	// A nil slice marshals to literal null, which would round-trip back as
	// Null:true — a present value silently becoming explicit-null. The
	// marshal must refuse loudly instead.
	_, err := json.Marshal(NullableOf([]string(nil)))
	if err == nil {
		t.Fatal("present nil slice marshaled without error; want a loud refusal")
	}

	// Non-nil empty slice is the legitimate "clear to empty list" value.
	out, err := json.Marshal(NullableOf([]string{}))
	if err != nil {
		t.Fatalf("empty slice: %v", err)
	}
	if string(out) != `[]` {
		t.Errorf("empty slice marshaled %s", out)
	}
}

func TestCaseVariantKeyNeitherDuplicatesNorShadows(t *testing.T) {
	// encoding/json matches struct fields case-insensitively when no
	// exact-case key exists, so "ColorName" populates the model — the Extra
	// bookkeeping must fold-compare or the remarshal emits both casings.
	var sc StatusCategory
	if err := json.Unmarshal([]byte(`{"ColorName":"green","id":7}`), &sc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := sc.Extra["ColorName"]; ok {
		t.Error("case-variant of a declared key survived into Extra")
	}
	out, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Count(out, []byte(`olorName`)) != 1 {
		t.Errorf("remarshal emitted duplicate case-variant keys: %s", out)
	}
}
