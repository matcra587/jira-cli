package envelope

import "github.com/matcra587/jira-cli/internal/core"

// TokenSource names the layer a resolved API token came from. Its own
// type because it is a closed set that a caller may branch on, and a
// bare string invites a typo that only shows up in someone's script.
type TokenSource string

const (
	// TokenSourceKeyring is the operating system credential store.
	TokenSourceKeyring TokenSource = "keyring"
	// TokenSourceOnePassword is the direct SDK credential store.
	TokenSourceOnePassword TokenSource = "1password"
	// TokenSourceEnv is JIRA_API_TOKEN or a selected JIRA_TOKEN_ reference.
	TokenSourceEnv TokenSource = "env"
	// TokenSourceFile is the config file — possible, and worth naming so
	// a caller can see a secret has ended up somewhere it should not be.
	TokenSourceFile TokenSource = "file"
)

// AuthStatusOutput is the `auth.status` payload: the account Jira says
// these credentials belong to, and where the credential came from.
//
// The account is what JIRA answered, not what was configured. Echoing
// the configured email back would make the command a mirror — it would
// report success for a token belonging to somebody else entirely.
type AuthStatusOutput struct {
	// BaseURL is the browser/site identity, separate from any verified gateway.
	BaseURL string `json:"base_url"`
	// CloudID is present only for a verified scoped gateway identity.
	CloudID string `json:"cloud_id,omitempty"`
	// Email is the configured account.
	Email string `json:"email"`
	// AccountID is the Jira account the credentials authenticated as.
	AccountID string `json:"account_id"`
	// DisplayName is that account's display name.
	DisplayName string `json:"display_name"`
	// Token is the API token, present ONLY when --show-token was passed.
	// Omitted otherwise so a routine status never carries a secret into
	// a log or a filter; when the flag IS given, it is present in every
	// output mode, because a flag that works in one mode and silently
	// does nothing in another is the behavior this CLI criticizes
	// elsewhere.
	Token string `json:"token,omitempty"`
	// TokenTail carries the real token for the human renderer to mask.
	// json:"-" is load-bearing: this is the secret, and it exists on the
	// struct only so the renderer can show the last few characters. It
	// must never reach the envelope.
	TokenTail string `json:"-"`
	// TokenSource names the layer that supplied the token. The most
	// confusing state this command can find is an environment variable
	// quietly overriding the keyring, and this is the field that shows it.
	TokenSource string `json:"token_source"`
}

// AuthLoginOutput is the verified candidate and independent credential/settings
// publication outcomes from `auth.login`.
type AuthLoginOutput struct {
	// MutationState carries dry_run on both paths: a preview verifies
	// the token and stores nothing, so a caller must be able to tell
	// which happened.
	MutationState
	// BaseURL is the browser/site identity, separate from any verified gateway.
	BaseURL string `json:"base_url"`
	// CloudID records the verified gateway scope while BaseURL stays the site.
	CloudID string `json:"cloud_id,omitempty"`
	// Email is the credential account verified against Jira.
	Email string `json:"email"`
	// Replaced names the credential this login displaced — "account on
	// site" — when it was for a different account or site. Empty
	// otherwise. Login replaces only the selected credential location;
	// the displaced credential cannot be recovered from here.
	Replaced string `json:"replaced,omitempty"`
	// AccountID and DisplayName are what Jira answered during
	// verification, independently of whether publication succeeded.
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	// Verified describes the candidate proof, independently of publication.
	Verified bool `json:"verified"`
	// Credential retains provider acknowledgment or unknown entered writes.
	Credential core.CredentialPublication `json:"credential"`
	// Settings retains actual public identity publication, including failures.
	Settings core.AccountIdentityPublication `json:"settings"`
	// LocatorPublication records a new public keyring reference saved before
	// token publication. It remains visible if a later login step fails.
	LocatorPublication *core.AccountIdentityPublication `json:"locator_publication,omitempty"`
}

