package envelope

import (
	"encoding/json"
	"testing"
)

func TestRawJSONSchemaDescribesEmittedJSON(t *testing.T) {
	t.Parallel()
	type sample struct {
		Data json.RawMessage `json:"data,omitempty"`
	}
	props := SchemaOf(sample{}, nil)["properties"].(map[string]any)
	if schema := props["data"].(map[string]any); len(schema) != 0 {
		t.Fatalf("RawMessage schema claims a Go byte-slice layout: %v", schema)
	}
	schema := SchemaOf(APIOutput{}, apiSchemaOverrides())
	response := schema["properties"].(map[string]any)["response"].(map[string]any)
	jsonSchema := response["properties"].(map[string]any)["json"].(map[string]any)
	if _, constrained := jsonSchema["items"]; constrained {
		t.Fatal("arbitrary JSON arrays were constrained to byte integers")
	}
}
