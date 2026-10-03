package envelope

// GuideExportArtifact describes a rendered artifact and its publication state.
type GuideExportArtifact struct {
	Slug    string `json:"slug"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Written bool   `json:"written"`
}

// GuideExportOutput is the local preparation and publication receipt.
type GuideExportOutput struct {
	MutationState
	ContractVersion string                `json:"contract_version"`
	Format          string                `json:"format"`
	Directory       string                `json:"directory"`
	Scope           string                `json:"scope,omitempty"`
	Harness         string                `json:"harness,omitempty"`
	Artifacts       []GuideExportArtifact `json:"artifacts"`
	WrittenPaths    []string              `json:"written_paths"`
	Attempted       bool                  `json:"attempted"`
	AttemptedPath   string                `json:"attempted_path,omitempty"`
}

func init() {
	Register("agent.export", GuideExportOutput{}, map[string]any{
		"properties": map[string]any{
			"format":        map[string]any{"enum": []any{"agent-skill", "claude-skill"}},
			"written_paths": map[string]any{"description": "Published paths relative to directory, in canonical guide order, including retained partial outcomes."},
			"attempted":     map[string]any{"description": "Whether filesystem publication was invoked; directory creation may precede the first file."},
			"artifacts":     map[string]any{"description": "Complete prepared corpus: source slug, relative export path, exact content digest and bytes, and whether each file was published."},
		},
	})
}
