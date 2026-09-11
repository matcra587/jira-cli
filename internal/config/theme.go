package config

import (
	"fmt"
	"os"
	"strings"

	clibtheme "github.com/gechr/clib/theme"
	xstrings "github.com/gechr/x/strings"
	"github.com/gechr/x/terminal"
)

// EnvThemeName overrides the default theme process-wide. It matches the prefix
// set via clibtheme.SetEnvPrefix("JIRA") at help-renderer setup, so the same
// variable drives help, plain output, and the TUI.
const EnvThemeName = "JIRA_THEME"

// ThemeNameAuto selects clib's light or dark theme by detecting the terminal
// background, so hash-based entity colors stay readable on either surface. It
// is opt-in, never the default: background detection misfires silently under
// tmux, SSH, and container exec — all common developer environments — and a
// wrong guess is actively worse than the fixed palette, so the default stays
// dark until detection has a release cycle of field reports. A user whose
// terminal is misdetected pins "light" or "dark" instead.
const ThemeNameAuto = "auto"

// ThemeNameValues advertises the selectable theme names: the opt-in "auto" plus
// clib's built-in presets, sourced from clibtheme.Names so the list always
// matches the dependency. Names resolve straight through clib — there is no
// jira-cli-specific aliasing.
var ThemeNameValues = append([]string{ThemeNameAuto}, clibtheme.Names()...)

// IsAutoTheme reports whether name selects the background-detecting theme.
func IsAutoTheme(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), ThemeNameAuto)
}

// ValidateThemeName reports whether name is a theme clib accepts (or "auto").
// It is the write-time gate for `config theme --name`; config load deliberately
// does not call it, so a theme name that clib later renames degrades to the
// dark fallback at render rather than blocking every command.
func ValidateThemeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || IsAutoTheme(name) {
		return nil
	}
	var th clibtheme.Theme
	if th.UnmarshalText([]byte(name)) != nil {
		// Build the message from ThemeNameValues, not clib's UnmarshalText
		// error: clib's valid-list omits "auto", which jira-cli accepts.
		return fmt.Errorf("theme.name: unknown theme %q (valid: %s)", name, strings.Join(ThemeNameValues, ", "))
	}
	return nil
}

// themeFromName resolves a single theme name. It returns nil for an empty or
// unrecognized name so callers fall back to the dark default.
func themeFromName(name string) *clibtheme.Theme {
	if xstrings.IsBlank(name) {
		return nil
	}
	var th clibtheme.Theme
	if th.UnmarshalText([]byte(name)) != nil {
		return nil
	}
	return &th
}

// DefaultTheme resolves the process default theme: the JIRA_THEME override when
// set and valid, otherwise the dark built-in. This restores clib v0.4.15's
// Default(), which the v0.5 upgrade removed, keeping the documented JIRA_THEME
// override working for plain output, help, and the TUI.
func DefaultTheme() *clibtheme.Theme {
	if th := themeFromName(os.Getenv(EnvThemeName)); th != nil {
		return th
	}
	return clibtheme.Dark()
}

// ThemeForName resolves an explicit theme name (e.g. config theme.name),
// falling back to DefaultTheme when the name is empty or unrecognized.
func ThemeForName(name string) *clibtheme.Theme {
	if th := themeFromName(name); th != nil {
		return th
	}
	return DefaultTheme()
}

// AutoTheme resolves the opt-in "auto" theme: clib's light or dark theme chosen
// by the terminal background when out supports terminal output. The
// JIRA_THEME override still wins, matching every other resolution path. When
// there is nothing to detect — out is not a terminal, e.g. --color=always piped
// into a pager — it falls back to dark: dark matches the convention of every
// major ANSI CLI (git, ripgrep, bat) and the dark-terminal skew of users who
// deliberately force color through a pipe.
func AutoTheme(out *os.File) *clibtheme.Theme {
	if th := themeFromName(os.Getenv(EnvThemeName)); th != nil {
		return th
	}
	if !terminal.Is(out) {
		return clibtheme.Dark()
	}
	bg, ok := clibtheme.DetectBackground()
	if !ok {
		bg = clibtheme.BackgroundDark
	}
	return clibtheme.DefaultPair().ForBackground(bg)
}
