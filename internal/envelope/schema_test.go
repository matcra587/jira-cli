package envelope_test

// No t.Parallel: SchemaOf and mergeSchema are pure, but this file follows
// the same non-parallel posture as registry_test.go for consistency
// within the package's test suite.

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func schemaProps(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties map: %#v", schema)
	}
	return props
}

func TestSchemaOfBytePreimagesMatchesBase64EncodingAndNativeJSON(t *testing.T) {
	type byteForms struct {
		Preimage []byte          `json:"preimage"`
		Optional []byte          `json:"optional,omitempty"`
		Native   json.RawMessage `json:"native"`
		Array    [2]byte         `json:"array"`
	}
	props := schemaProps(t, envelope.SchemaOf(byteForms{}, nil))
	preimage := props["preimage"].(map[string]any)
	optional := props["optional"].(map[string]any)
	if !reflect.DeepEqual(preimage["type"], []string{"string", "null"}) || preimage["contentEncoding"] != "base64" || optional["type"] != "string" || optional["contentEncoding"] != "base64" {
		t.Fatal("byte preimages do not describe actual encoding", props)
	}
	if len(props["native"].(map[string]any)) != 0 || props["array"].(map[string]any)["type"] != "array" {
		t.Fatal("byte preimage refinement changed native JSON or fixed arrays", props)
	}
	for _, preimage := range [][]byte{nil, {}, []byte("native preimage")} {
		encoded, err := json.Marshal(byteForms{Preimage: preimage, Native: json.RawMessage(`{"future":9007199254740993}`)})
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		if preimage == nil {
			if actual["preimage"] != nil {
				t.Fatal("nil preimage was not null", encoded)
			}
		} else if _, ok := actual["preimage"].(string); !ok {
			t.Fatal("present preimage was not a string", encoded)
		}
		if _, present := actual["optional"]; present {
			t.Fatal("omitted byte preimage was invented", encoded)
		}
	}
}

func TestSchemaOfOmitZeroMatchesActualEncoding(t *testing.T) {
	type optionalValues struct {
		Email jira.Nullable[string] `json:"email,omitzero"`
		Count int                   `json:"count,omitzero"`
		Ptr   *string               `json:"ptr,omitzero"`
		Kept  string                `json:"kept"`
	}
	schema := envelope.SchemaOf(optionalValues{}, nil)
	required, _ := schema["required"].([]string)
	if !reflect.DeepEqual(required, []string{"kept"}) {
		t.Fatalf("omitzero fields incorrectly required: %v", required)
	}
	props := schemaProps(t, schema)
	if !reflect.DeepEqual(props["email"].(map[string]any)["type"], []string{"string", "null"}) || props["ptr"].(map[string]any)["type"] != "string" {
		t.Fatal("omitzero removed value/null semantics or invented an emitted nil pointer")
	}
	for _, tc := range []struct {
		name      string
		value     optionalValues
		wantEmail bool
		want      any
	}{
		{name: "unset"},
		{name: "explicit null", value: optionalValues{Email: jira.NullableNull[string]()}, wantEmail: true},
		{name: "empty string", value: optionalValues{Email: jira.NullableOf("")}, wantEmail: true, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			value, present := actual["email"]
			if present != tc.wantEmail || value != tc.want {
				t.Fatalf("actual omission differs: %s", encoded)
			}
			for _, name := range required {
				if _, present := actual[name]; !present {
					t.Fatalf("schema requires omitted field %q: %s", name, encoded)
				}
			}
		})
	}
	user := envelope.SchemaOf(envelope.UserSearchMatch{}, nil)
	if slices.Contains(user["required"].([]string), "email_address") {
		t.Fatal("privacy-controlled search email must be optional in its actual schema")
	}
}

func TestSchemaOfNilSampleReturnsUnconstrainedSchemaMergedWithOverrides(t *testing.T) {
	got := envelope.SchemaOf(nil, nil)
	if len(got) != 0 {
		t.Errorf("SchemaOf(nil, nil) = %#v, want an empty map (a nil registration derives no shape)", got)
	}

	got = envelope.SchemaOf(nil, map[string]any{"description": "escape hatch prose"})
	if got["description"] != "escape hatch prose" {
		t.Errorf("SchemaOf(nil, overrides) = %#v, want the override merged onto the empty base", got)
	}
}

