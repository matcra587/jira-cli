package envelope

// AliasListOutput exposes literal user-defined names and expansions. The map
// is a local, user-defined boundary, rather than a fixed-schema nested object.
type AliasListOutput struct {
	Path    string            `json:"path"`
	Aliases map[string]string `json:"aliases"`
	Count   int               `json:"count"`
}

// AliasSetOutput preserves the authored expansion and any previous value.
type AliasSetOutput struct {
	MutationState
	Path      string  `json:"path"`
	Name      string  `json:"name"`
	Expansion string  `json:"expansion"`
	Previous  *string `json:"previous,omitempty"`
	Changed   bool    `json:"changed"`
}

// AliasDeleteOutput reports existence on both preview and live paths. Changed
// is true only after an acknowledged file update, never during a preview.
type AliasDeleteOutput struct {
	MutationState
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Previous *string `json:"previous,omitempty"`
	Deleted  bool    `json:"deleted"`
	Changed  bool    `json:"changed"`
}

// AliasImportOutput reports accepted names and literal per-name skip reasons.
// Imported counts the proposed merge when DryRun is true. Skipped is a
// user-defined name map; no opaque fixed object is hidden in that boundary.
type AliasImportOutput struct {
	MutationState
	Path     string            `json:"path"`
	Imported int               `json:"imported"`
	Aliases  []string          `json:"aliases"`
	Skipped  map[string]string `json:"skipped"`
	Changed  bool              `json:"changed"`
}

func init() {
	Register("alias.list", AliasListOutput{}, map[string]any{"description": "Literal aliases in the selected local config file."})
	Register("alias.set", AliasSetOutput{}, map[string]any{"description": "Validated alias upsert, or a local preview."})
	Register("alias.delete", AliasDeleteOutput{}, map[string]any{"description": "Alias removal, including missing-name and local-preview outcomes."})
	Register("alias.import", AliasImportOutput{}, map[string]any{"description": "Validated YAML merge with accepted names and per-name skip reasons."})
}
