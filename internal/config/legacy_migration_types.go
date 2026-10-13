package config

// These decode-only types match the public v0.15.2 configuration shape. They
// are not another runtime profile system and deliberately contain no token.
type legacyMigrationConfig struct {
	DefaultProfile string                   `koanf:"default_profile"`
	Profiles       []legacyMigrationProfile `koanf:"profiles"`
	Theme          legacyMigrationTheme     `koanf:"theme"`
	TUI            legacyMigrationTUI       `koanf:"tui"`
	QueriesPath    string                   `koanf:"queries_path"`
	Aliases        map[string]string        `koanf:"aliases"`
	Editor         string                   `koanf:"editor"`
}

type legacyMigrationProfile struct {
	Name               string   `koanf:"name"`
	BaseURL            string   `koanf:"base_url"`
	AuthType           string   `koanf:"auth_type"`
	Email              string   `koanf:"email"`
	DefaultProject     string   `koanf:"default_project"`
	DefaultIssueType   string   `koanf:"default_issue_type"`
	DefaultBoard       string   `koanf:"default_board"`
	RefreshInterval    int      `koanf:"refresh_interval"`
	Timeout            int64    `koanf:"timeout"`
	WorkdaySeconds     int      `koanf:"workday_seconds"`
	SecretBackend      string   `koanf:"secret_backend"`
	CloudID            string   `koanf:"cloud_id"`
	OnePasswordAccount string   `koanf:"onepassword_account"`
	Vault              string   `koanf:"vault"`
	Item               string   `koanf:"item"`
	TeamAccountIDs     []string `koanf:"team_account_ids"`
	AccountID          string   `koanf:"account_id"`
	ReadOnly           bool     `koanf:"read_only"`
	Editor             string   `koanf:"editor"`
}

type legacyMigrationTheme struct {
	Name string `koanf:"name"`
	Path string `koanf:"path"`
}

type legacyMigrationTUI struct {
	RefreshInterval int                         `koanf:"refresh_interval"`
	DefaultTab      string                      `koanf:"default_tab"`
	Tabs            []string                    `koanf:"tabs"`
	Preview         string                      `koanf:"preview"`
	Sections        []legacyMigrationTUISection `koanf:"sections"`
	PreviewSize     int                         `koanf:"preview_size"`
	Keys            map[string][]string         `koanf:"keys"`
	Lenses          []legacyMigrationTUISection `koanf:"lenses"`
	DefaultLens     string                      `koanf:"default_lens"`
	Icons           string                      `koanf:"icons"`
}

type legacyMigrationTUISection struct {
	Title string `koanf:"title"`
	JQL   string `koanf:"jql"`
}
