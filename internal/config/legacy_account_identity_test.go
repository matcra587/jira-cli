package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLegacyAccountIdentityMapsSelectedPublicScopeAndRetainsOlderRefusal(t *testing.T) {
	t.Parallel()
	source := []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://SITE.example.invalid/jira/'\nemail='public@example.invalid'\ncloud_id='bound-cloud'\naccount_id='literal,account'\n[[profiles]]\nname='other'\naccount_id='other-id'\n")
	migration, err := PrepareLegacyMigration(t.Context(), source, "", "work")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := migration.Candidate()
	if err != nil {
		t.Fatal("selected cached identity remains unmapped", err)
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	writeTransactionFile(t, path, candidate)
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.CachedAccountID() != "literal,account" || loaded.Config.AccountIDSite != "https://site.example.invalid/jira" || loaded.Config.AccountIDEmail != "public@example.invalid" || loaded.Config.AccountIDCloudID != "bound-cloud" {
		t.Fatal("migration changed selected ID or independent scope", err)
	}
	for version := 1; version <= 6; version++ {
		older, err := prepareLegacyMigration(t.Context(), source, "", "work", version)
		if err != nil || !slices.Contains(older.Unmapped(), "profiles.work.account_id") {
			t.Fatal("older policy reinterpreted unsupported account identity", err)
		}
	}
}

func TestLegacyAccountIdentityKeepsAbsentAndExplicitEmpty(t *testing.T) {
	t.Parallel()
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[declared], func(t *testing.T) {
			t.Parallel()
			source := "[[profiles]]\nname='default'\n"
			if declared {
				source += "account_id=''\n"
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
			for _, key := range []string{"account_id", "account_id_site", "account_id_email", "account_id_cloud_id"} {
				setting, err := file.Setting(key)
				if err != nil || setting.Present != declared || setting.Value != "" {
					t.Fatal("migration changed absent/empty identity or invented binding", err)
				}
			}
		})
	}
}

func TestDeliveredPolicySixFixtureReopensAndRollsBack(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 6)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 6 {
		t.Fatal("independently delivered policy6 could not reopen", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy6 exact rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy6 destination absence lost", err)
	}
}

func TestBoundAccountIdentityMigrationPublishesAndRollsBackExactPreimages(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"in place", "absent", "existing"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "old.toml")
			original := []byte("# exact source\ndefault_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://SITE.example.invalid/jira/'\nemail='public@example.invalid'\naccount_id='cached,account'\nsecret_backend='env'\n[[profiles]]\nname='other'\naccount_id='other-id'\n")
			writeTransactionFile(t, sourcePath, original)
			targetPath, before := sourcePath, original
			if kind != "in place" {
				targetPath, before = filepath.Join(dir, "new.toml"), nil
			}
			if kind == "existing" {
				before = []byte("# exact target\naccount_id='older-id'\n")
				writeTransactionFile(t, targetPath, before)
			}
			source, err := OpenFile(t.Context(), sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			target, err := OpenFile(t.Context(), targetPath)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PrepareLegacyTransaction(t.Context(), source, target, "work", "")
			if err != nil {
				t.Fatal(err)
			}
			transaction, published, err := plan.Create(t.Context(), filepath.Join(dir, "recovery"))
			if err != nil || !published {
				t.Fatal("identity recovery was not published", err)
			}
			receipt, err := transaction.Apply(t.Context(), refuseEnvironmentProvider(t))
			if err != nil || receipt.Status != "applied" || receipt.CredentialState != "external" || transaction.configuration.record.Version != legacyMigrationVersion {
				t.Fatal("identity migration did not apply with honest external credentials", err)
			}
			loaded, err := Load(Options{Path: targetPath, Environ: func() []string { return nil }})
			if err != nil || loaded.Config.CachedAccountID() != "cached,account" {
				t.Fatal("migrated ID cannot serve its selected public scope", err)
			}
			reopened, err := LoadLegacyTransaction(t.Context(), receipt.Journal)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := reopened.Rollback(t.Context(), refuseEnvironmentProvider(t))
			if err != nil || restored.Status != "rolled_back" {
				t.Fatal("bound identity rollback failed", err)
			}
			contents, err := os.ReadFile(targetPath)
			if before == nil {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("rollback invented absent target", err)
				}
			} else if err != nil || !bytes.Equal(contents, before) {
				t.Fatal("rollback changed exact identity/source preimage", err)
			}
			if kind != "in place" {
				contents, err := os.ReadFile(sourcePath)
				if err != nil || !bytes.Equal(contents, original) {
					t.Fatal("migration changed unselected/source metadata", err)
				}
			}
		})
	}
}