func TestSchemaOfNoJSONTagUsesGoFieldName(t *testing.T) {
	type withNoTag struct {
		NoTag string
	}
	schema := envelope.SchemaOf(withNoTag{}, nil)
	props := schemaProps(t, schema)
	if _, ok := props["NoTag"]; !ok {
		t.Errorf("properties = %#v, want the untagged field published under its Go name %q", props, "NoTag")
	}
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "NoTag" {
		t.Errorf("required = %#v, want [\"NoTag\"] (no omitempty means required)", req)
	}
}

func TestSchemaOfDashTagAndUnexportedFieldsAreSkipped(t *testing.T) {
	type withSkips struct {
		Skip       string `json:"-"`
		unexported string
		Kept       string `json:"kept"`
	}
	// unexported is populated (not left zero-value) so the assertion
	// proves SchemaOf skips it regardless of content, not just because
	// there was nothing there to leak.
	schema := envelope.SchemaOf(withSkips{Skip: "skip", unexported: "leak-if-published", Kept: "kept"}, nil)
	props := schemaProps(t, schema)
	if len(props) != 1 {
		t.Errorf("properties = %#v, want exactly one published field", props)
	}
	if _, ok := props["kept"]; !ok {
		t.Errorf("properties = %#v, want %q present", props, "kept")
	}
	if _, ok := props["Skip"]; ok {
		t.Errorf(`properties = %#v, want json:"-" field absent`, props)
	}
	if _, ok := props["unexported"]; ok {
		t.Errorf("properties = %#v, want unexported field absent", props)
	}
}

func TestSchemaOfPointerAndSliceFieldsWithoutOmitemptyGoNullable(t *testing.T) {
	type anyPtrHolder struct {
		Ptr    *string  `json:"ptr"`
		OptPtr *string  `json:"opt_ptr,omitempty"`
		List   []string `json:"list"`
		AnyPtr *any     `json:"any_ptr"`
	}
	schema := envelope.SchemaOf(anyPtrHolder{}, nil)
	props := schemaProps(t, schema)

	// Pointer field, no omitempty: unwraps to the pointee's type, then
	// nullable() adds the "null" alternative because a nil pointer
	// marshals as JSON null.
	ptrType, _ := props["ptr"].(map[string]any)["type"].([]string)
	if len(ptrType) != 2 || ptrType[0] != "string" || ptrType[1] != "null" {
		t.Errorf(`properties["ptr"]["type"] = %#v, want ["string","null"]`, props["ptr"].(map[string]any)["type"])
	}

	// Pointer field WITH omitempty: no nullable wrapping.
	optType := props["opt_ptr"].(map[string]any)["type"]
	if optType != "string" {
		t.Errorf(`properties["opt_ptr"]["type"] = %#v, want "string" (omitempty means no nullable wrap)`, optType)
	}

	// Slice field, no omitempty: same nullable treatment, wrapping the
	// derived "array" type rather than a scalar.
	listType, _ := props["list"].(map[string]any)["type"].([]string)
	if len(listType) != 2 || listType[0] != "array" || listType[1] != "null" {
		t.Errorf(`properties["list"]["type"] = %#v, want ["array","null"]`, props["list"].(map[string]any)["type"])
	}

	// *any field: schemaOfType for the interface elem is {} (no "type"
	// key at all). nullable()'s switch on schema["type"] hits its nil
	// case and is a no-op — pins that branch of nullable directly.
	anyPtrSchema := props["any_ptr"].(map[string]any)
	if _, hasType := anyPtrSchema["type"]; hasType {
		t.Errorf(`properties["any_ptr"] = %#v, want no "type" key (nullable() no-ops on an untyped node)`, anyPtrSchema)
	}
}

func TestSchemaOfMapFieldIsOpaqueObjectAndNullableWhenRequired(t *testing.T) {
	type withMap struct {
		M map[string]int `json:"m"`
	}
	schema := envelope.SchemaOf(withMap{}, nil)
	props := schemaProps(t, schema)
	mSchema := props["m"].(map[string]any)
	if _, ok := mSchema["properties"]; ok {
		t.Errorf(`properties["m"] = %#v, want no "properties" key — a map field is opaque by declaration`, mSchema)
	}
	// A map is one of the nullable-eligible kinds (nil maps marshal as
	// null too), so without omitempty it gets the same wrapping as a
	// pointer or slice.
	mType, _ := mSchema["type"].([]string)
	if len(mType) != 2 || mType[0] != "object" || mType[1] != "null" {
		t.Errorf(`properties["m"]["type"] = %#v, want ["object","null"]`, mSchema["type"])
	}
}

