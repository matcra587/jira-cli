package envelope

import "github.com/matcra587/jira-cli/internal/core"

// ConfigMigrateOutput reports selected legacy metadata and retained effect facts.
// Raw configuration, credential snapshots and provider keys never enter output.
type ConfigMigrateOutput struct {
	MutationState
	SourcePath        string                      `json:"source_path"`
	Path              string                      `json:"path"`
	Profile           string                      `json:"profile"`
	Profiles          []string                    `json:"profiles"`
	Backend           core.CredentialBackend      `json:"backend"`
	SourceCredential  core.CredentialLocation     `json:"source_credential"`
	TargetCredential  core.CredentialLocation     `json:"target_credential"`
	SourceSHA256      string                      `json:"source_sha256"`
	Mapped            []string                    `json:"mapped"`
	Unmapped          []string                    `json:"unmapped"`
	Deferred          []string                    `json:"deferred"`
	Ready             bool                        `json:"ready"`
	Recovery          core.MigrationRecoveryPaths `json:"recovery"`
	RecoveryPublished bool                        `json:"recovery_published"`
	Receipt           *core.MigrationReceipt      `json:"receipt"`
}

// ConfigRollbackOutput distinguishes local preview from credential verification
// and actual restoration. Recovery remains retained after every outcome.
type ConfigRollbackOutput struct {
	MutationState
	Journal                      string                 `json:"journal"`
	WouldRestoreConfiguration    bool                   `json:"would_restore_configuration"`
	CredentialInspectionRequired bool                   `json:"credential_inspection_required"`
	Restored                     bool                   `json:"restored"`
	Receipt                      *core.MigrationReceipt `json:"receipt"`
}

func init() {
	Register("config.migrate", ConfigMigrateOutput{}, map[string]any{
		"description": "Convert all legacy profiles or a flat configuration into named profiles with protected recovery and per-resource provider backups.",
		"properties": map[string]any{
			"deferred":           map[string]any{"description": "Recognized TUI declaration names explicitly retained in original source/recovery bytes for later migration; values never enter runtime settings or output."},
			"ready":              map[string]any{"description": "Whether every declaration is mapped or explicitly deferred; deferred preferences are not active and previews never verify credentials."},
			"recovery_published": map[string]any{"description": "Whether the configuration recovery artifact was published, even if journal creation later failed."},
			"receipt":            map[string]any{"description": "Public transaction evidence when a journal exists; null for preparation or artifact-only publication."},
		},
	})
	Register("config.rollback", ConfigRollbackOutput{}, map[string]any{
		"description": "Restore exact configuration and provider preimages after checking current expected states, retaining recovery records.",
		"properties": map[string]any{
			"credential_inspection_required": map[string]any{"description": "Whether real rollback must inspect credentials; dry-run never opens a provider."},
			"restored":                       map[string]any{"description": "Whether this invocation acknowledged completion and journal durability; false for previews and incomplete outcomes."},
		},
	})
}
