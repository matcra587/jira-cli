package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	koanftoml "github.com/knadh/koanf/parsers/toml"

	"github.com/matcra587/jira-cli/internal/core"
)

// ProfileFormatVersion identifies the named-profile document format.
const ProfileFormatVersion int64 = 1

type profileDocument struct {
	fields         map[string]any
	profiles       map[string]any
	defaultProfile string
	named          bool
}

func globalSetting(key string) bool {
	switch key {
	case "theme", "theme_path", "glyphs", "queries_path", "editor", "aliases", "tui_preview", "tui_status", "tui_assignee", "tui_labels", "tui_priority", "tui_issue_type", "tui_compact", "tui_updated", "tui_auto_page", "tui_preview_dock", "tui_preview_size", "tui_hierarchy_query", "tui_refresh_interval", "tui_refresh_on_focus", "tui_change_markers", "tui_workspace":
		return true
	default:
		return false
	}
}

// ValidateProfileName keeps identifiers portable between TOML, environment
// selection and command arguments. Names are literal and case-sensitive.
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: profile name must contain ASCII letters, digits, underscores or hyphens", ErrFileInvalid)
	}
	for _, char := range name {
		letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		digit := char >= '0' && char <= '9'
		if !letter && !digit && char != '_' && char != '-' {
			return fmt.Errorf("%w: profile name must contain only ASCII letters, digits, underscores or hyphens", ErrFileInvalid)
		}
	}
	return nil
}

func validateCredentialID(value string) error {
	if value == "" || value == "default" {
		return nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != value {
		return fmt.Errorf("credential_id must be a canonical UUID or default")
	}
	return nil
}

// decodeProfileDocument validates every authored table before selecting one.
// Values may be deferred for file-only repair; types and unknown keys never are.
func decodeProfileDocument(data []byte, values bool) (*profileDocument, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: configuration must be valid UTF-8", ErrFileInvalid)
	}
	fields, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	doc := &profileDocument{fields: fields}
	_, versionPresent := fields["format_version"]
	_, profilesPresent := fields["profiles"]
	_, defaultPresent := fields["default_profile"]
	doc.named = versionPresent || profilesPresent || defaultPresent
	if !doc.named {
		if err := validateProfileFields(fields, values); err != nil {
			return nil, err
		}
		return doc, nil
	}
	version, ok := fields["format_version"].(int64)
	if !ok || version != ProfileFormatVersion {
		return nil, fmt.Errorf("%w: named profiles require format_version = 1", ErrFileInvalid)
	}
	for key := range fields {
		if key != "format_version" && key != "default_profile" && key != "profiles" && !globalSetting(key) {
			return nil, fmt.Errorf("%w: unknown root setting %q; account and workflow settings belong under profiles.NAME", ErrFileInvalid, key)
		}
	}
	doc.profiles, ok = fields["profiles"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: profiles must be a table", ErrFileInvalid)
	}
	if defaultPresent {
		doc.defaultProfile, ok = fields["default_profile"].(string)
		if !ok {
			return nil, fmt.Errorf("%w: default_profile must be a string", ErrFileInvalid)
		}
	}
	if len(doc.profiles) != 0 {
		if _, ok := doc.profiles[doc.defaultProfile]; !ok {
			return nil, fmt.Errorf("%w: default_profile must name an existing profile", ErrFileInvalid)
		}
	} else if doc.defaultProfile != "" {
		return nil, fmt.Errorf("%w: default_profile must be empty when profiles is empty", ErrFileInvalid)
	}
	globals := doc.globals()
	if err := validateProfileFields(globals, values); err != nil {
		return nil, err
	}
	locations := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(doc.profiles)) {
		if err := ValidateProfileName(name); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
		}
		profile, ok := doc.profiles[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: profile %q must be a table", ErrFileInvalid, name)
		}
		for key := range profile {
			if !slices.Contains(Keys(), key) {
				return nil, fmt.Errorf("%w: profile %q: unknown setting %q", ErrFileInvalid, name, key)
			}
			if globalSetting(key) && key != "editor" {
				return nil, fmt.Errorf("%w: profile %q: %s belongs at the document root", ErrFileInvalid, name, key)
			}
			if IsSecret(key) {
				return nil, fmt.Errorf("%w: profile %q: credentials belong in auth, not configuration", ErrFileInvalid, name)
			}
		}
		if err := validateProfileFields(profile, values); err != nil {
			return nil, fmt.Errorf("profile %q: %w", name, err)
		}
		decoded, err := unmarshalFields(profile)
		if err != nil {
			return nil, err
		}
		location := profileCredentialLocation(profile, decoded)
		if location != "" {
			if previous, exists := locations[location]; exists {
				return nil, fmt.Errorf("%w: profiles %q and %q select the same credential location", ErrFileInvalid, previous, name)
			}
			locations[location] = name
		}
	}
	return doc, nil
}

func validateProfileFields(fields map[string]any, values bool) error {
	decoded, err := unmarshalFields(fields)
	if err == nil && values {
		err = decoded.ValidateValues()
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	return nil
}

func profileCredentialLocation(fields map[string]any, decoded *Config) string {
	switch decoded.SecretBackend {
	case "", core.CredentialBackendKeyring:
		if disabled, ok := fields["keyring"].(bool); ok && !disabled {
			return ""
		}
		if decoded.CredentialID != "" {
			return "keyring:" + decoded.CredentialID
		}
	case core.CredentialBackendOnePassword:
		if decoded.OnePasswordVault != "" && decoded.OnePasswordItem != "" {
			return "1password:" + decoded.OnePasswordAccount + "\x00" + decoded.OnePasswordVault + "\x00" + decoded.OnePasswordItem
		}
	}
	return ""
}

func (d *profileDocument) globals() map[string]any {
	globals := map[string]any{}
	for key, value := range d.fields {
		if globalSetting(key) {
			globals[key] = value
		}
	}
	return globals
}

func (d *profileDocument) selected(name string) (map[string]any, string, error) {
	if !d.named {
		if name != "" {
			return nil, "", fmt.Errorf("%w: profile %q does not exist in this flat configuration", ErrFileInvalid, name)
		}
		return maps.Clone(d.fields), "", nil
	}
	if name == "" {
		name = d.defaultProfile
	}
	fields := d.globals()
	if name == "" && len(d.profiles) == 0 {
		return fields, "", nil
	}
	profile, ok := d.profiles[name].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("%w: profile %q does not exist", ErrFileInvalid, name)
	}
	maps.Copy(fields, profile)
	return fields, name, nil
}

func selectedProfile(opts Options, flags FlagLayer, variables []string) (string, error) {
	if opts.Profile != "" {
		return opts.Profile, ValidateProfileName(opts.Profile)
	}
	if flags.ProfileSet {
		return flags.Profile, ValidateProfileName(flags.Profile)
	}
	selected, present := "", false
	for _, variable := range variables {
		key, value, _ := strings.Cut(variable, "=")
		if key == "JIRA_PROFILE" {
			selected, present = value, true
		}
	}
	if present {
		return selected, ValidateProfileName(selected)
	}
	return "", nil
}
