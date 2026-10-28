package config

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gechr/clib/theme"
	koanftoml "github.com/knadh/koanf/parsers/toml"
)

// MaxThemeFileBytes bounds a custom palette, independently of settings files.
const MaxThemeFileBytes = 64 << 10

// ErrThemeFileInvalid identifies invalid palette input, separately from I/O or
// cancellation, so callers can offer a correction without suggesting a retry.
var ErrThemeFileInvalid = errors.New("invalid custom theme file")

// ThemeFile is a validated custom palette. Colors override clib styles, Syntax
// overrides clog syntax roles, and an absent EntityColors retains the preset.
// Every color is a six-digit RGB value; this format contains no executable code.
type ThemeFile struct {
	Name         string
	Base         string
	Colors       map[string]string
	Syntax       map[string]string
	EntityColors []string
}

// ThemeColorRoles names the customizable clib style slots. The renderer checks
// its bindings against this inventory so an advertised role cannot be ignored.
func ThemeColorRoles() []string {
	return []string{
		"blue", "bold", "bold_green", "dim", "green", "magenta", "orange", "red", "yellow",
		"help_alias", "help_arg", "help_arg_optional", "help_bold_dim", "help_command",
		"help_desc_backtick", "help_desc_list", "help_desc_numbered_list", "help_desc_unordered_list",
		"help_dim", "help_enum_default", "help_flag", "help_flag_backtick", "help_flag_default",
		"help_flag_example", "help_flag_note", "help_key_value_separator", "help_repeat_ellipsis",
		"help_section", "help_subcommand", "help_value_placeholder", "markdown_code", "markdown_text",
	}
}

// ThemeSyntaxRoles names the independent clog syntax highlighting slots.
func ThemeSyntaxRoles() []string {
	return []string{"accent", "bool_false", "bool_true", "comment", "foreground", "key", "number", "secondary", "string"}
}

// ParseThemeFile checks TOML types, keys, preset names and palette values before
// any styles are applied. A missing base selects dark; an explicit empty base
// is an error. An explicit empty entity palette is refused rather than ignored.
func ParseThemeFile(data []byte) (*ThemeFile, error) {
	parsed, err := parseThemeFile(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrThemeFileInvalid, err)
	}
	return parsed, nil
}

func parseThemeFile(data []byte) (*ThemeFile, error) {
	if len(data) > MaxThemeFileBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("theme file must be valid UTF-8 and at most %d bytes", MaxThemeFileBytes)
	}
	fields, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("decode theme TOML: %w", err)
	}
	result := &ThemeFile{Base: "dark", Colors: map[string]string{}, Syntax: map[string]string{}}
	for key, value := range fields {
		switch key {
		case "name":
			name, ok := value.(string)
			if !ok {
				return nil, errors.New("theme name must be text")
			}
			if err := ValidateThemeName(name); err != nil {
				return nil, err
			}
			result.Name = name
		case "base":
			name, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("theme base must be a preset name")
			}
			var preset theme.Theme
			if err := preset.UnmarshalText([]byte(name)); err != nil {
				return nil, fmt.Errorf("theme base: %w", err)
			}
			result.Base = preset.String()
		case "colors", "syntax":
			roles := ThemeColorRoles()
			if key == "syntax" {
				roles = ThemeSyntaxRoles()
			}
			values, err := parseThemeColors(key, value, roles)
			if err != nil {
				return nil, err
			}
			if key == "colors" {
				result.Colors = values
			} else {
				result.Syntax = values
			}
		case "entity_colors":
			items, ok := value.([]any)
			if !ok || len(items) == 0 {
				return nil, fmt.Errorf("entity_colors must be a nonempty array of RGB strings")
			}
			for _, item := range items {
				text, ok := item.(string)
				if !ok || !validThemeColor(text) {
					return nil, fmt.Errorf("entity_colors must contain only #RRGGBB strings")
				}
				result.EntityColors = append(result.EntityColors, text)
			}
		default:
			return nil, fmt.Errorf("unknown theme key %q", key)
		}
	}
	return result, nil
}

func parseThemeColors(section string, value any, roles []string) (map[string]string, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("theme %s must be a TOML table", section)
	}
	result := make(map[string]string, len(fields))
	for key, value := range fields {
		if !slices.Contains(roles, key) {
			return nil, fmt.Errorf("unknown theme %s role %q", section, key)
		}
		text, ok := value.(string)
		if !ok || !validThemeColor(text) {
			return nil, fmt.Errorf("theme %s.%s must be a #RRGGBB string", section, key)
		}
		result[key] = text
	}
	return result, nil
}

func validThemeColor(text string) bool {
	if len(text) != 7 || !strings.HasPrefix(text, "#") {
		return false
	}
	_, err := hex.DecodeString(text[1:])
	return err == nil
}

// LoadThemeFile reads a named regular file without writing or consulting any
// credential/config layer. It bounds bytes even when the file grows after stat.
func LoadThemeFile(ctx context.Context, path string) (*ThemeFile, error) {
	data, err := readThemeInput(ctx, path)
	if err != nil {
		return nil, err
	}
	return ParseThemeFile(data)
}

func readThemeInput(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect theme file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxThemeFileBytes {
		return nil, fmt.Errorf("%w: theme input must be a regular file of at most %d bytes", ErrThemeFileInvalid, MaxThemeFileBytes)
	}
	file, err := openFileInput(path)
	if err != nil {
		return nil, fmt.Errorf("open theme file: %w", err)
	}
	defer func() { _ = file.Close() }() //nolint:errcheck // read-only descriptor; all read errors are returned
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened theme file: %w", err)
	}
	if !opened.Mode().IsRegular() || opened.Size() > MaxThemeFileBytes {
		return nil, fmt.Errorf("%w: opened theme input must be a regular file of at most %d bytes", ErrThemeFileInvalid, MaxThemeFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxThemeFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read theme file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > MaxThemeFileBytes {
		return nil, ErrThemeFileInvalid
	}
	return data, nil
}
