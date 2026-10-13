package core

// CredentialPublication reports public provider effects, never the credential
// or provider error text. Unknown means an entered write was not acknowledged;
// callers must inspect the selected store before retrying it.
type CredentialPublication struct {
	Backend   CredentialBackend `json:"backend"`
	Status    string            `json:"status"`
	Attempted bool              `json:"attempted"`
	Saved     bool              `json:"saved"`
	Reference string            `json:"reference,omitempty"`
}

// LoginIdentityBinding carries the public selection that produced a verified
// candidate. A later configuration read must not substitute another provider
// or environment reference before publishing its account identity.
type LoginIdentityBinding struct {
	AccountIdentityScope
	CredentialLocation
}
