package cli

const (
	// CodeUpdateUnsupported means this executable has no supported update owner.
	CodeUpdateUnsupported = "update_unsupported"
	// CodeUpdateFailed retains a failed discovery, download or installation.
	CodeUpdateFailed = "update_failed"
	// CodeUpdateOwnership refuses an unowned Homebrew installation.
	CodeUpdateOwnership = "update_ownership"
	// CodeUpdateIntegrity refuses bytes failing release integrity verification.
	CodeUpdateIntegrity = "update_integrity"
	// CodeUpdateChanged refuses an intervening executable replacement.
	CodeUpdateChanged = "update_changed"
	// CodeUpdateUnavailable means no published release was found.
	CodeUpdateUnavailable = "update_unavailable"
)
