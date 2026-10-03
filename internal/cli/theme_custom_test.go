package cli

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/gechr/clib/theme"
	clogtheme "github.com/gechr/clog/theme"

	"github.com/matcra587/jira-cli/internal/config"
)

func assertCustomColor(t *testing.T, actual color.Color, text string) {
	t.Helper()
	want, err := customRGB(text)
	if err != nil || actual == nil {
		t.Fatalf("missing custom color: %v %v", actual, err)
	}
	ar, ag, ab, aa := actual.RGBA()
	wr, wg, wb, wa := want.RGBA()
	if ar != wr || ag != wg || ab != wb || aa != wa {
		t.Fatalf("palette color differs: actual=%04x,%04x,%04x,%04x want=%s", ar, ag, ab, aa, text)
	}
}

func TestCustomThemeAppliesEveryDeclaredStyleAndSyntaxRole(t *testing.T) {
	t.Parallel()
	definition := &config.ThemeFile{Base: "light", Colors: map[string]string{}, Syntax: map[string]string{}, EntityColors: []string{"#123456", "#abcdef"}}
	for index, role := range config.ThemeColorRoles() {
		definition.Colors[role] = fmt.Sprintf("#%06x", 0x010203+index*0x050001)
	}
	for index, role := range config.ThemeSyntaxRoles() {
		definition.Syntax[role] = fmt.Sprintf("#%06x", 0x050403+index*0x010501)
	}
	selected, syntax, err := customTheme(definition)
	if err != nil {
		t.Fatal(err)
	}
	bindings := customThemeBindings(selected)
	if len(bindings) != len(config.ThemeColorRoles()) || selected.Background != theme.BackgroundLight || syntax.Background != clogtheme.BackgroundLight || selected.String() != "custom" {
		t.Fatal("palette composition lost advertised role inventory or background")
	}
	for _, role := range config.ThemeColorRoles() {
		binding, ok := bindings[role]
		if !ok || binding.style == nil {
			t.Fatalf("custom style role %s is not implemented", role)
		}
		assertCustomColor(t, binding.style.GetForeground(), definition.Colors[role])
	}
	syntaxBindings := customSyntaxBindings(syntax)
	if len(syntaxBindings) != len(config.ThemeSyntaxRoles()) {
		t.Fatal("custom syntax inventory differs from the parser")
	}
	for _, role := range config.ThemeSyntaxRoles() {
		target, ok := syntaxBindings[role]
		if !ok || target == nil {
			t.Fatalf("custom syntax role %s is not implemented", role)
		}
		assertCustomColor(t, *target, definition.Syntax[role])
	}
	if len(selected.EntityColors) != 2 {
		t.Fatal("custom entity colors were dropped")
	}
	for index, text := range definition.EntityColors {
		assertCustomColor(t, selected.EntityColors[index], text)
	}
}

func TestCustomThemeRetainsInheritedAttributesAndIndependentPaletteOwnership(t *testing.T) {
	t.Parallel()
	definition := &config.ThemeFile{Base: "dark", Colors: map[string]string{"bold_green": "#010203"}}
	selected, _, err := customTheme(definition)
	if err != nil || !selected.BoldGreen.GetBold() {
		t.Fatal("custom foreground dropped inherited bold styling")
	}
	if selected.Red.GetForeground() != theme.Dark().Red.GetForeground() || len(selected.EntityColors) != len(theme.Dark().EntityColors) {
		t.Fatal("partial custom theme replaced untouched preset roles")
	}
	definition.Colors["bold_green"] = "#abcdef"
	other, _, err := customTheme(definition)
	if err != nil {
		t.Fatal(err)
	}
	assertCustomColor(t, selected.BoldGreen.GetForeground(), "#010203")
	assertCustomColor(t, other.BoldGreen.GetForeground(), "#abcdef")
	if other.BoldGreen == selected.BoldGreen {
		t.Fatal("independent custom themes alias modified styles")
	}
	_, syntax, err := customTheme(&config.ThemeFile{Base: "gruvbox-light"})
	if err != nil || syntax.Background != clogtheme.BackgroundLight {
		t.Fatal("a clib-only light preset received dark syntax colors")
	}
}

func TestCustomThemeLoadsRelativeToSelectedConfigAndWarnsWithoutDisablingCommands(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	config.SetFlagLayer(config.FlagLayer{})
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "palette.toml")
	if err := os.WriteFile(path, []byte("theme='monokai'\ntheme_path='palette.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config.SetFlagLayer(config.FlagLayer{Path: path})
	if err := os.WriteFile(palette, []byte("base='light'\n[colors]\nhelp_command='#123456'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := readThemeFromConfig()
	if selected == nil || selected.Background != theme.BackgroundLight || len(TakeWarnings()) != 0 {
		t.Fatal("selected config did not load its relative custom palette")
	}
	assertCustomColor(t, selected.HelpCommand.GetForeground(), "#123456")
	if err := os.WriteFile(palette, []byte("[colors]\nred='#broken'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected = readThemeFromConfig()
	warnings := TakeWarnings()
	if selected == nil || selected.String() != "monokai" || len(warnings) != 1 || warnings[0].Type != "theme_invalid" {
		t.Fatalf("invalid custom file lost preset or warning: %v %+v", selected, warnings)
	}
}

func TestExplicitThemeEnvironmentWinsWithoutReadingCustomFile(t *testing.T) {
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
	t.Setenv("JIRA_THEME", "monokai")
	t.Setenv("JIRA_THEME_PATH", filepath.Join(t.TempDir(), "missing-palette.toml"))
	applyTheme()
	selected := ResolvedTheme()
	if selected.String() != "monokai" || len(TakeWarnings()) != 0 {
		t.Fatal("named environment override loaded a losing custom file")
	}
}
