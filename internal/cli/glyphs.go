package cli

import (
	"sync"
	"sync/atomic"

	"github.com/matcra587/jira-cli/internal/config"
)

// ResolvedGlyphs answers "which glyph vocabulary" for the process,
// mirroring ResolvedTheme's own shape (theme.go): resolved once, never
// read by a sink from config directly, and never itself an error a
// renderer has to handle.
//
// That last point is NOT the same silent-fallback story ResolvedTheme
// tells for an unusable theme name. GlyphSet.UnmarshalText (config.go)
// makes an unrecognized `glyphs` value fail config.Load() outright, and
// every command that can reach a renderer already calls config.Load()
// itself first — through Deps.JiraClient or Deps.Credentials at the
// composition root (internal/cli/root/deps.go) — to get the client or
// keyring it needs to run at all. That call is where a bad `glyphs`
// value is actually caught, as a classified config error, before this
// function's own config.Load() ever runs a second time. glyphsFromConfig
// falling back to GlyphSetDefault on an error is therefore defensive
// symmetry with ResolvedTheme (a renderer must never hang or panic for
// want of an answer), not the enforcement point — by the time a sink
// asks ResolvedGlyphs(), the command has already survived the real
// check, or it never got this far.
func ResolvedGlyphs() config.GlyphSet {
	if override := glyphOverride.Load(); override != nil {
		return *override
	}
	return glyphsFromConfig()
}

// glyphOverride pins ResolvedGlyphs for callers outside this package —
// today only tests in internal/cli/issue, which render pills and must
// not inherit whatever the DEVELOPER happens to have in their own config
// file. Without it, a machine with `glyphs: nerd` set renders capped
// pills where a test written against the padded shape expected spaces,
// so the suite's result depends on whose machine it runs on. That is not
// hypothetical: it is exactly how these tests first went red.
//
// An atomic rather than the colorMu-style mutex next door: this is a
// single pointer swapped by a test and read by renderers, with no second
// piece of state it has to stay consistent with.
var glyphOverride atomic.Pointer[config.GlyphSet]

// SetGlyphs pins the glyph set for this process, mirroring
// SetStyleEnabled's own shape (color.go) — the seam a test in another
// package uses to render a known glyph vocabulary. Passing nil clears
// the pin and returns ResolvedGlyphs to reading config.
func SetGlyphs(set *config.GlyphSet) {
	glyphOverride.Store(set)
}

// glyphsFromConfig resolves the `glyphs` key from the config FILE and
// environment exactly once per process — reassignable in tests the same
// way themeFromConfig is (theme_test.go's resetTheme), so a developer's
// own config file cannot leak into a test's expectations.
//
// Credentials is nil for the same reason readThemeFromConfig's is: this
// runs before a renderer needs anything from the keyring, and opening
// one to answer a glyph-set question would be slow, and on macOS could
// prompt for a password nobody asked for.
var glyphsFromConfig = sync.OnceValue(readGlyphsFromConfig)

// readGlyphsFromConfig is glyphsFromConfig's one execution — see there.
func readGlyphsFromConfig() config.GlyphSet {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return config.GlyphSetDefault
	}
	return resolved.Config.Glyphs
}
