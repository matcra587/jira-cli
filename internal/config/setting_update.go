package config

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gechr/clib/theme"
	koanftoml "github.com/knadh/koanf/parsers/toml"
)

// WritableSettingKeys lists scalar preferences and account metadata. Tokens
// belong to auth, and alias changes use their command-tree validation owner.
func WritableSettingKeys() []string {
	return slices.DeleteFunc(Keys(), func(key string) bool { return IsSecret(key) || key == "aliases" })
}

// SettingValueChoices returns closed-set values without file or credential
// access. An empty slice means a key has no enumerated completion candidates.
func SettingValueChoices(key string) []string {
	switch key {
	case "keyring", "read_only":
		return []string{"true", "false"}
	case "glyphs":
		return []string{GlyphSetDefault.String(), GlyphSetNerd.String()}
	case "theme":
		return theme.Names()
	default:
		return nil
	}
}

// ValidateSettingValue checks a supported scalar assignment before file I/O.
// PrepareSetting repeats this boundary for callers outside the CLI.
func ValidateSettingValue(key, value string) error {
	_, err := parseSettingValue(key, value)
	return err
}

func parseSettingValue(key, value string) (any, error) {
	if IsSecret(key) {
		return nil, fmt.Errorf("credentials cannot be stored through config set; use jira auth login")
	}
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("%s must be valid UTF-8", key)
	}
	switch key {
	case "credential_id":
		if err := validateCredentialID(value); err != nil {
			return nil, err
		}
		return value, nil
	case "base_url":
		if err := ValidateBaseURL(value); value != "" && err != nil {
			return nil, err
		}
		return value, nil
	case "email", "queries_path", "theme_path", "editor":
		return value, nil
	case "keyring", "read_only":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%s must be true or false", key)
		}
		return parsed, nil
	case "timeout":
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("timeout must be a nonnegative Go duration, such as 30s or 2m; 0s disables the deadline")
		}
		return parsed.String(), nil
	case "workday_seconds":
		parsed, err := strconv.ParseInt(value, 10, strconv.IntSize)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("workday_seconds must be a positive integer")
		}
		return parsed, nil
	case "glyphs":
		var parsed GlyphSet
		if err := parsed.UnmarshalText([]byte(value)); err != nil {
			return nil, err
		}
		return parsed.String(), nil
	case "theme":
		if value != "" {
			var parsed theme.Theme
			if err := parsed.UnmarshalText([]byte(value)); err != nil {
				return nil, err
			}
		}
		return value, nil
	case "aliases":
		return nil, fmt.Errorf("alias changes require command-tree validation; use jira alias set or jira alias import")
	default:
		return nil, fmt.Errorf("unknown config key %q", key)
	}
}

// SettingUpdate holds a validated candidate and redacted before/after values.
// Preparation performs no writes. The immutable original snapshot still owns
// conflict detection and publication; reopen the file after a successful save.
type SettingUpdate struct {
	Previous  StoredSetting
	Next      StoredSetting
	file      *File
	candidate []byte
	changed   bool
}

// WouldChange reports whether publication would change the stored declaration.
// Equal typed values retain the original bytes, including comments and spelling.
func (u *SettingUpdate) WouldChange() bool { return u.changed }

// Save publishes the validated candidate through File.Save. It checks context,
// original bytes/existence and the resolved target before atomic replacement.
func (u *SettingUpdate) Save(ctx context.Context) (bool, error) {
	return u.file.Save(ctx, u.candidate)
}

// PrepareSetting changes one scalar declaration without adding defaults,
// environment values or credentials. Changed files are normalized as TOML;
// all other declared values, including literal alias names, are retained.
// Invalid syntax/types are refused; replacing a malformed site URL can repair it.
func (f *File) PrepareSetting(key, value string) (*SettingUpdate, error) {
	update, err := f.PrepareSettings(map[string]string{key: value})
	if err != nil {
		return nil, err
	}
	change := update.Changes[0]
	return &SettingUpdate{Previous: change.Previous, Next: change.Next, file: f, candidate: update.candidate, changed: update.changed}, nil
}

// SettingChange carries redacted authored values for one scalar declaration.
type SettingChange struct {
	Previous StoredSetting
	Next     StoredSetting
}

// SettingsUpdate owns one combined candidate for related scalar assignments.
// A failed assignment prepares nothing; publication uses the original file
// snapshot once, so pairs such as a theme name/path cannot be partially saved.
type SettingsUpdate struct {
	Changes   []SettingChange
	file      *File
	candidate []byte
	changed   bool
}

// WouldChange reports whether any supplied declaration differs from the file.
func (u *SettingsUpdate) WouldChange() bool { return u.changed }

// Save publishes all prepared assignments through the existing snapshot owner.
func (u *SettingsUpdate) Save(ctx context.Context) (bool, error) {
	return u.file.Save(ctx, u.candidate)
}

// Create publishes the candidate only if the selected file still does not
// exist. It retains the shared validation and no-clobber creation boundary.
func (u *SettingsUpdate) Create(ctx context.Context) (bool, error) {
	return u.file.Create(ctx, u.candidate)
}

// PrepareSettings validates all assignments before preparing a single candidate.
// Keys and receipts are ordered deterministically. No defaults, environment or
// credentials enter the file; unchanged typed values retain authored bytes.
func (f *File) PrepareSettings(values map[string]string) (*SettingsUpdate, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one setting assignment is required")
	}
	keys := slices.Sorted(maps.Keys(values))
	parsed := make(map[string]any, len(values))
	for _, key := range keys {
		value, err := parseSettingValue(key, values[key])
		if err != nil {
			return nil, err
		}
		parsed[key] = value
	}
	fields, selected, decoded, err := f.selectedFields()
	if err != nil {
		return nil, err
	}
	changes := make([]SettingChange, 0, len(values))
	for _, key := range keys {
		changes = append(changes, SettingChange{Previous: storedSetting(key, selected, decoded)})
	}
	for key, value := range parsed {
		if err := f.setSelectedField(fields, key, value); err != nil {
			return nil, err
		}
	}
	maps.Copy(selected, parsed)
	updated, err := unmarshalFields(selected)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	if err := updated.ValidateValues(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileInvalid, err)
	}
	changed := false
	for index, key := range keys {
		next := storedSetting(key, selected, updated)
		changes[index].Next = next
		previous := changes[index].Previous
		changed = changed || !previous.Present || previous.Value != next.Value || previous.Secret != next.Secret
	}
	candidate := f.original
	if changed {
		candidate, err = koanftoml.Parser().Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("encode config: %w", err)
		}
		if err := ValidateFile(candidate); err != nil {
			return nil, err
		}
	}
	return &SettingsUpdate{Changes: changes, file: f, candidate: candidate, changed: changed}, nil
}