func TestSchemaOfSliceOfStructRecursesIntoElementSchema(t *testing.T) {
	type item struct {
		X string `json:"x"`
	}
	type withSlice struct {
		Items []item `json:"items"`
	}
	schema := envelope.SchemaOf(withSlice{}, nil)
	props := schemaProps(t, schema)
	itemsSchema := props["items"].(map[string]any)
	elemSchema, ok := itemsSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["items"]["items"] missing or not a schema map: %#v`, itemsSchema)
	}
	elemProps, _ := elemSchema["properties"].(map[string]any)
	if _, ok := elemProps["x"]; !ok {
		t.Errorf("slice element schema = %#v, want a recursed object schema with property %q", elemSchema, "x")
	}
}

func TestSchemaOfSliceOfInterfaceHasNoItemsConstraint(t *testing.T) {
	// omitempty isolates this case to schemaOfType's slice-of-interface
	// branch alone; the nullable-wrapping axis for slices is pinned
	// separately in TestSchemaOfPointerAndSliceFieldsWithoutOmitemptyGoNullable.
	type withAnySlice struct {
		Anys []any `json:"anys,omitempty"`
	}
	schema := envelope.SchemaOf(withAnySlice{}, nil)
	props := schemaProps(t, schema)
	anysSchema := props["anys"].(map[string]any)
	if anysSchema["type"] != "array" {
		t.Errorf(`properties["anys"]["type"] = %#v, want "array"`, anysSchema["type"])
	}
	if _, ok := anysSchema["items"]; ok {
		t.Errorf(`properties["anys"] = %#v, want no "items" key for a []any field`, anysSchema)
	}
}

func TestSchemaOfEmbeddedStructPromotesFields(t *testing.T) {
	type innerA struct {
		A string `json:"a"`
	}
	type withEmbed struct {
		innerA
		B string `json:"b"`
	}
	schema := envelope.SchemaOf(withEmbed{}, nil)
	props := schemaProps(t, schema)
	for _, name := range []string{"a", "b"} {
		if _, ok := props[name]; !ok {
			t.Errorf("properties = %#v, want promoted field %q present", props, name)
		}
	}
}

// TestSchemaOfNameCollisionShallowestDepthWins pins encoding/json's own
// visibility rule as reimplemented in resolveFields: when an embedded
// field and a direct field share a json name, the shallower (direct)
// field wins, regardless of tagging.
func TestSchemaOfNameCollisionShallowestDepthWins(t *testing.T) {
	type innerB struct {
		X bool `json:"dup"`
	}
	type collision struct {
		innerB
		Y string `json:"dup"`
	}
	schema := envelope.SchemaOf(collision{}, nil)
	props := schemaProps(t, schema)
	dup, ok := props["dup"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["dup"] missing: %#v`, props)
	}
	if dup["type"] != "string" {
		t.Errorf(`properties["dup"]["type"] = %v, want "string" (the depth-0 field Y, not the embedded bool)`, dup["type"])
	}
}

// TestSchemaOfAmbiguousSameDepthCollisionDropsTheField pins
// resolveFields' "otherwise dropped" branch: two same-depth, both-tagged
// candidates for one name is exactly what encoding/json itself silently
// omits, so the schema must omit it too.
//
// The ambiguous type is built with reflect.StructOf rather than a static
// struct literal: two embedded fields statically tagged with the same
// json name trips `go vet`'s structtag check (it catches this exact
// promoted-field ambiguity at compile time in ordinary code), so writing
// it as source would fail `mise run check` even though it's the runtime
// shape this test exists to pin.
func TestSchemaOfAmbiguousSameDepthCollisionDropsTheField(t *testing.T) {
	type tieA struct {
		V string `json:"tie"`
	}
	type tieB struct {
		V2 bool `json:"tie"`
	}
	ambiguous := reflect.StructOf([]reflect.StructField{
		{Name: "TieA", Type: reflect.TypeOf(tieA{}), Anonymous: true},
		{Name: "TieB", Type: reflect.TypeOf(tieB{}), Anonymous: true},
	})
	sample := reflect.New(ambiguous).Elem().Interface()

	schema := envelope.SchemaOf(sample, nil)
	props := schemaProps(t, schema)
	if _, ok := props["tie"]; ok {
		t.Errorf(`properties = %#v, want "tie" dropped entirely on an unresolved same-depth collision`, props)
	}
	req, _ := schema["required"].([]string)
	for _, r := range req {
		if r == "tie" {
			t.Errorf("required = %#v, want the dropped field absent from required too", req)
		}
	}
}

