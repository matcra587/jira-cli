// SchemaOf's override merge is the one sanctioned way prose and
// refinements enter a published schema, so its semantics are contract:
// shape always comes from the struct, overrides may only decorate or
// fill the deliberate `any` escape hatch — never invent structure on a
// node the derivation typed as opaque. These pins are the audit-harness
// consume-or-delete verdict on the merge path: consumed, and held to
// exactly what its doc comment promises.
package unit

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type overrideOutput struct {
	Name  string         `json:"name"`
	Tags  []string       `json:"tags,omitempty"`
	Bag   map[string]any `json:"bag,omitempty"`
	Count int            `json:"count,omitempty"`
}

func TestSchemaOverrideDecoratesDerivedNodes(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(overrideOutput{}, map[string]any{
		"properties": map[string]any{
			"name":  map[string]any{"description": "display name"},
			"count": map[string]any{"minimum": 0},
		},
	})
	compileSchema(t, schema)

	name, _ := property(t, schema, "name")
	if name["type"] != "string" || name["description"] != "display name" {
		t.Errorf("name = %v, want derived string type plus override prose", name)
	}
	count, _ := property(t, schema, "count")
	if count["type"] != "integer" || count["minimum"] != 0 {
		t.Errorf("count = %v, want derived integer type plus override refinement", count)
	}
}

func TestSchemaOverrideMergesIntoArrayItems(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(overrideOutput{}, map[string]any{
		"properties": map[string]any{
			"tags": map[string]any{
				"items": map[string]any{"description": "a label"},
			},
		},
	})
	compileSchema(t, schema)

	tags, _ := property(t, schema, "tags")
	items, _ := tags["items"].(map[string]any)
	if items["type"] != "string" || items["description"] != "a label" {
		t.Errorf("tags.items = %v, want derived string type plus override prose", items)
	}
}

func TestSchemaOverrideCannotInventStructureOnOpaqueNodes(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(overrideOutput{}, map[string]any{
		"properties": map[string]any{
			"bag": map[string]any{
				// A map field is opaque BY DERIVATION: promising keys the
				// code never constrains would turn "anything allowed"
				// into a lie. The guard drops the structural key and
				// keeps the prose.
				"properties":  map[string]any{"sneaky": map[string]any{"type": "string"}},
				"description": "tenant-defined values",
			},
		},
	})
	compileSchema(t, schema)

	bag, _ := property(t, schema, "bag")
	if _, invented := bag["properties"]; invented {
		t.Errorf("override invented properties on an opaque map node: %v", bag)
	}
	if bag["description"] != "tenant-defined values" {
		t.Errorf("prose on an opaque node was lost: %v", bag)
	}
}

func TestSchemaOverrideScalarReplacesDerivedValue(t *testing.T) {
	t.Parallel()
	schema := envelope.SchemaOf(overrideOutput{}, map[string]any{
		"properties": map[string]any{
			"name": map[string]any{"format": "identifier"},
		},
		"description": "the operation payload",
	})
	compileSchema(t, schema)

	if schema["description"] != "the operation payload" {
		t.Errorf("top-level override scalar missing: %v", schema["description"])
	}
	name, _ := property(t, schema, "name")
	if name["format"] != "identifier" {
		t.Errorf("scalar override not applied: %v", name)
	}
}
