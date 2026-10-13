package config

import (
	"strings"

	"github.com/gechr/clib/theme"
)

// IsAutomaticTheme recognizes the application selector for passive background
// hints. It is not a concrete palette name understood by the styling libraries.
func IsAutomaticTheme(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "auto")
}

// ThemeChoices returns application selectors and the pinned library presets.
// Custom palette bases continue to require a concrete library preset.
func ThemeChoices() []string {
	return append([]string{"auto"}, theme.Names()...)
}
