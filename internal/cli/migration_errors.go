package cli

const (
	CodeConfigMigrationUnmapped = "config_migration_unmapped"
	CodeConfigMigrationChanged  = "config_migration_changed"
	CodeConfigRecoveryInvalid   = "config_recovery_invalid"
	CodeConfigMigrationFailed   = "config_migration_failed"
)

func init() {
	codedCodes = append(codedCodes, CodeConfigMigrationUnmapped, CodeConfigMigrationChanged, CodeConfigRecoveryInvalid, CodeConfigMigrationFailed)
	registry[CodeConfigMigrationUnmapped] = spec{exit: ExitValidation, hint: "Inspect data.unmapped with config migrate --dry-run. Resolve every runtime mapping before applying; original input remains retained.", next: "jira config migrate --help", doc: "jira agent guide migrate-configuration"}
	registry[CodeConfigMigrationChanged] = spec{exit: ExitValidation, hint: "Inspect current configuration, credentials and retained journal. Recovery refuses external edits. Do not replay an interrupted apply; use explicit config rollback after resolving conflicts.", next: "jira config rollback --help", doc: "jira agent guide migrate-configuration"}
	registry[CodeConfigRecoveryInvalid] = spec{exit: ExitValidation, hint: "Verify the selected legacy input or retained journal, immutable configuration artifact and provider backups. Keep all records; do not guess missing preimages.", next: "jira config rollback --help", doc: "jira agent guide migrate-configuration"}
	registry[CodeConfigMigrationFailed] = spec{exit: ExitServer, hint: "Inspect retained effect facts and journal durability. A failed write may have completed. Keep recovery and use explicit rollback with a fresh context; never automatically replay apply.", next: "jira config rollback --help", doc: "jira agent guide migrate-configuration"}
}
