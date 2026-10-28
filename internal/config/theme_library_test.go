package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedThemeLibraryCreateRenameConflictAndPortableExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "themes")
	def := &ThemeFile{Base: "dark", Colors: map[string]string{"blue": "#123456"}, Syntax: map[string]string{"number": "#abcdef"}}
	saved, published, err := SaveTheme(t.Context(), dir, "Ocean", def, nil)
	if err != nil || !published || saved == nil {
		t.Fatalf("create: %v %v", published, err)
	}
	if def.Name != "" {
		t.Fatal("saving mutated caller-owned palette")
	}
	if _, _, err := SaveTheme(t.Context(), dir, "ocean", def, nil); err == nil {
		t.Fatal("case-folded duplicate accepted")
	}
	renamed, _, err := SaveTheme(t.Context(), dir, "Sea", saved.Definition, saved)
	if err != nil || renamed.Path != saved.Path || renamed.Name != "Sea" {
		t.Fatalf("rename changed selected path: %+v %v", renamed, err)
	}
	if _, _, err := SaveTheme(t.Context(), dir, "ocean", def, nil); err == nil {
		t.Fatal("renamed theme filename no longer reserves its portable stem")
	}
	entries, err := LoadSavedThemes(t.Context(), dir)
	if err != nil || len(entries) != 1 || entries[0].Name != "Sea" || !entries[0].Managed() {
		t.Fatalf("discover: %+v %v", entries, err)
	}
	changed := CloneThemeFile(saved.Definition)
	changed.Colors["blue"] = "#654321"
	if _, _, err := SaveTheme(t.Context(), dir, "Sea", changed, saved); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	export := filepath.Join(t.TempDir(), "export.toml")
	if ok, err := ExportTheme(t.Context(), export, renamed.Definition); err != nil || !ok {
		t.Fatal(ok, err)
	}
	roundtrip, err := LoadThemeFile(t.Context(), export)
	if err != nil || roundtrip.Name != "Sea" || roundtrip.Colors["blue"] != "#123456" || roundtrip.Syntax["number"] != "#abcdef" {
		t.Fatal(roundtrip, err)
	}
	if _, err := ExportTheme(t.Context(), export, changed); !errors.Is(err, ErrFileChanged) {
		t.Fatal("export replaced existing file", err)
	}
	if got, err := LoadThemeFile(t.Context(), export); err != nil || got.Colors["blue"] != "#123456" {
		t.Fatal("refused export changed original", err)
	}
}

