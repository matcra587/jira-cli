package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLegacyAutomaticThemeMappingKeepsRecordedPolicies(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"auto", " AUTO "} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := []byte("default_profile='work'\n[theme]\nname='" + name + "'\n[[profiles]]\nname='work'\nsecret_backend='env'\n")
			for version := 1; version <= 5; version++ {
				prepared, err := prepareLegacyMigration(t.Context(), source, "", "work", version)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := prepared.Candidate()
				if version < 5 {
					if !slices.Contains(prepared.Unmapped(), "theme.name") || !errors.Is(err, ErrLegacyPreferencesUnmapped) || candidate != nil {
						t.Fatal("older policy lost automatic-theme refusal")
					}
					continue
				}
				if err != nil || len(prepared.Unmapped()) != 0 || !bytes.Contains(candidate, []byte("theme = \""+name+"\"")) {
					t.Fatal("automatic theme lost its authored selector", err)
				}
			}
		})
	}
}

func TestDeliveredPolicyFourFixtureReopensAndRollsBack(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 4)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 4 || !bytes.Contains(recovery.Candidate, []byte("default_board = \"42\"")) {
		t.Fatal("delivered policy4 could not reopen", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy4 exact rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !os.IsNotExist(err) {
		t.Fatal("policy4 target absence lost", err)
	}
}

func TestAutomaticThemeWritableChoicesPreserveAuthoredValue(t *testing.T) {
	t.Parallel()
	if !slices.Contains(SettingValueChoices("theme"), "auto") {
		t.Fatal("automatic theme is missing from offline choices")
	}
	for _, name := range []string{"auto", " AUTO "} {
		if err := ValidateSettingValue("theme", name); err != nil {
			t.Fatal("automatic theme is not writable", err)
		}
	}
	path := filepath.Join(t.TempDir(), "palette.toml")
	if err := os.WriteFile(path, []byte("base='auto'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadThemeFile(t.Context(), path); !errors.Is(err, ErrThemeFileInvalid) {
		t.Fatal("application auto selector became a concrete custom palette base", err)
	}
}
