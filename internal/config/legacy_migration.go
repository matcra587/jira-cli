package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	xfilepath "github.com/gechr/x/filepath"
	xstrings "github.com/gechr/x/strings"
	"github.com/go-viper/mapstructure/v2"
	koanftoml "github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// ErrLegacyConfig identifies an invalid or unrecognized v0.15 configuration.
// Error messages never quote parser input, which can contain private material.
var ErrLegacyConfig = errors.New("invalid legacy configuration")

// ErrLegacyPreferencesUnmapped refuses publication of a partial conversion.
var ErrLegacyPreferencesUnmapped = errors.New("legacy preferences require additional mappings before migration")

type legacyConfigFailure struct{ cause error }

func (*legacyConfigFailure) Error() string   { return ErrLegacyConfig.Error() }
func (e *legacyConfigFailure) Unwrap() error { return errors.Join(ErrLegacyConfig, e.cause) }

// LegacySelection retains the old credential identity independently of the new
// normalized site. In particular, old keyring host spelling is case-sensitive.
type LegacySelection struct {
	Name, Site, Email string
	CloudID           string
	Backend           core.CredentialBackend
	Account, Vault    string
	Item, Host        string
}

// KeyringName is v0.15.2's exact site-host/profile namespace, not v0.16's slot.
func (s LegacySelection) KeyringName() string { return s.Host + "/" + s.Name }

// EnvTokenKey preserves v0.15's historical hyphen/underscore profile encoding.
// It names an override; preparing migration never reads the variable's value.
func (s LegacySelection) EnvTokenKey() string {
	encoded := strings.NewReplacer("_", "__", "-", "_").Replace(s.Name)
	return "JIRA_TOKEN_" + strings.ToUpper(encoded)
}

// LegacyMigration is an immutable, credential-free preparation. Its candidate
// contains supported preferences only; Unmapped must be resolved before apply.
// The complete original remains available for the transaction's recovery owner.
type LegacyMigration struct {
	selection LegacySelection
	original  []byte
	candidate []byte
	mapped    []string
	unmapped  []string
	deferred  []string
	version   int
}

// Version 1 is the delivered preparation contract. Later mappings must retain
// its exact candidate derivation so existing immutable recovery remains usable.
const (
	legacyMigrationVersion            = 8
	legacyMigrationDeferredTUIVersion = 9
)

// LegacyMigrationOptions selects explicit retention of preferences outside the CLI runtime.
// Deferred TUI declarations remain only in the original recovery bytes.
type LegacyMigrationOptions struct {
	DeferTUI bool
}

func (m *LegacyMigration) Selection() LegacySelection { return m.selection }
func (m *LegacyMigration) Original() []byte           { return bytes.Clone(m.original) }
func (m *LegacyMigration) Candidate() ([]byte, error) {
	if m.selection.Name == "" {
		return nil, ErrLegacyConfig
	}
	if len(m.unmapped) != 0 {
		return nil, ErrLegacyPreferencesUnmapped
	}
	return bytes.Clone(m.candidate), nil
}
func (m *LegacyMigration) Mapped() []string   { return slices.Clone(m.mapped) }
func (m *LegacyMigration) Unmapped() []string { return slices.Clone(m.unmapped) }

// Deferred returns names retained for a future TUI migration, without their values.
func (m *LegacyMigration) Deferred() []string { return slices.Clone(m.deferred) }

func (m *LegacyMigration) SourceSHA256() string {
	digest := sha256.Sum256(m.original)
	return hex.EncodeToString(digest[:])
}

// LegacyProfileNames returns validated public names for local completion. It
// does not select a runtime profile or read settings, environment or credentials.
func LegacyProfileNames(ctx context.Context, data []byte) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	legacy, _, err := decodeLegacyMigration(data)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(legacy.Profiles))
	for _, profile := range legacy.Profiles {
		if !legacyProfileNamespace(profile.Name) {
			return nil, ErrLegacyConfig
		}
		names = append(names, profile.Name)
	}
	slices.Sort(names)
	return names, ctx.Err()
}

