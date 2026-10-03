package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// AuthWhoamiOutput retains Jira's native current account without copying
// configured identity values into privacy-hidden or absent server fields.
type AuthWhoamiOutput struct {
	BaseURL string           `json:"base_url"`
	User    jira.UserDetails `json:"user"`
}

func init() {
	registerWhoami("auth.whoami")
	registerWhoami("me")
}

func registerWhoami(op string) {
	Register(op, AuthWhoamiOutput{}, map[string]any{
		"description": "The complete native current Jira account, fetched using the resolved credentials.",
		"properties": map[string]any{
			"base_url": map[string]any{"description": "Site the account was fetched from."},
			"user":     map[string]any{"description": "Native Jira account, preserving absent, null and unknown fields."},
		},
	})
}
