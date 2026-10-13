package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLegacySelectedClientPreferencesRetainMetadata(t *testing.T) {
	t.Parallel()
	for _, interval := range []string{"45", "0", "-1"} {
		t.Run(interval, func(t *testing.T) {
			t.Parallel()
			source := []byte("default_profile='work'\n[[profiles]]\nname='work'\nrefresh_interval=" + interval + "\nteam_account_ids=['a,b',' a ','a,b','']\n[[profiles]]\nname='other'\nrefresh_interval=77\nteam_account_ids=['other']\n")
			migration, err := PrepareLegacyMigration(t.Context(), source, "", "work")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := migration.Candidate()
			if err != nil {
				t.Fatal("client-preference metadata remains unmapped", err)
			}
			path := filepath.Join(t.TempDir(), "settings.toml")
			writeTransactionFile(t, path, candidate)
			loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			want := "30"
			if interval == "45" {
				want = "45"
			}
			if loaded.Config.Values()["refresh_interval"] != want || loaded.Config.Values()["team_account_ids"] != `["a,b"," a ","a,b",""]` {
				t.Fatal("selected metadata changed order, IDs, fallback or profile")
			}
			for version := 1; version <= 5; version++ {
				older, err := prepareLegacyMigration(t.Context(), source, "", "work", version)
				if err != nil || !slices.Contains(older.Unmapped(), "profiles.work.refresh_interval") || !slices.Contains(older.Unmapped(), "profiles.work.team_account_ids") {
					t.Fatal("recorded policy lost unsupported metadata refusal", err)
				}
			}
		})
	}
}

func TestClientPreferencePublicationPreservesTypedArrayAndPresence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSettings(map[string]string{"refresh_interval": "45", "team_account_ids": `["a,b"," a ","a,b",""]`})
	if err != nil {
		t.Fatal("client preferences are not writable", err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatal("client preferences did not publish", err)
	}
	file, err = ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	setting, err := file.Setting("team_account_ids")
	if err != nil || !setting.Present || setting.Value != `["a,b"," a ","a,b",""]` {
		t.Fatal("published metadata lost values or declaration", err)
	}
	cleared, err := file.PrepareSetting("team_account_ids", "[]")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := cleared.Save(t.Context()); err != nil || !changed {
		t.Fatal("explicit empty array did not publish", err)
	}
}

func TestDeliveredPolicyFiveFixtureReopensAndRollsBack(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 5)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 5 {
		t.Fatal("delivered policy5 could not reopen", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy5 exact rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy5 target absence lost", err)
	}
}

func TestLegacyTeamMetadataKeepsAbsentAndEmptyDeclarations(t *testing.T) {
	t.Parallel()
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[declared], func(t *testing.T) {
			t.Parallel()
			source := "[[profiles]]\nname='default'\n"
			if declared {
				source += "team_account_ids=[]\n"
			}
			migration, err := PrepareLegacyMigration(t.Context(), []byte(source), "", "")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := migration.Candidate()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "settings.toml")
			writeTransactionFile(t, path, candidate)
			file, err := ReadFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			setting, err := file.Setting("team_account_ids")
			if err != nil || setting.Present != declared || declared && setting.Value != "[]" {
				t.Fatal("migration invented or discarded an authored empty team array", err)
			}
		})
	}
}
