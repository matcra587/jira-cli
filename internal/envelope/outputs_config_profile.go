package envelope

// ConfigProfile describes authored public metadata, not authentication status.
type ConfigProfile struct {
	Name          string `json:"name"`
	Default       bool   `json:"default"`
	Selected      bool   `json:"selected"`
	BaseURL       string `json:"base_url"`
	Email         string `json:"email"`
	SecretBackend string `json:"secret_backend"`
}

// ConfigProfileListOutput never requires a credential provider.
type ConfigProfileListOutput struct {
	Path       string          `json:"path"`
	FileExists bool            `json:"file_exists"`
	Profiles   []ConfigProfile `json:"profiles"`
}

// ConfigProfileChangeOutput distinguishes a prepared candidate from publication.
type ConfigProfileChangeOutput struct {
	MutationState
	Path           string `json:"path"`
	Name           string `json:"name"`
	PreviousName   string `json:"previous_name,omitempty"`
	DefaultProfile string `json:"default_profile"`
	WouldChange    bool   `json:"would_change"`
	Changed        bool   `json:"changed"`
	Status         string `json:"status"`
}

func init() {
	Register("config.profile.list", ConfigProfileListOutput{}, map[string]any{
		"description": "Configured profiles and public identity; listing does not open credentials or verify authentication.",
	})
	for _, operation := range []string{"add", "rename", "default", "remove"} {
		Register("config.profile."+operation, ConfigProfileChangeOutput{}, map[string]any{
			"description": "Local profile metadata change; credentials are not read, moved or removed.",
		})
	}
}
