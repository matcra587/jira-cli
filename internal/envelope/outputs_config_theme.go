package envelope

// ConfigThemeOutput reports stored theme selection and actual publication.
// Name and ThemePath never include environment or built-in default values.
type ConfigThemeOutput struct {
	MutationState
	Path        string `json:"path"`
	FileExists  bool   `json:"file_exists"`
	Name        string `json:"name"`
	NamePresent bool   `json:"name_present"`
	ThemePath   string `json:"theme_path"`
	PathPresent bool   `json:"path_present"`
	WouldChange bool   `json:"would_change"`
	Changed     bool   `json:"changed"`
	Created     bool   `json:"created"`
}

func init() {
	Register("config.theme", ConfigThemeOutput{}, map[string]any{
		"description": "File-only theme inspection or validated combined name/path publication, without Jira or credentials.",
		"properties": map[string]any{
			"name_present": map[string]any{"description": "Whether the file/candidate declares the preset name, independently of an empty value."},
			"path_present": map[string]any{"description": "Whether the file/candidate declares the custom palette path, independently of an empty value."},
			"would_change": map[string]any{"description": "Whether the validated candidate changes either stored declaration."},
			"changed":      map[string]any{"description": "Whether this invocation published a changed settings file; false during inspection or preview."},
			"theme_path":   map[string]any{"description": "Stored custom palette path; a newly selected nonempty path is stored absolute."},
		},
	})
}
