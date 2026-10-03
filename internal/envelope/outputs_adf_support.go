package envelope

import "github.com/matcra587/jira-cli/internal/adf"

// ADFSupportOutput is local implementation discovery, not server metadata.
type ADFSupportOutput struct {
	SchemaSHA256 string             `json:"schema_sha256"`
	OfficialURL  string             `json:"official_url"`
	Entries      []adf.SupportEntry `json:"entries"`
	Unknown      ADFUnknownSupport  `json:"unknown"`
}

// ADFUnknownSupport separates preservation from authoring/validation support.
type ADFUnknownSupport struct {
	PreserveNative bool   `json:"preserve_native"`
	Validate       bool   `json:"validate"`
	Edit           bool   `json:"edit"`
	NodeProjection string `json:"node_projection"`
	MarkProjection string `json:"mark_projection"`
}

func init() { Register("agent.adf-matrix", ADFSupportOutput{}, nil) }
