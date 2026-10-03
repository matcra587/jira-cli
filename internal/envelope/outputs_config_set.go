package envelope

// ConfigSetOutput describes a validated scalar setting change. PreviousPresent
// distinguishes an absent declaration from an explicitly stored empty value.
// Changed and Created describe publication; both are false during a preview.
type ConfigSetOutput struct {
	MutationState
	Profile         string `json:"profile,omitempty"`
	Path            string `json:"path"`
	Key             string `json:"key"`
	Value           string `json:"value"`
	PreviousValue   string `json:"previous_value"`
	PreviousPresent bool   `json:"previous_present"`
	PreviousSecret  bool   `json:"previous_secret"`
	WouldChange     bool   `json:"would_change"`
	Changed         bool   `json:"changed"`
	Created         bool   `json:"created"`
}

func init() {
	Register("config.set", ConfigSetOutput{}, map[string]any{
		"description": "Validated file-only scalar assignment; credentials cannot be written through this command.",
		"properties": map[string]any{
			"previous_present": map[string]any{"description": "Whether the original file declares this key, independently of its value."},
			"previous_secret":  map[string]any{"description": "Whether the original value was withheld by redaction."},
			"would_change":     map[string]any{"description": "Whether the validated assignment changes the stored declaration."},
			"changed":          map[string]any{"description": "Whether this invocation published a changed file; false during preview."},
			"created":          map[string]any{"description": "Whether publication created a previously missing file; false during preview."},
		},
	})
}
