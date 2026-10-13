package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLegacyLiteralThemePathsPreserveBytesAndRecordedPolicies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, path := range []string{"~", "~/palette.toml", "$PALETTE/palette.toml", "${PALETTE}/palette.toml", "palettes/$NAME.toml", filepath.Join(dir, "$absolute.toml"), "ordinary/palette.toml", ""} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			original := []byte("[theme]\npath='" + path + "'\n[[profiles]]\nname='default'\n")
			sourcePath := filepath.Join(dir, "source", "legacy.toml")
			migration, err := PrepareLegacyMigration(t.Context(), original, sourcePath, "")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := migration.Candidate()
			if err != nil {
				t.Fatal("literal path was rejected as shell expansion", err)
			}
			file := filepath.Join(t.TempDir(), "settings.toml")
			writeTransactionFile(t, file, candidate)
			loaded, err := Load(Options{Path: file, Environ: func() []string { return nil }})
			want := path
			if path != "" && !filepath.IsAbs(path) {
				want = filepath.Join(filepath.Dir(sourcePath), path)
			}
			if err != nil || loaded.Config.ThemePath != want || !bytes.Equal(migration.Original(), original) {
				t.Fatal("literal path or exact source bytes changed", err)
			}
			for version := 1; version <= 7; version++ {
				old, err := prepareLegacyMigration(t.Context(), original, sourcePath, "", version)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := old.Candidate()
				blocked := path == "~" || strings.HasPrefix(path, "~/") || strings.Contains(path, "$")
				if blocked {
					if !slices.Equal(old.Unmapped(), []string{"theme.path"}) || candidate != nil || !errors.Is(err, ErrLegacyPreferencesUnmapped) {
						t.Fatal("recorded policy reinterpreted an unsupported path")
					}
				} else if err != nil || len(old.Unmapped()) != 0 {
					t.Fatal("recorded ordinary path mapping changed", err)
				}
			}
		})
	}
}

func TestLegacyLiteralThemeRelativeOriginMustBeExplicit(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"~", "~/palette.toml", "$PALETTE/palette.toml", "ordinary/palette.toml"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			original := []byte("[theme]\npath='" + path + "'\n[[profiles]]\nname='default'\n")
			for _, source := range []string{"", "relative/legacy.toml"} {
				migration, err := PrepareLegacyMigration(t.Context(), original, source, "")
				if migration != nil || !errors.Is(err, ErrLegacyConfig) {
					t.Fatal("relative literal theme path guessed a source directory", err)
				}
			}
		})
	}
}

func TestDeliveredPolicySevenFixtureReopensAndRollsBack(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 7)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 7 || !bytes.Contains(recovery.Candidate, []byte("account_id = \"literal,account\"")) {
		t.Fatal("independently delivered policy7 fixture could not reopen", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy7 exact rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy7 target absence lost", err)
	}
	got, err := os.ReadFile(recovery.Source.Path)
	if err != nil || !bytes.Equal(got, recovery.Source.Contents) {
		t.Fatal("policy7 original source bytes changed", err)
	}
}