// TestSchemaOfUnexportedEmbeddedTypePromotesUnlessPointer pins
// collectStructFields' documented exception: an untagged embedded struct
// field with an unexported type name still has its exported fields
// promoted by encoding/json — UNLESS the embed is a pointer, in which
// case the field is unexported and is skipped like any other unexported
// field.
func TestSchemaOfUnexportedEmbeddedTypePromotesUnlessPointer(t *testing.T) {
	schema := envelope.SchemaOf(outerWithUnexportedEmbed{}, nil)
	props := schemaProps(t, schema)
	if _, ok := props["visible"]; !ok {
		t.Errorf("properties = %#v, want the unexported-type embed's exported field %q promoted", props, "visible")
	}
	if _, ok := props["other"]; !ok {
		t.Errorf("properties = %#v, want the sibling field %q present", props, "other")
	}

	ptrSchema := envelope.SchemaOf(outerWithUnexportedPointerEmbed{}, nil)
	ptrProps := schemaProps(t, ptrSchema)
	if _, ok := ptrProps["visible"]; ok {
		t.Errorf("properties = %#v, want the unexported-type field NOT promoted through a pointer embed", ptrProps)
	}
	if _, ok := ptrProps["other2"]; !ok {
		t.Errorf("properties = %#v, want the sibling field %q present", ptrProps, "other2")
	}
}

// embeddedType has a deliberately unexported (lowercase) type name to
// exercise the promote-unless-pointer branch above; it must live at
// package scope so both non-pointer and pointer embeds share the type.
type embeddedType struct {
	Visible string `json:"visible"`
}

type outerWithUnexportedEmbed struct {
	embeddedType
	Other string `json:"other"`
}

type outerWithUnexportedPointerEmbed struct {
	*embeddedType
	Other2 string `json:"other2"`
}

func TestSchemaOfSelfReferentialCycleCollapsesToOpaqueObject(t *testing.T) {
	schema := envelope.SchemaOf(cycleNode{}, nil)
	props := schemaProps(t, schema)
	next, ok := props["next"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["next"] missing: %#v`, props)
	}
	if next["type"] != "object" {
		t.Errorf(`properties["next"]["type"] = %v, want "object" (the cycle collapses to opaque, and Next's omitempty means no nullable wrap on top)`, next["type"])
	}
	if _, ok := next["properties"]; ok {
		t.Errorf(`properties["next"] = %#v, want no nested "properties" — the self-reference must not recurse forever`, next)
	}
	if _, ok := props["name"]; !ok {
		t.Errorf("properties = %#v, want sibling field %q present", props, "name")
	}
}

type cycleNode struct {
	Next *cycleNode `json:"next,omitempty"`
	Name string     `json:"name"`
}

// TestSchemaOfSiblingReuseOfSameTypeExpandsBothTimes pins that visited is
// path-scoped (unmarked on return from schemaOfType), not a global memo:
// the same struct type used twice as siblings must fully expand both
// times, not opaque out the second occurrence.
func TestSchemaOfSiblingReuseOfSameTypeExpandsBothTimes(t *testing.T) {
	type dup struct {
		V string `json:"v"`
	}
	type siblings struct {
		A dup `json:"a"`
		B dup `json:"b"`
	}
	schema := envelope.SchemaOf(siblings{}, nil)
	props := schemaProps(t, schema)
	for _, name := range []string{"a", "b"} {
		sub, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("properties[%q] missing: %#v", name, props)
		}
		subProps, _ := sub["properties"].(map[string]any)
		if _, ok := subProps["v"]; !ok {
			t.Errorf("properties[%q] = %#v, want it fully expanded with property %q, not collapsed to opaque", name, sub, "v")
		}
	}
}