// AuthLogoutOutput is the `auth.logout` payload.
type AuthLogoutOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// Email is the account whose stored token was removed.
	Email string `json:"email"`
	// EnvTokenRemains reports an actual environment token still supplied after
	// removal, including one exposed by clearing the stored record. Serialized
	// even when false: a caller
	// checking whether logout took effect needs the answer either way.
	EnvTokenRemains bool `json:"env_token_remains"`
	// RemainingSource names a layer that still supplies a token after
	// the stored one was removed — the environment, or the config file.
	// Empty when logout really did log you out. Reporting success while
	// the next command still works is this command's most misleading
	// outcome.
	RemainingSource string `json:"remaining_source,omitempty"`
	// RevocationNeeded restates that removing the stored credential is not
	// revocation. Always true today, and a field rather than prose so a
	// script cannot miss it.
	RevocationNeeded bool `json:"revocation_needed"`
}

func init() {
	Register("auth.status", AuthStatusOutput{}, map[string]any{
		"description": "The account these credentials authenticate as, verified against Jira.",
		"properties": map[string]any{
			"base_url":     map[string]any{"description": "Browser/site identity; scoped authentication is verified at the bound gateway."},
			"cloud_id":     map[string]any{"description": "Verified scoped-token gateway cloud ID; omitted for classic site routing."},
			"email":        map[string]any{"description": "Configured account."},
			"account_id":   map[string]any{"description": "Jira account ID the credentials resolved to."},
			"display_name": map[string]any{"description": "Display name of that account."},
			"token":        map[string]any{"description": "The API token itself; present only when --show-token was passed."},
			"token_source": map[string]any{"description": "Layer that supplied the token: keyring, 1password, env, or file."},
		},
	})
	Register("auth.login", AuthLoginOutput{}, map[string]any{
		"description": "Verified login and public identity publication, retaining acknowledged or unknown credential/settings effects on failure. The environment backend stores public metadata only.",
		"properties": map[string]any{
			"dry_run":      map[string]any{"description": "Whether this was a preview; the token is verified but not stored when true."},
			"base_url":     map[string]any{"description": "Browser/site identity; scoped authentication is verified at the bound gateway."},
			"cloud_id":     map[string]any{"description": "Verified scoped-token gateway cloud ID; omitted for classic site routing."},
			"email":        map[string]any{"description": "Account the token was stored for."},
			"account_id":   map[string]any{"description": "Jira account ID the token resolved to."},
			"display_name": map[string]any{"description": "Display name of that account."},
			"replaced":     map[string]any{"description": "Credential this login displaced, when it was a different account or site."},
			"verified":     map[string]any{"description": "The candidate authenticated as the reported native Jira account; independent of whether publication succeeded."},
			"credential":   map[string]any{"description": "Public selected-provider acknowledgment: prepared, not_saved, saved, unknown or external. An unknown entered write must be inspected before retrying. No secret is included."},
			"settings":     map[string]any{"description": "Actual immutable public settings candidate and publication receipt; blocked previews may verify scope without preparing a publishable candidate."},
		},
	})
	Register("auth.logout", AuthLogoutOutput{}, map[string]any{
		"description": "Result of removing the selected stored credential. For 1Password, only the owned field is removed; other clients using it also lose access.",
		"properties": map[string]any{
			"dry_run":           map[string]any{"description": "Whether this was a preview; nothing is removed when true."},
			"email":             map[string]any{"description": "Account whose stored token was removed."},
			"env_token_remains": map[string]any{"description": "Whether a nonempty environment token remains after removal, including a newly exposed fallback."},
			"remaining_source":  map[string]any{"description": "Layer still supplying a nonempty token after removal. Omitted when no token remains; a default layer label alone is not a credential."},
			"revocation_needed": map[string]any{"description": "Removing the stored credential does not revoke the token."},
		},
	})
}
