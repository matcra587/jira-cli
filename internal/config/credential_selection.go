package config

import (
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// CredentialSelection captures public provider coordinates from the same
// resolved snapshot as login identity. SDK selectors may be IDs or exact titles;
// they are not the provider-owned actual-ID snapshots used by recovery.
func CredentialSelection(resolved Resolved) core.CredentialLocation {
	selected := core.CredentialLocation{Backend: resolved.Config.SecretBackend, Account: "default"}
	switch selected.Backend {
	case core.CredentialBackendKeyring:
		if resolved.NamedProfiles || resolved.Config.CredentialID != "" {
			selected.Account = resolved.Config.CredentialID
		}
	case core.CredentialBackendOnePassword:
		selected.SDKAccount = strings.TrimSpace(resolved.Config.OnePasswordAccount)
		selected.Vault, selected.Item = resolved.Config.OnePasswordVault, resolved.Config.OnePasswordItem
	case core.CredentialBackendEnvironment:
		selected.EnvironmentKey = resolved.TokenEnvironment
	}
	return selected
}
