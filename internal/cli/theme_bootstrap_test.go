package cli

import (
	"testing"

	"github.com/gechr/clib/theme"
)

func TestBootstrapThemeLeavesSettingsResolutionLazy(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	reads := 0
	selected := theme.Light()
	themeFromConfig = func() *theme.Theme {
		reads++
		return selected
	}
	if got := BootstrapTheme(); got == nil || !BackgroundIsDark(got) || reads != 0 {
		t.Fatal("bootstrap consumed settings or lost the nonnil dark fallback")
	}
	if got := ResolvedTheme(); got != selected || reads != 1 {
		t.Fatal("bootstrap prevented later settings resolution")
	}
}
