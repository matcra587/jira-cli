package config

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestSettingsUpdatePublishesThemePairTogetherAndRetainsAuthoredUnrelatedValues(t *testing.T) {
	t.Parallel()
	path := aliasFixture(t, "# original\nemail='stored@example.invalid'\n[aliases]\n'Mixed.Name'='version'\n")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	assignments := map[string]string{"theme_path": "palette.toml", "theme": "light"}
	prepared, err := file.PrepareSettings(assignments)
	if err != nil || !prepared.WouldChange() || len(prepared.Changes) != 2 || prepared.Changes[0].Next.Key != "theme" || prepared.Changes[1].Next.Key != "theme_path" {
		t.Fatalf("paired settings did not prepare deterministic receipts: %+v %v", prepared, err)
	}
	assignments["theme"] = "dracula"
	if changed, err := prepared.Save(t.Context()); err != nil || !changed {
		t.Fatalf("paired settings did not publish: %t %v", changed, err)
	}
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.Theme != "light" || loaded.Config.ThemePath != "palette.toml" || loaded.Config.Email != "stored@example.invalid" || loaded.Config.Aliases["Mixed.Name"] != "version" || loaded.Source("theme_path") != LayerFile {
		t.Fatalf("combined candidate lost ownership or unrelated authored values: %+v %v", loaded, err)
	}
}

func TestSettingsUpdateInvalidMemberOrConflictCannotPublishHalfThePair(t *testing.T) {
	t.Parallel()
	const original = "theme='monokai'\ntheme_path='old.toml'\n"
	path := aliasFixture(t, original)
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]string{
		{}, {"theme_path": "new.toml", "theme": "unknown-preset"}, {"theme": "light", "api_token": "synthetic-forbidden-pair"},
	} {
		if prepared, err := file.PrepareSettings(values); err == nil || prepared != nil {
			t.Fatal("invalid member prepared a publishable candidate")
		}
	}
	prepared, err := file.PrepareSettings(map[string]string{"theme": "light", "theme_path": "new.toml"})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatal("preparation published part of the theme pair")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if changed, err := prepared.Save(ctx); changed || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication changed the pair or lost its cause")
	}
	const intervening = "theme='nord'\ntheme_path='other.toml'\n"
	if err := os.WriteFile(path, []byte(intervening), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := prepared.Save(t.Context()); changed || !errors.Is(err, ErrFileChanged) {
		t.Fatal("combined save overwrote an intervening theme pair")
	}
	contents, err = os.ReadFile(path)
	if err != nil || string(contents) != intervening {
		t.Fatal("refused save lost the intervening pair")
	}
}

func TestSettingsUpdateNoopAndIndependentCandidatesKeepOriginalSnapshot(t *testing.T) {
	t.Parallel()
	const original = "# authored spelling\ntheme = 'dark'\ntheme_path = ''\n"
	path := aliasFixture(t, original)
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := file.PrepareSettings(map[string]string{"theme": "dark", "theme_path": ""})
	if err != nil || noop.WouldChange() {
		t.Fatal("equal combined values fabricated a change")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := noop.Save(t.Context()); err != nil || changed {
		t.Fatal("equal combined candidate rewrote the file")
	}
	after, err := os.Stat(path)
	contents, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(contents) != original || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Fatal("combined no-op lost authored bytes or metadata")
	}
	first, err := file.PrepareSettings(map[string]string{"theme": "light"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := file.PrepareSettings(map[string]string{"theme_path": "other.toml"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := second.Save(t.Context()); err != nil || !changed {
		t.Fatal("independent second candidate failed to publish")
	}
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.Theme != "dark" || loaded.Config.ThemePath != "other.toml" {
		t.Fatal("independent candidate accidentally included another prepared change")
	}
	if changed, err := first.Save(t.Context()); changed || !errors.Is(err, ErrFileChanged) {
		t.Fatal("first candidate abandoned its original snapshot")
	}
}
