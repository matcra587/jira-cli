package dashboard

// These descriptions distinguish preferences from cached identity and startup
// configuration. A successful save/reload adopts a newly bound runtime.
var otherSettings = []setting{
	{key: "default_project", name: "Default project", group: "Defaults", description: "Project key used for structured lists and new issues."},
	{key: "default_issue_type", name: "Default issue type", group: "Defaults", description: "Issue type used when creating an issue without an explicit type."},
	{key: "default_board", name: "Default board", group: "Defaults", description: "Cached board scope for structured issue lists."},
	{key: "refresh_interval", name: "Refresh interval", group: "Defaults", description: "Polling preference in seconds; retained as metadata. This screen does not poll automatically."},
	{key: "team_account_ids", name: "Team account IDs", group: "Defaults", description: "JSON array of literal Jira account IDs. Does not change issue queries implicitly."},
	{key: "workday_seconds", name: "Workday duration", group: "Defaults", description: "Positive number of seconds represented by 1d in worklog input."},
	{key: "queries_path", name: "Saved queries directory", group: "Tools", description: "Directory containing named .jql searches."},
	{key: "editor", name: "External editor", group: "Tools", description: "Editor command; parsed and resolved only when an editor is requested."},
	{key: "theme_path", name: "Custom palette path", group: "Appearance", description: "Custom TOML palette path, relative to the selected config directory. Clear to use the named theme."},
	{key: "glyphs", name: "Glyph set", group: "Appearance", description: "Use default symbols or Nerd Font glyphs."},
	{key: "base_url", name: "Jira site URL", group: "Connection", description: "Jira site root, separate from the API gateway."},
	{key: "cloud_id", name: "Cloud ID", group: "Connection", description: "Optional cloud ID for scoped-token API routing."},
	{key: "email", name: "Account email", group: "Connection", description: "Account used for Jira authentication."},
	{key: "timeout", name: "Request timeout", group: "Connection", description: "Nonnegative Go duration such as 30s or 2m. 0s disables the deadline."},
	{key: "read_only", name: "Read-only Jira access", group: "Connection", description: "Refuse Jira writes at the shared transport boundary."},
	{key: "secret_backend", name: "Credential provider", group: "Authentication", description: "Choose keyring, env or 1password. Does not move or verify stored credentials."},
	{key: "keyring", name: "Enable keyring", group: "Authentication", description: "Allow native credential storage when the selected provider is keyring."},
	{key: "credential_id", name: "Credential slot", group: "Authentication", description: "Stable credential slot, independent of the profile name. Does not move credentials."},
	{key: "api_token_env", name: "Token environment variable", group: "Authentication", description: "Optional variable name to read at startup; the token value is never shown here."},
	{key: "onepassword_account", name: "1Password account", group: "Authentication", description: "Account used for desktop authorization."},
	{key: "onepassword_vault", name: "1Password vault", group: "Authentication", description: "Vault coordinate or ID for the selected credential provider."},
	{key: "onepassword_item", name: "1Password item", group: "Authentication", description: "Item coordinate or ID for the selected credential provider."},
	{key: "account_id", name: "Cached account ID", group: "Identity", description: "Cached public identity for local previews. Editing does not verify authentication."},
	{key: "account_id_site", name: "Cached identity site", group: "Identity", description: "Site binding for the cached public identity."},
	{key: "account_id_email", name: "Cached identity email", group: "Identity", description: "Email binding for the cached public identity."},
	{key: "account_id_cloud_id", name: "Cached identity cloud ID", group: "Identity", description: "API scope binding for the cached public identity."},
	{key: "api_token", name: "API token", group: "Authentication", description: "Managed by jira auth login; tokens are never shown or stored in settings."},
	{key: "aliases", name: "Command aliases", group: "Tools", description: "Use jira alias set or jira alias import for command-tree validation; jira alias list shows shortcuts."},
}

func allDashboardSettings() []setting {
	return append(append([]setting{}, dashboardSettings...), otherSettings...)
}
