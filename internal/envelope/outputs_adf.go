package envelope

import (
	"encoding/json"

	"github.com/matcra587/jira-cli/internal/adf"
)

// ADFConvertOutput retains the authored document and all conversion diagnostics.
// Diagnostics also live in data so compact mode cannot hide a downgrade.
type ADFConvertOutput struct {
	Document    json.RawMessage `json:"document"`
	Lossy       bool            `json:"lossy"`
	Diagnostics []Warning       `json:"diagnostics"`
}

// ADFRenderOutput exposes a Markdown review artifact, never an edit source.
// Document is retained only for the native human renderer; it is never serialized.
type ADFRenderOutput struct {
	Document        json.RawMessage `json:"-"`
	Markdown        string          `json:"markdown"`
	Lossy           bool            `json:"lossy"`
	LossyConstructs []string        `json:"lossy_constructs"`
}

// ADFValidateOutput reports local conformance, not Jira field acceptance.
type ADFValidateOutput struct {
	SchemaSHA256 string                     `json:"schema_sha256"`
	Valid        bool                       `json:"valid"`
	Diagnostics  []adf.ValidationDiagnostic `json:"diagnostics"`
}

// ADFLintOutput retains both schema errors and Markdown projection risks.
type ADFLintOutput struct {
	ProjectionDiagnostic string                     `json:"projection_diagnostic,omitempty"`
	ProjectionAvailable  bool                       `json:"projection_available"`
	SchemaSHA256         string                     `json:"schema_sha256"`
	Valid                bool                       `json:"valid"`
	Diagnostics          []adf.ValidationDiagnostic `json:"diagnostics"`
	Lossy                bool                       `json:"lossy"`
	LossyConstructs      []string                   `json:"lossy_constructs"`
}

// ADFExplainOutput keeps every matching context definition visible.
type ADFExplainOutput struct {
	SchemaSHA256 string             `json:"schema_sha256"`
	Entries      []adf.SupportEntry `json:"entries"`
}

// ADFEditRegion exposes immutable anchor identity and protection state.
type ADFEditRegion struct {
	ID        string `json:"id"`
	NodeType  string `json:"node_type"`
	Protected bool   `json:"protected"`
}

// ADFEditPrepareOutput pairs anchored Markdown with its native region metadata.
// Applying it requires the exact original document; the projection is not a backup.
type ADFEditPrepareOutput struct {
	Markdown string          `json:"markdown"`
	Regions  []ADFEditRegion `json:"regions"`
}

// ADFEditDiagnostic identifies a refused edit without a partial native candidate.
type ADFEditDiagnostic struct {
	Kind   string `json:"kind"`
	Region string `json:"region,omitempty"`
	Reason string `json:"reason"`
}

// ADFEditOutput carries either a preserved native candidate or edit diagnostics.
// A local candidate does not establish a server-enforced write precondition.
type ADFEditOutput struct {
	Document    json.RawMessage     `json:"document,omitempty"`
	Diagnostics []ADFEditDiagnostic `json:"diagnostics"`
}

func init() {
	Register("adf.validate", ADFValidateOutput{}, nil)
	Register("adf.lint", ADFLintOutput{}, nil)
	Register("adf.explain-node", ADFExplainOutput{}, nil)
	Register("adf.convert", ADFConvertOutput{}, map[string]any{
		"properties": map[string]any{"document": map[string]any{
			"type": "object", "description": "Native version-1 ADF from the shared Markdown body converter; validated against the pinned stable ADF schema. Extract this document for --json-input.",
		}},
	})
	Register("adf.render", ADFRenderOutput{}, nil)
	Register("adf.edit.prepare", ADFEditPrepareOutput{}, nil)
	for _, op := range []string{"adf.edit.apply", "adf.edit.merge"} {
		Register(op, ADFEditOutput{}, map[string]any{
			"properties": map[string]any{"document": map[string]any{"type": "object", "description": "Preserved native ADF candidate. Only newly authored content is schema-validated; unchanged future server state survives. This local artifact is not a remote write precondition."}},
		})
	}
}