func TestThemeLibraryCancellationInvalidEntriesAndIndependentDrafts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Broken.toml"), []byte("[colors]\nblue='bad'"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Valid.toml"), []byte("name='Valid'\nbase='light'"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadSavedThemes(t.Context(), dir)
	if err != nil || len(entries) != 2 || entries[0].Err == nil || entries[1].Definition == nil {
		t.Fatal("one invalid entry hid the library", entries, err)
	}
	clone := CloneThemeFile(entries[1].Definition)
	clone.Colors["blue"] = "#abcdef"
	if len(entries[1].Definition.Colors) != 0 {
		t.Fatal("editor draft aliased stored theme")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LoadSavedThemes(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, published, err := SaveTheme(ctx, dir, "Canceled", clone, nil); published || !errors.Is(err, context.Canceled) {
		t.Fatal(published, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Canceled.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled save published", err)
	}
	for _, name := range []string{"../outside", "CON", "COM1", "trailing ", "bad:name", ""} {
		if err := ValidateThemeName(name); err == nil {
			t.Fatalf("invalid theme name accepted: %q", name)
		}
	}
}

func TestThemeUnchangedStaleDraftRefusesNewSnapshot(t *testing.T) {
	dir := t.TempDir()
	def := &ThemeFile{Base: "dark", Colors: map[string]string{"blue": "#123456"}, Syntax: map[string]string{}}
	saved, _, err := SaveTheme(t.Context(), dir, "Snapshot", def, nil)
	if err != nil {
		t.Fatal(err)
	}
	external := CloneThemeFile(saved.Definition)
	external.Colors["blue"] = "#abcdef"
	data, err := MarshalThemeFile(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if next, published, err := SaveTheme(t.Context(), dir, "Snapshot", saved.Definition, saved); next != nil || published || !errors.Is(err, ErrFileChanged) {
		t.Fatal("unchanged stale draft claimed success", next, published, err)
	}
	loaded, err := LoadThemeFile(t.Context(), saved.Path)
	if err != nil || loaded.Colors["blue"] != "#abcdef" {
		t.Fatal("refused edit overwrote external data", err)
	}
}

func TestThemeDiscoveryRefusesOversizedSnapshotAndRecheck(t *testing.T) {
	dir := t.TempDir()
	saved, _, err := SaveTheme(t.Context(), dir, "Bounded", &ThemeFile{Base: "dark"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(saved.Path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(8 << 20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadSavedThemes(t.Context(), dir)
	if err != nil || len(entries) != 1 || !errors.Is(entries[0].Err, ErrThemeFileInvalid) {
		t.Fatal("oversized discovery was not a visible invalid entry", entries, err)
	}
	if _, published, err := SaveTheme(t.Context(), dir, "Bounded", saved.Definition, saved); published || !errors.Is(err, ErrThemeFileInvalid) {
		t.Fatal("snapshot recheck admitted oversized replacement", published, err)
	}
}

func TestThemeDiscoveryKeepsDisappearedFileAsInvalidEntry(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "Disappeared.toml")
	valid := filepath.Join(dir, "Valid.toml")
	for _, path := range []string{missing, valid} {
		if err := os.WriteFile(path, []byte("base='light'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := loadSavedThemes(t.Context(), dir, func(ctx context.Context, path string) (*File, error) {
		if path == missing {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		return openFileWithReader(ctx, path, readThemeInput)
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("discovery lost visible entries: %+v %v", entries, err)
	}
	disappeared := entries[0]
	if disappeared.Name != "Disappeared" || !errors.Is(disappeared.Err, os.ErrNotExist) {
		t.Fatalf("disappeared theme did not retain its read failure: %+v", disappeared)
	}
	if disappeared.Definition != nil || disappeared.Managed() {
		t.Fatal("disappeared theme became an editable default palette")
	}
	validTheme := entries[1]
	if validTheme.Err != nil || validTheme.Definition == nil {
		t.Fatalf("disappeared sibling hid the valid theme: %+v", validTheme)
	}
	if validTheme.Definition.Base != "light" || !validTheme.Managed() {
		t.Fatalf("valid sibling lost its editable palette: %+v", validTheme)
	}
}

func TestThemeDiscoveryPropagatesCancellationDuringFinalRead(t *testing.T) {
	for _, readError := range []bool{false, true} {
		name := "after successful snapshot"
		if readError {
			name = "bounded reader reports cancellation"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Final.toml")
			if err := os.WriteFile(path, []byte("base='dark'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := false
			entries, err := loadSavedThemes(ctx, dir, func(ctx context.Context, path string) (*File, error) {
				entered = true
				if readError {
					return openFileWithReader(ctx, path, func(ctx context.Context, path string) ([]byte, error) {
						if _, err := readThemeInput(ctx, path); err != nil {
							return nil, err
						}
						cancel()
						return nil, ctx.Err()
					})
				}
				snapshot, err := openFileWithReader(ctx, path, readThemeInput)
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				return snapshot, nil
			})
			if !entered {
				t.Fatal("control never reached the final theme read")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("final-read cancellation was reported as successful discovery: %+v %v", entries, err)
			}
			if entries != nil {
				t.Fatal("canceled discovery published a partial theme library")
			}
		})
	}
}

func TestThemeNameValidationKeepsTextAndDeviceBoundaries(t *testing.T) {
	const invalidText = "theme name must be 1–64 bytes without path separators, " +
		"reserved punctuation, controls or surrounding spaces"
	const reservedDevice = "theme name is reserved on Windows"
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "ordinary", value: "Ocean"},
		{name: "internal space", value: "My Theme"},
		{name: "maximum length", value: strings.Repeat("a", 64)},
		{name: "short device prefix", value: "COM"},
		{name: "long device prefix", value: "COM10"},
		{name: "nondigit device suffix", value: "LPTx"},
		{name: "unicode name", value: "海"},
		{name: "unicode suffix", value: "COM¹"},
		{name: "empty", want: invalidText},
		{name: "leading space", value: " Ocean", want: invalidText},
		{name: "trailing space", value: "Ocean ", want: invalidText},
		{name: "over maximum length", value: strings.Repeat("a", 65), want: invalidText},
		{name: "invalid UTF-8", value: "\xff", want: invalidText},
		{name: "punctuation", value: "Ocean.blue", want: invalidText},
		{name: "control", value: "Ocean\nBlue", want: invalidText},
		{name: "device case", value: "cOn", want: reservedDevice},
		{name: "first device digit", value: "COM0", want: reservedDevice},
		{name: "last device digit", value: "LPT9", want: reservedDevice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateThemeName(tc.value)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("validation error = %v; want %q", err, tc.want)
			}
		})
	}
}
