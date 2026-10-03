// Schema derivation must publish exactly what encoding/json emits — the
// shadowing and conflict rules included. Every case here was a live bug:
// the old walker overwrote colliding properties with the LOSER's type,
// duplicated required entries (a schema the metaschema rejects), and
// skipped unexported embeds whose exported fields ship on the wire.
package unit

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type shadowEmbed struct {
	DryRun bool   `json:"dry_run"`
	Note   string `json:"note"`
}

type shadowParent struct {
	DryRun string `json:"dry_run"`
	shadowEmbed
}

type conflictA struct {
	Note string `json:"note"`
}

type conflictB struct {
	Memo string `json:"note"`
}

// conflictParent embeds two structs whose fields collide on the JSON name
// "note". Built with reflect.StructOf because the collision is the test's
// point and vet's structtag check (rightly) refuses it in source.
func conflictParent() any {
	t := reflect.StructOf([]reflect.StructField{
		{Name: "Key", Type: reflect.TypeOf(""), Tag: `json:"key"`},
		{Name: "ConflictA", Type: reflect.TypeOf(conflictA{}), Anonymous: true},
		{Name: "ConflictB", Type: reflect.TypeOf(conflictB{}), Anonymous: true},
	})
	return reflect.New(t).Elem().Interface()
}

type unexportedEmbed struct {
	Label string `json:"label"`
}

type promotesUnexported struct {
	Key string `json:"key"`
	unexportedEmbed
}

func compileSchema(t *testing.T, schema map[string]any) {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("reparse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("derived.json", doc); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	if _, err := c.Compile("derived.json"); err != nil {
		t.Fatalf("derived schema does not compile: %v", err)
	}
}

func property(t *testing.T, schema map[string]any, name string) (map[string]any, bool) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %v", schema)
	}
	p, ok := props[name].(map[string]any)
	return p, ok
}

func TestSchemaParentShadowsEmbed(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(shadowParent{}, nil)
	compileSchema(t, schema)

	p, ok := property(t, schema, "dry_run")
	if !ok {
		t.Fatal("dry_run missing")
	}
	// The parent's string shadows the embed's bool — what the wire carries.
	if typ := p["type"]; typ != "string" {
		t.Errorf("dry_run type = %v, want string (the shadowing parent's)", typ)
	}
	req, _ := schema["required"].([]string)
	seen := map[string]int{}
	for _, r := range req {
		seen[r]++
	}
	if seen["dry_run"] != 1 {
		t.Errorf("required lists dry_run %d times, want exactly 1 (%v)", seen["dry_run"], req)
	}
}

func TestSchemaEqualDepthConflictDropsBoth(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(conflictParent(), nil)
	compileSchema(t, schema)

	if _, ok := property(t, schema, "note"); ok {
		t.Error("note published, but encoding/json drops equal-depth conflicts — the wire never carries it")
	}
	if _, ok := property(t, schema, "key"); !ok {
		t.Error("key missing")
	}
}

func TestSchemaPromotesUnexportedEmbedFields(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(promotesUnexported{}, nil)
	compileSchema(t, schema)

	if _, ok := property(t, schema, "label"); !ok {
		t.Error("label missing: encoding/json promotes exported fields of an unexported embedded struct")
	}
}

func TestSchemaOverrideTableNeverAliased(t *testing.T) {
	t.Parallel()
	override := map[string]any{
		"properties": map[string]any{
			"body": map[string]any{"properties": map[string]any{"kind": map[string]any{"type": "string"}}},
		},
	}
	type withAny struct {
		Body any `json:"body"`
	}
	schema := envelope.SchemaOf(withAny{}, override)
	// Mutate the derived schema the way a careless consumer might.
	p, _ := property(t, schema, "body")
	if inner, ok := p["properties"].(map[string]any); ok {
		inner["polluted"] = true
	}
	again := envelope.SchemaOf(withAny{}, override)
	p2, _ := property(t, again, "body")
	if inner, ok := p2["properties"].(map[string]any); ok {
		if _, polluted := inner["polluted"]; polluted {
			t.Error("mutating a derived schema wrote through to the registration override table")
		}
	}
}
