package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeFilePreservesEveryAdvertisedPaletteRole(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString("base=' CATPPUCCIN_LATTE '\nentity_colors=['#123456','#aBcDeF']\n[colors]\n")
	for _, role := range ThemeColorRoles() {
		source.WriteString(role + "='#13579B'\n")
	}
	source.WriteString("[syntax]\n")
	for _, role := range ThemeSyntaxRoles() {
		source.WriteString(role + "='#2468AC'\n")
	}
	parsed, err := ParseThemeFile([]byte(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Base != "catppuccin-latte" || len(parsed.Colors) != len(ThemeColorRoles()) || len(parsed.Syntax) != len(ThemeSyntaxRoles()) || len(parsed.EntityColors) != 2 || parsed.EntityColors[1] != "#aBcDeF" {
		t.Fatalf("custom palette lost base or role values: %+v", parsed)
	}
	for _, role := range ThemeColorRoles() {
		if parsed.Colors[role] != "#13579B" {
			t.Fatalf("style role %s was lost", role)
		}
	}
	for _, role := range ThemeSyntaxRoles() {
		if parsed.Syntax[role] != "#2468AC" {
			t.Fatalf("syntax role %s was lost", role)
		}
	}
}

func TestThemeFileRefusesUnknownTypesRolesAndUnsafeColors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "invalid TOML", text: "base = ["},
		{name: "unknown key", text: "palette='dark'"},
		{name: "unknown nested key", text: "[base]\nname='dark'"},
		{name: "empty base", text: "base=''"},
		{name: "wrong base type", text: "base=true"},
		{name: "unknown preset", text: "base='dracual'"},
		{name: "probe is not a preset", text: "base='auto'"},
		{name: "wrong table", text: "colors=['#123456']"},
		{name: "style typo", text: "[colors]\nhelp_unrecognized='#123456'"},
		{name: "nested style", text: "[colors.red]\nvalue='#123456'"},
		{name: "syntax typo", text: "[syntax]\nstrings='#123456'"},
		{name: "wrong color type", text: "[colors]\nred=123456"},
		{name: "short RGB", text: "[colors]\nred='#123'"},
		{name: "nonhex RGB", text: "[colors]\nred='#12345G'"},
		{name: "alpha RGB", text: "[colors]\nred='#123456ff'"},
		{name: "trimmed RGB", text: "[syntax]\nkey=' #123456'"},
		{name: "escape injection", text: "[colors]\nred=\"\\u001b[31m\""},
		{name: "empty entity colors", text: "entity_colors=[]"},
		{name: "wrong entity colors", text: "entity_colors='#123456'"},
		{name: "wrong entity item", text: "entity_colors=['#123456',false]"},
		{name: "bad entity color", text: "entity_colors=['#12345x']"},
		{name: "invalid UTF8", text: "#\xff"},
		{name: "oversized document", text: "#" + strings.Repeat("x", MaxThemeFileBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if parsed, err := ParseThemeFile([]byte(tc.text)); err == nil || parsed != nil {
				t.Fatalf("invalid custom theme accepted: %+v %v", parsed, err)
			}
		})
	}
}

func TestThemeFileDefaultsAndIndependentResults(t *testing.T) {
	t.Parallel()
	first, err := ParseThemeFile(nil)
	if err != nil || first.Base != "dark" || first.EntityColors != nil {
		t.Fatalf("absent theme fields lost defaults: %+v %v", first, err)
	}
	first.Colors["red"] = "#123456"
	first.Syntax["key"] = "#123456"
	second, err := ParseThemeFile(nil)
	if err != nil || len(second.Colors) != 0 || len(second.Syntax) != 0 {
		t.Fatal("palette maps alias independent parsed documents")
	}
	exact, err := ParseThemeFile([]byte("#" + strings.Repeat("x", MaxThemeFileBytes-1)))
	if err != nil || exact.Base != "dark" {
		t.Fatal("exact byte limit was refused")
	}
}

func TestThemeFileLoadsOnlyOwnedRegularInputAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "custom.toml")
	const data = "base='light'\n[colors]\nhelp_command='#123456'\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadThemeFile(t.Context(), path)
	if err != nil || loaded.Base != "light" || loaded.Colors["help_command"] != "#123456" {
		t.Fatalf("custom file was not read: %+v %v", loaded, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LoadThemeFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled palette read lost its original context cause")
	}
	for _, rejected := range []string{filepath.Dir(path), path + ".missing"} {
		if _, err := LoadThemeFile(t.Context(), rejected); err == nil {
			t.Fatal("nonregular or missing palette input was accepted")
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxThemeFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadThemeFile(t.Context(), path); err == nil {
		t.Fatal("oversized palette file was accepted")
	}
}
