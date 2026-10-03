package cli

import (
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

// setGlyphs stubs glyphsFromConfig for the duration of the test, the
// same way resetTheme (theme_test.go) stubs themeFromConfig — a real
// machine's own config file, or one left behind by a previous test,
// must never leak into what ResolvedGlyphs() answers here.
func setGlyphs(t *testing.T, g config.GlyphSet) {
	t.Helper()
	previous := glyphsFromConfig
	glyphsFromConfig = func() config.GlyphSet { return g }
	t.Cleanup(func() { glyphsFromConfig = previous })
}

// TestResolvedGlyphsDefaultsWithNoConfig pins glyphsFromConfig's own
// unstubbed path against an empty file: no `glyphs` key at all resolves
// to GlyphSetDefault, the zero value — not an error, and not nerd.
func TestResolvedGlyphsDefaultsWithNoConfig(t *testing.T) {
	previous := glyphsFromConfig
	t.Cleanup(func() { glyphsFromConfig = previous })
	glyphsFromConfig = readGlyphsFromConfig

	// Point config.Load at a config directory nothing has written to,
	// the same isolation resetTheme's own comment explains is needed —
	// readGlyphsFromConfig calls config.Load(config.Options{}), which
	// resolves the REAL XDG path, so this proves the function's
	// contract (default on read error, since a nonexistent path here
	// mirrors the "no file" case config.Load already treats as normal)
	// rather than asserting on a developer machine's own config.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty"))

	if got := ResolvedGlyphs(); got != config.GlyphSetDefault {
		t.Errorf("ResolvedGlyphs() with no config file = %v, want GlyphSetDefault", got)
	}
}

// TestResolvedGlyphsReadsStubbedValue pins that ResolvedGlyphs() reads
// through glyphsFromConfig rather than caching its own separate answer —
// the seam every sinks_test.go caller needing nerd mode relies on.
func TestResolvedGlyphsReadsStubbedValue(t *testing.T) {
	setGlyphs(t, config.GlyphSetNerd)

	if got := ResolvedGlyphs(); got != config.GlyphSetNerd {
		t.Errorf("ResolvedGlyphs() = %v, want the stubbed GlyphSetNerd", got)
	}
}