// PrepareLegacyMigration decodes v0.15.2's nested TOML without environment,
// defaults from the current version, file reads, or credentials. sourcePath is
// used only to retain the origin of a relative custom-theme declaration.
// A blank profile selects the legacy default; no profiles are merged.
func PrepareLegacyMigration(ctx context.Context, data []byte, sourcePath, profile string) (*LegacyMigration, error) {
	return PrepareLegacyMigrationWithOptions(ctx, data, sourcePath, profile, LegacyMigrationOptions{})
}

// PrepareLegacyMigrationWithOptions prepares the same CLI mappings with explicit TUI deferral.
func PrepareLegacyMigrationWithOptions(ctx context.Context, data []byte, sourcePath, profile string, options LegacyMigrationOptions) (*LegacyMigration, error) {
	version := legacyMigrationVersion
	if options.DeferTUI {
		version = legacyMigrationDeferredTUIVersion
	}
	return prepareLegacyMigration(ctx, data, sourcePath, profile, version)
}

func prepareLegacyMigration(ctx context.Context, data []byte, sourcePath, profile string, version int) (*LegacyMigration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version < 1 || version > legacyMigrationDeferredTUIVersion {
		return nil, ErrLegacyConfig
	}
	legacy, fields, err := decodeLegacyMigration(data)
	if err != nil {
		return nil, err
	}
	if profile == "" {
		profile = legacy.DefaultProfile
	}
	selected := -1
	for index, candidate := range legacy.Profiles {
		if candidate.Name == profile {
			selected = index
			break
		}
	}
	if selected < 0 || !legacyProfileNamespace(profile) {
		return nil, fmt.Errorf("%w: selected profile is missing or has an unsafe credential namespace", ErrLegacyConfig)
	}
	p := legacy.Profiles[selected]
	profileFields := legacyProfileFields(fields, selected)
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return nil, ErrLegacyConfig
	}
	host := legacyCredentialHost(u)
	item := p.Item
	if item == "" {
		item = "jira-cli-" + profile
		if host != "" {
			item = "jira-cli-" + host + "-" + profile
		}
	}
	m := &LegacyMigration{version: version, original: bytes.Clone(data), selection: LegacySelection{
		Name: profile, Site: credentials.NormalizeSite(p.BaseURL), Email: strings.TrimSpace(p.Email),
		Backend: core.CredentialBackend(p.SecretBackend), Account: p.OnePasswordAccount,
		Vault: p.Vault, Item: item, Host: host,
	}}
	if version >= 3 {
		if err := core.ValidateCloudID(p.CloudID); err != nil {
			return nil, fmt.Errorf("%w: invalid selected cloud routing", ErrLegacyConfig)
		}
		m.selection.CloudID = strings.TrimSpace(p.CloudID)
	}
	next := map[string]any{
		"base_url": m.selection.Site, "email": m.selection.Email, "secret_backend": p.SecretBackend,
		"read_only": p.ReadOnly, "timeout": (time.Duration(p.Timeout) * time.Second).String(),
		"workday_seconds": p.WorkdaySeconds,
	}
	if version >= 2 {
		next["api_token_env"] = m.selection.EnvTokenKey()
	}
	if version >= 3 {
		next["cloud_id"] = m.selection.CloudID
	}
	if version >= 4 {
		next["default_project"], next["default_issue_type"], next["default_board"] = p.DefaultProject, p.DefaultIssueType, p.DefaultBoard
	}
	if version >= 6 {
		interval := p.RefreshInterval
		if interval <= 0 {
			interval = defaultRefreshInterval
		}
		next["refresh_interval"] = interval
		if _, present := profileFields["team_account_ids"]; present {
			team := slices.Clone(p.TeamAccountIDs)
			if team == nil {
				team = []string{}
			}
			next["team_account_ids"] = team
		}
	}
	if version >= 7 {
		if _, present := profileFields["account_id"]; present {
			next["account_id"], next["account_id_site"] = p.AccountID, m.selection.Site
			next["account_id_email"], next["account_id_cloud_id"] = m.selection.Email, m.selection.CloudID
		}
	}
	_, accountDeclared := profileFields["onepassword_account"]
	_, vaultDeclared := profileFields["vault"]
	_, itemDeclared := profileFields["item"]
	if p.SecretBackend == string(core.CredentialBackendOnePassword) || accountDeclared || vaultDeclared || itemDeclared {
		next["onepassword_account"], next["onepassword_vault"], next["onepassword_item"] = p.OnePasswordAccount, p.Vault, item
	}
	if _, present := fields["queries_path"]; present {
		next["queries_path"] = legacy.QueriesPath // both query loaders resolve relative paths from the working directory
	}
	if _, present := fields["aliases"]; present {
		aliases := maps.Clone(legacy.Aliases)
		if aliases == nil {
			aliases = map[string]string{}
		}
		next["aliases"] = aliases
	}
	_, globalEditorDeclared := fields["editor"]
	_, profileEditorDeclared := profileFields["editor"]
	if globalEditorDeclared || profileEditorDeclared {
		next["editor"] = strings.TrimSpace(legacy.Editor)
		if editor := strings.TrimSpace(p.Editor); editor != "" {
			next["editor"] = editor
		}
	}
	if themeFields, ok := fields["theme"].(map[string]any); ok {
		if _, present := themeFields["name"]; present {
			next["theme"] = legacy.Theme.Name
			if version < 5 && IsAutomaticTheme(legacy.Theme.Name) {
				m.unmapped = append(m.unmapped, "theme.name")
			}
		}
		if _, present := themeFields["path"]; present {
			path := legacy.Theme.Path
			homePath := path == "~" || strings.HasPrefix(path, "~/")
			variablePath := strings.Contains(path, "$")
			if version < 8 && (variablePath || homePath) {
				m.unmapped = append(m.unmapped, "theme.path")
			}
			// v0.15 stored literal path text. Policy 8 preserves those names;
			// expanding them would redirect a palette through unrelated process
			// state. Earlier policies retain their original refusal/derivation.
			if path != "" && !filepath.IsAbs(path) && (version >= 8 || !homePath && !variablePath) {
				if sourcePath == "" || !filepath.IsAbs(sourcePath) {
					return nil, fmt.Errorf("%w: relative theme path requires an absolute source path", ErrLegacyConfig)
				}
				path = xfilepath.Rebase(filepath.Dir(sourcePath), path)
			}
			next["theme_path"] = path
		}
	}
	for _, key := range []string{"default_project", "default_issue_type", "default_board", "refresh_interval", "cloud_id", "team_account_ids", "account_id"} {
		if version >= 7 && key == "account_id" {
			continue
		}
		if version >= 6 && (key == "refresh_interval" || key == "team_account_ids") {
			continue
		}
		if key == "cloud_id" && version >= 3 || version >= 4 && (key == "default_project" || key == "default_issue_type" || key == "default_board") {
			continue
		}
		if _, present := profileFields[key]; present {
			m.unmapped = append(m.unmapped, "profiles."+profile+"."+key)
		}
	}
	if tui, ok := fields["tui"].(map[string]any); ok {
		for key := range tui {
			if version >= legacyMigrationDeferredTUIVersion {
				m.deferred = append(m.deferred, "tui."+key)
			} else {
				m.unmapped = append(m.unmapped, "tui."+key)
			}
		}
	}
	m.mapped = slices.Sorted(maps.Keys(next))
	slices.Sort(m.unmapped)
	slices.Sort(m.deferred)
	m.candidate, err = koanftoml.Parser().Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("%w: encode candidate", ErrLegacyConfig)
	}
	if err := ValidateFile(m.candidate); err != nil {
		return nil, fmt.Errorf("%w: converted preferences are invalid", ErrLegacyConfig)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeLegacyMigration(data []byte) (*legacyMigrationConfig, map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, nil, ErrLegacyConfig
	}
	fields, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		return nil, nil, &legacyConfigFailure{cause: err}
	}
	fields, err = canonicalLegacyFields(fields)
	if err != nil {
		return nil, nil, err
	}
	k := koanf.New(delim)
	if err := k.Load(confmap.Provider(fields, ""), nil); err != nil {
		return nil, nil, &legacyConfigFailure{cause: err}
	}
	var legacy legacyMigrationConfig
	if err := k.UnmarshalWithConf("", &legacy, koanf.UnmarshalConf{
		Tag: "koanf", DecoderConfig: &mapstructure.DecoderConfig{
			WeaklyTypedInput: true, ErrorUnused: true, Result: &legacy,
			// ASCII schema keys were canonicalized above. Refuse Unicode fold
			// aliases instead of accepting declarations tracking cannot see.
			MatchName: func(mapKey, fieldName string) bool { return mapKey == fieldName },
		},
	}); err != nil {
		return nil, nil, &legacyConfigFailure{cause: err}
	}
	if legacy.DefaultProfile == "" {
		legacy.DefaultProfile = "default"
	}
	if len(legacy.Profiles) == 0 {
		legacy.Profiles = []legacyMigrationProfile{{Name: "default"}}
	}
	seen := make(map[string]bool, len(legacy.Profiles))
	for index := range legacy.Profiles {
		p := &legacy.Profiles[index]
		if p.Name == "" || seen[p.Name] || p.AuthType != "" && p.AuthType != "token" {
			return nil, nil, ErrLegacyConfig
		}
		seen[p.Name] = true
		if p.BaseURL != "" && ValidateBaseURL(p.BaseURL) != nil {
			return nil, nil, ErrLegacyConfig
		}
		if p.CloudID != "" && (strings.TrimSpace(p.CloudID) == "" || strings.ContainsFunc(strings.TrimSpace(p.CloudID), func(r rune) bool {
			return !xstrings.IsAlphanumericChar(r) && r != '-' && r != '_' && r != '.'
		})) {
			return nil, nil, ErrLegacyConfig
		}
		if p.SecretBackend == "" {
			p.SecretBackend = string(core.CredentialBackendKeyring)
		}
		if !slices.Contains([]string{"keyring", "env", "1password"}, p.SecretBackend) {
			return nil, nil, ErrLegacyConfig
		}
		if p.Timeout <= 0 {
			p.Timeout = 30
		}
		if p.Timeout > int64((1<<63-1)/time.Second) {
			return nil, nil, ErrLegacyConfig
		}
		if p.WorkdaySeconds <= 0 {
			p.WorkdaySeconds = 28800
		}
	}
	if !seen[legacy.DefaultProfile] {
		return nil, nil, ErrLegacyConfig
	}
	return &legacy, fields, nil
}

func legacyProfileNamespace(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func legacyProfileFields(fields map[string]any, selected int) map[string]any {
	profiles, ok := fields["profiles"].([]any)
	if !ok || selected >= len(profiles) {
		return nil
	}
	profile, _ := profiles[selected].(map[string]any)
	return profile
}

// legacyCredentialHost reproduces the old NormalizeBaseURL/siteHost lookup.
// That implementation discarded ports while expanding single-label hosts;
// fixing the spelling here would look up a different historical credential.
func legacyCredentialHost(u *url.URL) string {
	host := u.Hostname()
	if host == "" {
		return u.Host
	}
	if !strings.Contains(host, ".") {
		switch strings.ToLower(host) {
		case "localhost", "127.0.0.1", "::1":
		default:
			u.Host = host + ".atlassian.net"
			if port := u.Port(); port != "" {
				u.Host += ":" + port
			}
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	normalized := u.String()
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return normalized
	}
	return parsed.Host
}
