package config

import "slices"

import "testing"

// TestIsAutoTheme covers the opt-in gate, including case and surrounding space.
func TestIsAutoTheme(t *testing.T) {
	for _, name := range []string{"auto", "AUTO", " Auto "} {
		if !IsAutoTheme(name) {
			t.Errorf("IsAutoTheme(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "dark", "default", "automatic"} {
		if IsAutoTheme(name) {
			t.Errorf("IsAutoTheme(%q) = true, want false", name)
		}
	}
}

// TestAutoThemeValidatesAndIsAdvertised pins that "auto" passes validation and
// shows up in the completion/enum list.
func TestAutoThemeValidatesAndIsAdvertised(t *testing.T) {
	if err := ValidateThemeName("auto"); err != nil {
		t.Errorf("ValidateThemeName(\"auto\") = %v, want nil", err)
	}
	found := slices.Contains(ThemeNameValues, "auto")
	if !found {
		t.Error("ThemeNameValues does not advertise \"auto\"")
	}
}

// TestThemeNameValuesSourcedFromClib pins that the advertised list is built
// from clibtheme.Names (a v0.5 preset that was never hand-listed must appear)
// and keeps "auto" first.
func TestThemeNameValuesSourcedFromClib(t *testing.T) {
	if len(ThemeNameValues) == 0 || ThemeNameValues[0] != "auto" {
		t.Fatalf("ThemeNameValues[0] = %q, want \"auto\"", ThemeNameValues)
	}
	want := map[string]bool{"solarized-dark": false, "solarized-light": false}
	for _, name := range ThemeNameValues {
		if name == "default" {
			t.Fatalf("ThemeNameValues advertises legacy name %q: %v", name, ThemeNameValues)
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("ThemeNameValues missing %q", name)
		}
	}
}

// TestAutoThemeFallsBackToDarkOffTerminal pins the DARK fallback: with no
// terminal to detect (nil out), auto resolves to the dark theme.
func TestAutoThemeFallsBackToDarkOffTerminal(t *testing.T) {
	t.Setenv(EnvThemeName, "")
	if got := AutoTheme(nil).String(); got != "dark" {
		t.Errorf("AutoTheme(nil) = %q, want \"dark\"", got)
	}
}

// TestAutoThemeHonoursEnvOverride pins that JIRA_THEME wins over detection, so
// an explicit override holds even for the auto theme.
func TestAutoThemeHonoursEnvOverride(t *testing.T) {
	t.Setenv(EnvThemeName, "light")
	if got := AutoTheme(nil).String(); got != "light" {
		t.Errorf("AutoTheme(nil) with %s=light = %q, want \"light\"", EnvThemeName, got)
	}
}

// TestAdvertisedThemeNamesValidate guards that every name jira-cli advertises
// is accepted by clib.
func TestAdvertisedThemeNamesValidate(t *testing.T) {
	for _, name := range ThemeNameValues {
		if err := ValidateThemeName(name); err != nil {
			t.Errorf("ValidateThemeName(%q) = %v, want nil", name, err)
		}
	}
}

func TestLegacyThemeNamesDoNotValidate(t *testing.T) {
	for _, name := range []string{"default", "plain", "monochrome", "solarized"} {
		if err := ValidateThemeName(name); err == nil {
			t.Errorf("ValidateThemeName(%q) error = nil, want validation error", name)
		}
	}
}

func TestConfigValidateToleratesUnknownThemeName(t *testing.T) {
	cfg := Defaults()
	cfg.Theme.Name = "no-such-theme"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected unknown theme.name: %v", err)
	}
}

// TestDefaultThemeHonoursEnvOverride guards the documented process-wide
// JIRA_THEME override that the v0.5 upgrade dropped from plain output and the
// TUI when theme.Default() was removed.
func TestDefaultThemeHonoursEnvOverride(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"unset falls back to dark", "", "dark"},
		{"explicit current name", "nord", "nord"},
		{"invalid name falls back to dark", "no-such-theme", "dark"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvThemeName, tc.env)
			if got := DefaultTheme().String(); got != tc.want {
				t.Errorf("DefaultTheme() with %s=%q = %q, want %q", EnvThemeName, tc.env, got, tc.want)
			}
		})
	}
}
