package credentialstore

import (
	"github.com/gechr/clive"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// New selects a provider without opening it. Both library and CLI resolution
// use this owner so selected profiles retain the same credential locations.
func New(settings config.Config) core.CredentialStore {
	switch settings.SecretBackend {
	case core.CredentialBackendOnePassword:
		return onepassword.New(onepassword.Config{
			Account: settings.OnePasswordAccount, Vault: settings.OnePasswordVault,
			Item: settings.OnePasswordItem, IntegrationVersion: clive.Current(),
		})
	case core.CredentialBackendEnvironment:
		return core.EnvironmentCredentialStore{}
	default:
		if !settings.Keyring {
			return core.NoCredentialStore{}
		}
		return credentials.Select(keyring.New(), settings.CredentialID)
	}
}

// FromResolved prevents an empty named profile from borrowing the flat login.
func FromResolved(resolved *config.Resolved) core.CredentialStore {
	namedKeyring := resolved.NamedProfiles && resolved.Config.SecretBackend == core.CredentialBackendKeyring
	if namedKeyring && resolved.Config.CredentialID == "" {
		return core.NoCredentialStore{}
	}
	return New(resolved.Config)
}
