package core

// MigrationEffect records intent before a dependent operation. Attempted is
// nil when entry is unknown, including an interrupted file write. Acknowledgment
// describes the operation, not an atomic cross-provider transaction.
type MigrationEffect struct {
	Sequence  int    `json:"sequence"`
	Operation string `json:"operation"`
	Profile   string `json:"profile,omitempty"`
	Status    string `json:"status"`
	Attempted *bool  `json:"attempted"`
	ErrorCode string `json:"error_code,omitempty"`
}

// MigrationReceipt is public recovery evidence. It contains no config bytes,
// tokens or provider snapshots. JournalDurable distinguishes actual in-memory
// outcomes from the last successfully persisted journal after publication fails.
type MigrationReceipt struct {
	Profile          string                    `json:"profile"`
	Profiles         []MigrationProfileReceipt `json:"profiles,omitempty"`
	Status           string                    `json:"status"`
	Journal          string                    `json:"journal"`
	Configuration    string                    `json:"configuration_recovery"`
	SourceSHA256     string                    `json:"source_sha256"`
	CandidateSHA256  string                    `json:"candidate_sha256"`
	CredentialState  string                    `json:"credential_state"`
	SourceCredential CredentialLocation        `json:"source_credential"`
	TargetCredential CredentialLocation        `json:"target_credential"`
	CredentialBackup string                    `json:"credential_backup"`
	Effects          []MigrationEffect         `json:"effects"`
	JournalDurable   bool                      `json:"journal_durable"`
}

// MigrationRecoveryPaths names the explicit configuration artifact and journal.
// Resolving these names does not publish a file or create directories.
type MigrationRecoveryPaths struct {
	Directory     string `json:"directory"`
	Configuration string `json:"configuration"`
	Journal       string `json:"journal"`
}

// MigrationRollbackPreview inspects local configuration only. Credentials must
// still be opened and verified during real rollback when inspection is required.
type MigrationRollbackPreview struct {
	Receipt                      MigrationReceipt `json:"receipt"`
	WouldRestoreConfiguration    bool             `json:"would_restore_configuration"`
	CredentialInspectionRequired bool             `json:"credential_inspection_required"`
}

// MigrationProfileReceipt identifies each separately recoverable credential.
type MigrationProfileReceipt struct {
	Profile          string             `json:"profile"`
	CredentialState  string             `json:"credential_state"`
	SourceCredential CredentialLocation `json:"source_credential"`
	TargetCredential CredentialLocation `json:"target_credential"`
	CredentialBackup string             `json:"credential_backup"`
}
