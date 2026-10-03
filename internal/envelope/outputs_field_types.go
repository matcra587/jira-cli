package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// FieldTypesOutput distinguishes local native-value handling from server metadata.
type FieldTypesOutput struct {
	Types       []jira.FieldTypeExample `json:"types"`
	Handling    FieldTypeHandling       `json:"handling"`
	Metadata    []FieldMetadataRoute    `json:"metadata"`
	OfficialURL string                  `json:"official_url"`
}

// FieldTypeHandling describes guarantees shared by known and unknown field types.
type FieldTypeHandling struct {
	InputFlag        string `json:"input_flag"`
	Encoding         string `json:"encoding"`
	PreserveValues   bool   `json:"preserve_values"`
	TypeValidation   bool   `json:"type_validation"`
	ScalarConversion bool   `json:"scalar_conversion"`
	UnknownTypes     string `json:"unknown_types"`
	NullValues       string `json:"null_values"`
	EmptyArrays      string `json:"empty_arrays"`
}

// FieldMetadataRoute is an authenticated read available through jira api get.
type FieldMetadataRoute struct {
	Purpose string `json:"purpose"`
	Path    string `json:"path"`
}

func init() { Register("agent.fieldtypes", FieldTypesOutput{}, nil) }
