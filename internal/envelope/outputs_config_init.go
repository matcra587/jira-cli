package envelope

// ConfigInitOutput reports explicit public account metadata and whether a new
// configuration was published. Credentials are never initialization inputs.
type ConfigInitOutput struct {
	MutationState
	Path        string `json:"path"`
	BaseURL     string `json:"base_url"`
	Email       string `json:"email"`
	WouldCreate bool   `json:"would_create"`
	Created     bool   `json:"created"`
}

func init() {
	Register("config.init", ConfigInitOutput{}, map[string]any{
		"description": "Initialization of a new configuration from explicit public account metadata, without credential access or environment persistence.",
		"properties": map[string]any{
			"would_create": map[string]any{"description": "Whether the validated candidate would create the absent selected file."},
			"created":      map[string]any{"description": "Whether this invocation published a complete new configuration, including when a later directory sync failed."},
		},
	})
}
