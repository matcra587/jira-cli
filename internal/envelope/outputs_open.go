package envelope

// IssueOpenOutput reports a resolved issue URL and browser process handoff.
// It contains no fetched fields and does not assert the issue exists remotely.
type IssueOpenOutput struct {
	Issue IssueRef `json:"issue"`
	WebLink
	Opened bool `json:"opened"`
}

func init() {
	Register("open", IssueOpenOutput{}, map[string]any{
		"description": "One issue's locally resolved browser URL and process handoff.",
		"properties": map[string]any{
			"web_url": map[string]any{"description": "The browse URL resolved from the configured site and one explicit issue key."},
			"opened":  map[string]any{"description": "True only after a browser process starts successfully; this does not prove page loading or issue existence."},
		},
	})
}
