package envelope

import "github.com/matcra587/jira-cli/internal/core"

// AuthWhoamiSaveOutput preserves the complete verified native account and
// separately reports public scope and actual local settings publication.
type AuthWhoamiSaveOutput struct {
	MutationState
	AuthWhoamiOutput
	Scope    core.AccountIdentityScope       `json:"scope"`
	Settings core.AccountIdentityPublication `json:"settings"`
}

func init() {
	Register("auth.whoami.save", AuthWhoamiSaveOutput{}, map[string]any{
		"description": "Verify the current native Jira account and save independently bound public cached identity.",
		"properties": map[string]any{
			"dry_run":  map[string]any{"description": "Verifies Jira and prepares the same candidate without publishing any settings when true."},
			"base_url": map[string]any{"description": "Browser/site identity used for the native identity read."},
			"user":     map[string]any{"description": "Complete native Jira account; privacy-hidden, absent, null and unknown fields remain intact."},
			"scope":    map[string]any{"description": "Independent public site, credential account and cloud binding; never inferred from a privacy-hidden native email."},
			"settings": map[string]any{"description": "Prepared intent and actual local publication. saved confirms durable success; unknown does not assert that an unsuccessful write did nothing; written reports acknowledged creation with failed durability."},
		},
	})
}