func TestMergeSchemaGuardsOpaqueMapFieldFromInventedProperties(t *testing.T) {
	type withMap struct {
		M map[string]int `json:"m,omitempty"`
	}
	overrides := map[string]any{
		"properties": map[string]any{
			"m": map[string]any{
				"properties": map[string]any{"fake": map[string]any{"type": "string"}},
			},
		},
	}
	schema := envelope.SchemaOf(withMap{}, overrides)
	props := schemaProps(t, schema)
	mSchema := props["m"].(map[string]any)
	if mSchema["type"] != "object" {
		t.Errorf(`properties["m"]["type"] = %v, want "object"`, mSchema["type"])
	}
	if _, ok := mSchema["properties"]; ok {
		t.Errorf("properties = %#v, want the override's invented properties ignored on a map field opaque by derivation", mSchema)
	}
}

// TestMergeSchemaAppliesPropertiesOverrideToUntypedAnyFieldAndClones pins
// the escape hatch mergeSchema documents: a bare-any node (no "type" key
// at all, e.g. an `any` field) DOES accept an invented "properties"
// override — and cloneSchema's copy means mutating the override map
// afterward cannot reach back into the registered schema.
func TestMergeSchemaAppliesPropertiesOverrideToUntypedAnyFieldAndClones(t *testing.T) {
	type withAny struct {
		Extra any `json:"extra,omitempty"`
	}
	fooSchema := map[string]any{"type": "string"}
	overrides := map[string]any{
		"properties": map[string]any{
			"extra": map[string]any{
				"properties": map[string]any{"foo": fooSchema},
			},
		},
	}
	schema := envelope.SchemaOf(withAny{}, overrides)
	props := schemaProps(t, schema)
	extra, ok := props["extra"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["extra"] missing: %#v`, props)
	}
	extraProps, ok := extra["properties"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["extra"]["properties"] missing, want the override applied: %#v`, extra)
	}
	if _, ok := extraProps["foo"]; !ok {
		t.Errorf(`properties["extra"]["properties"] = %#v, want %q present`, extraProps, "foo")
	}

	// Mutate the override's nested map after the call; a real clone means
	// the registered schema must not see this.
	fooSchema["type"] = "MUTATED-AFTER-THE-FACT"
	fooAfter, _ := extraProps["foo"].(map[string]any)
	if fooAfter["type"] == "MUTATED-AFTER-THE-FACT" {
		t.Errorf("returned schema aliases the override table; cloneSchema should have deep-copied it")
	}
}

func TestMergeSchemaScalarOverrideReplacesDerivedValue(t *testing.T) {
	type simple struct {
		Name string `json:"name"`
	}
	schema := envelope.SchemaOf(simple{}, map[string]any{
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "format": "hand-tuned"},
		},
	})
	props := schemaProps(t, schema)
	name := props["name"].(map[string]any)
	if name["format"] != "hand-tuned" {
		t.Errorf(`properties["name"]["format"] = %v, want the override's scalar value to replace/add onto the derived node`, name["format"])
	}
	if name["type"] != "string" {
		t.Errorf(`properties["name"]["type"] = %v, want the derived type preserved`, name["type"])
	}
}

func TestSchemaOfScalarKinds(t *testing.T) {
	type scalars struct {
		I    int     `json:"i"`
		F    float64 `json:"f"`
		Bool bool    `json:"bool"`
		Str  string  `json:"str"`
	}
	schema := envelope.SchemaOf(scalars{}, nil)
	props := schemaProps(t, schema)
	want := map[string]string{"i": "integer", "f": "number", "bool": "boolean", "str": "string"}
	for name, wantType := range want {
		got := props[name].(map[string]any)["type"]
		if got != wantType {
			t.Errorf("properties[%q][\"type\"] = %v, want %q", name, got, wantType)
		}
	}
}

