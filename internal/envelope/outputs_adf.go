package envelope

import "encoding/json"

// ADFConvertOutput retains the authored document and all conversion diagnostics.
// Diagnostics also live in data so compact mode cannot hide a downgrade.
type ADFConvertOutput struct {
	Document    json.RawMessage `json:"document"`
	Lossy       bool            `json:"lossy"`
	Diagnostics []Warning       `json:"diagnostics"`
}

// ADFRenderOutput is a lossy Markdown review artifact, never an edit source.
type ADFRenderOutput struct {
	Markdown        string   `json:"markdown"`
	Lossy           bool     `json:"lossy"`
	LossyConstructs []string `json:"lossy_constructs"`
}

func init() {
	Register("adf.convert", ADFConvertOutput{}, map[string]any{
		"properties": map[string]any{"document": map[string]any{
			"type": "object", "description": "Native version-1 ADF from the shared Markdown body converter; validated against the pinned stable ADF schema. Extract this document for --json-input.",
		}},
	})
	Register("adf.render", ADFRenderOutput{}, nil)
}
