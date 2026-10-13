package core

import "context"

// AccountIdentityScope binds public cached identity to the credential account,
// browser site and API route. A privacy-hidden native email is not this account.
type AccountIdentityScope struct {
	BaseURL string `json:"base_url"`
	Email   string `json:"email"`
	CloudID string `json:"cloud_id"`
}

// AccountIdentityTarget owns a checked, immutable local target before remote
// verification. Prepare validates the native account ID without publishing it.
type AccountIdentityTarget interface {
	Prepare(accountID string) (AccountIdentityUpdate, error)
}

// AccountIdentityUpdate owns one prepared settings candidate. Its receipt
// exposes public publication evidence, never file contents or credentials.
type AccountIdentityUpdate interface {
	Receipt() AccountIdentityPublication
	Save(context.Context) (AccountIdentityPublication, error)
}

// AccountIdentityPublication separates preview intent, acknowledged changes and
// durable success. Status unknown does not assert that a failed write did nothing.
type AccountIdentityPublication struct {
	Path        string `json:"path"`
	WouldChange bool   `json:"would_change"`
	Changed     bool   `json:"changed"`
	Created     bool   `json:"created"`
	Saved       bool   `json:"saved"`
	Status      string `json:"status"`
}