// TestSchemaOfUnhandledKindFallsBackToUnconstrained pins schemaOfType's
// default case: a Go kind the derivation has no rule for (a function
// field) publishes an empty, unconstrained schema node rather than
// panicking or guessing.
func TestSchemaOfUnhandledKindFallsBackToUnconstrained(t *testing.T) {
	type withFunc struct {
		Callback func() `json:"callback,omitempty"`
	}
	schema := envelope.SchemaOf(withFunc{}, nil)
	props := schemaProps(t, schema)
	cb, ok := props["callback"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["callback"] missing: %#v`, props)
	}
	if len(cb) != 0 {
		t.Errorf(`properties["callback"] = %#v, want an empty/unconstrained schema node for an unhandled kind`, cb)
	}
}

// TestSchemaOfSingleTaggedFieldWinsAmongMultipleUntaggedAtSameDepth pins
// resolveFields' middle case: when two same-depth candidates share a
// published name but exactly one carries an explicit json tag, the
// tagged one wins (matching encoding/json's own resolution) rather than
// falling through to "ambiguous, drop". Built with reflect.StructOf, for
// the same reason as the ambiguous-collision test above: two embedded
// fields publishing the same name at the same depth trips go vet's
// structtag ambiguity check if written as a static struct literal.
func TestSchemaOfSingleTaggedFieldWinsAmongMultipleUntaggedAtSameDepth(t *testing.T) {
	type embedUntagged struct {
		Dup bool // untagged: promoted under its Go name, "Dup"
	}
	type embedTagged struct {
		X string `json:"Dup"` // tagged explicitly as "Dup", same published name
	}
	sample := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "EmbedUntagged", Type: reflect.TypeOf(embedUntagged{}), Anonymous: true},
		{Name: "EmbedTagged", Type: reflect.TypeOf(embedTagged{}), Anonymous: true},
	})).Elem().Interface()

	schema := envelope.SchemaOf(sample, nil)
	props := schemaProps(t, schema)
	dup, ok := props["Dup"].(map[string]any)
	if !ok {
		t.Fatalf(`properties["Dup"] missing: %#v`, props)
	}
	if dup["type"] != "string" {
		t.Errorf(`properties["Dup"]["type"] = %v, want "string" (the single tagged candidate wins the same-depth tie)`, dup["type"])
	}
}

func TestMergeSchemaIgnoresMalformedNonMapPropertiesOverride(t *testing.T) {
	type simple struct {
		Name string `json:"name"`
	}
	// A malformed override: "properties" is a bare string, not a map.
	// mergeSchema's structural-key guard must ignore it rather than
	// panicking on the failed type assertion.
	schema := envelope.SchemaOf(simple{}, map[string]any{"properties": "not-a-map"})
	props := schemaProps(t, schema)
	if _, ok := props["name"]; !ok {
		t.Errorf("properties = %#v, want the derived schema untouched by a malformed override", props)
	}
}

func TestSchemaOfRequiredIsSortedAndExcludesOmitempty(t *testing.T) {
	type sample struct {
		Zeta  string `json:"zeta"`
		Alpha string `json:"alpha"`
		Opt   string `json:"opt,omitempty"`
	}
	schema := envelope.SchemaOf(sample{}, nil)
	req, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required missing or wrong type: %#v", schema["required"])
	}
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(req, want) {
		t.Errorf("required = %#v, want %#v (sorted, omitempty field excluded)", req, want)
	}
}

// TestSchemaOfNullableDescribesTheWireShape pins the rule that a wrapper
// is schematized by what it MARSHALS, not by its fields: every generated
// wire field is a Nullable, and publishing {Set,Null,Value} would describe
// a shape no agent ever receives.
func TestSchemaOfNullableDescribesTheWireShape(t *testing.T) {
	t.Parallel()
	type out struct {
		Summary jira.Nullable[string]          `json:"summary"`
		Body    jira.Nullable[json.RawMessage] `json:"body"`
		Labels  jira.Nullable[[]string]        `json:"labels"`
	}
	schema := envelope.SchemaOf(out{}, nil)
	props, _ := schema["properties"].(map[string]any)

	summary, _ := props["summary"].(map[string]any)
	types, _ := summary["type"].([]string)
	if len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("summary type = %v, want [string null]", summary["type"])
	}
	if _, leaked := summary["properties"]; leaked {
		t.Error("Nullable internals leaked into the published schema")
	}
	labels, _ := props["labels"].(map[string]any)
	if lt, _ := labels["type"].([]string); len(lt) == 0 || lt[0] != "array" {
		t.Errorf("labels type = %v, want array+null", labels["type"])
	}
}
