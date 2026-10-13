package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func refuseEnvironmentProvider(t *testing.T) MigrationStores {
	t.Helper()
	return func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		t.Fatal("externally owned environment migration opened a provider")
		return nil, core.ErrCredentialRecoveryInvalid
	}
}

func TestLegacyEnvironmentTransactionPublishesOnlyConfigurationAndRollsBackExactly(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"in place", "absent", "existing", "candidate"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newTransactionTest(t, core.CredentialBackendEnvironment, kind)
			if f.transaction.record.CredentialBackup != "" || f.transaction.record.CredentialState != "unexamined" || f.transaction.record.SourceCredential.EnvironmentKey != "JIRA_TOKEN_WORK" || f.transaction.record.SourceCredential != f.transaction.record.TargetCredential {
				t.Fatal("preparation invented owned credential state or lost selected scope")
			}
			receipt, err := f.transaction.Apply(t.Context(), refuseEnvironmentProvider(t))
			if err != nil || receipt.Status != "applied" || receipt.CredentialState != "external" || !receipt.JournalDurable || len(receipt.Effects) != 1 || receipt.Effects[0].Operation != "configuration_apply" || receipt.Effects[0].Status != "acknowledged" || receipt.Effects[0].Attempted == nil || *receipt.Effects[0].Attempted != (kind != "candidate") {
				t.Fatal("environment migration fabricated credential effects or lost config outcome", err)
			}
			resolved, err := Load(Options{Path: f.targetPath, Environ: func() []string { return []string{"JIRA_TOKEN_WORK=synthetic-named-value"} }})
			if err != nil || resolved.Config.APITokenEnv != "JIRA_TOKEN_WORK" || resolved.Config.APIToken != "synthetic-named-value" || resolved.Config.Email != "work@example.invalid" {
				t.Fatal("migrated reference did not resolve flat identity", err)
			}
			for _, path := range []string{receipt.Configuration, receipt.Journal, f.targetPath} {
				data, err := os.ReadFile(path)
				if err != nil || bytes.Contains(data, []byte("synthetic-named-value")) {
					t.Fatal("token entered configuration recovery", err)
				}
			}
			if _, err := f.transaction.Apply(t.Context(), nil); !errors.Is(err, ErrLegacyTransactionStarted) {
				t.Fatal("environment apply was replayable", err)
			}
			reopened := f.reload(t)
			preview, err := reopened.PreviewRollback(t.Context())
			if err != nil || preview.CredentialInspectionRequired || preview.WouldRestoreConfiguration != (kind != "candidate") {
				t.Fatal("preview claimed owned credential inspection", err)
			}
			restored, err := reopened.Rollback(t.Context(), refuseEnvironmentProvider(t))
			if err != nil || restored.Status != "rolled_back" || restored.CredentialState != "external" || !restored.JournalDurable {
				t.Fatal("config-only rollback lost external state", err)
			}
			f.assertRestored(t)
			if _, err := f.reload(t).Rollback(t.Context(), nil); err != nil {
				t.Fatal("fresh idempotent environment rollback failed", err)
			}
		})
	}
}

func TestLegacyEnvironmentTransactionRetainsEveryJournalPublicationFailure(t *testing.T) {
	t.Parallel()
	for boundary := 1; boundary <= 4; boundary++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("publication=%d/after=%v", boundary, after), func(t *testing.T) {
				t.Parallel()
				f := newTransactionTest(t, core.CredentialBackendEnvironment, "absent")
				failure := errors.New("synthetic journal durability failure")
				calls := 0
				original := f.transaction.ops.saveJournal
				f.transaction.ops.saveJournal = func(ctx context.Context, file *File, data []byte, validate func([]byte) error) error {
					calls++
					if calls == boundary && !after {
						return failure
					}
					if err := original(ctx, file, data, validate); err != nil {
						return err
					}
					if calls == boundary {
						return failure
					}
					return nil
				}
				receipt, err := f.transaction.Apply(t.Context(), refuseEnvironmentProvider(t))
				if !errors.Is(err, failure) || calls != boundary || receipt.JournalDurable {
					t.Fatal("journal failure falsely durable or bypassed", err)
				}
				_, statErr := os.Stat(f.targetPath)
				if boundary <= 2 && !errors.Is(statErr, os.ErrNotExist) || boundary > 2 && statErr != nil {
					t.Fatal("configuration write escaped durable intent", statErr)
				}
				if _, err := f.reload(t).Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
					t.Fatal("fresh rollback could not recover journal failure", err)
				}
				f.assertRestored(t)
			})
		}
	}
}

func TestLegacyEnvironmentTransactionPreservesUncertainWritesAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"before write", "after write", "canceled after write"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newTransactionTest(t, core.CredentialBackendEnvironment, "existing")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic configuration write failure")
			original := f.transaction.ops.applyConfig
			f.transaction.ops.applyConfig = func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
				if mode != "before write" {
					if _, err := original(ctx, recovery); err != nil {
						return false, err
					}
				}
				if mode == "canceled after write" {
					cancel()
					return true, nil
				}
				return false, failure
			}
			receipt, err := f.transaction.Apply(ctx, refuseEnvironmentProvider(t))
			if len(receipt.Effects) != 1 || receipt.CredentialState != "external" {
				t.Fatal("configuration failure invented credential effect")
			}
			if mode == "canceled after write" {
				if !errors.Is(err, context.Canceled) || receipt.JournalDurable || receipt.Effects[0].Status != "acknowledged" {
					t.Fatal("cancellation lost acknowledged write", err)
				}
				if disk := f.reload(t); disk.record.Effects[0].Status != "pending" || disk.record.Effects[0].Attempted != nil {
					t.Fatal("canceled outcome fabricated durable acknowledgment")
				}
			} else if !errors.Is(err, failure) || receipt.Effects[0].Status != "uncertain" || receipt.Effects[0].Attempted != nil {
				t.Fatal("false plus error fabricated no-write", err)
			}
			if _, err := f.reload(t).Rollback(t.Context(), nil); err != nil {
				t.Fatal("failed environment write could not recover", err)
			}
			f.assertRestored(t)
		})
	}
}

func TestLegacyEnvironmentRollbackRefusesChangedResourcesBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"source", "target", "artifact", "journal"} {
		t.Run(changed, func(t *testing.T) {
			t.Parallel()
			f := newTransactionTest(t, core.CredentialBackendEnvironment, "existing")
			if _, err := f.transaction.Apply(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			reopened := f.reload(t)
			path := map[string]string{"source": f.sourcePath, "target": f.targetPath, "artifact": reopened.configuration.file.path, "journal": reopened.file.path}[changed]
			writeTransactionFile(t, path, []byte("# external edit\n"))
			before := map[string][]byte{}
			for _, name := range []string{f.sourcePath, f.targetPath, reopened.configuration.file.path, reopened.file.path} {
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				before[name] = data
			}
			if _, err := reopened.Rollback(t.Context(), refuseEnvironmentProvider(t)); err == nil {
				t.Fatal("changed resource was overwritten")
			}
			for name, want := range before {
				got, err := os.ReadFile(name)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("rollback wrote before preflight", err)
				}
			}
		})
	}
}

func TestLegacyEnvironmentJournalRejectsInventedOwnershipAndOldPolicy(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"old journal", "old mapping", "backup", "before commitment", "native state", "wrong name", "native effects"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			f := newTransactionTest(t, core.CredentialBackendEnvironment, "absent")
			if _, err := f.transaction.Apply(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			record := f.transaction.record
			switch field {
			case "old journal":
				record.Version = 1
			case "old mapping":
				f.transaction.configuration.record.Version = 1
			case "backup":
				record.CredentialBackup = core.NewCredentialBackupID()
			case "before commitment":
				record.BeforeCommitment = "invented"
			case "native state":
				record.CredentialState = "absent"
			case "wrong name":
				record.SourceCredential.EnvironmentKey = "JIRA_TOKEN_OTHER"
			case "native effects":
				record.Effects = []MigrationEffect{{Sequence: 1, Operation: "credential_backup", Status: "acknowledged", Attempted: migrationAttempt(true)}}
			}
			if err := f.transaction.validate(t.Context(), record); err == nil {
				t.Fatal("forged environment ownership accepted")
			}
		})
	}
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendOnePassword} {
		f := newTransactionTest(t, backend, "absent")
		f.transaction.record.CredentialState = "external"
		if err := f.transaction.validate(t.Context(), f.transaction.record); err == nil {
			t.Fatal("native provider accepted external state")
		}
	}
}

// The fixtures were produced by the delivered version-1 CLI, independently of
// the current converter. Only paths and the enclosing artifact hash are rehomed.
func loadVersionOneFixture(t *testing.T) (string, legacyRecoveryRecord) {
	return loadDeliveredMigrationFixture(t, 1)
}

func loadDeliveredMigrationFixture(t *testing.T, version int) (string, legacyRecoveryRecord) {
	t.Helper()
	var recovery legacyRecoveryRecord
	var journal legacyTransactionRecord
	for name, target := range map[string]any{fmt.Sprintf("migration-v%d-configuration.json", version): &recovery, fmt.Sprintf("migration-v%d-migration.json", version): &journal} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := jsonv2.Unmarshal(data, target, jsonv2.RejectUnknownMembers(true)); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	recovery.Source.Requested = filepath.Join(dir, "old.toml")
	recovery.Source.Path = recovery.Source.Requested
	recovery.Target.Requested = filepath.Join(dir, "new.toml")
	recovery.Target.Path = recovery.Target.Requested
	writeTransactionFile(t, recovery.Source.Path, recovery.Source.Contents)
	writeTransactionFile(t, recovery.Target.Path, recovery.Candidate)
	journal.Configuration = filepath.Join(dir, "configuration.json")
	data, err := jsonv2.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	writeTransactionFile(t, journal.Configuration, data)
	journal.ConfigurationSHA256 = recoveryDigest(data)
	data, err = jsonv2.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "migration.json")
	writeTransactionFile(t, path, data)
	return path, recovery
}

func TestLegacyVersionOneDeliveredFixtureReopensAndRollsBackWithoutReinterpretation(t *testing.T) {
	t.Parallel()
	path, recovery := loadVersionOneFixture(t)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil {
		t.Fatal("delivered version-1 receipt became unreadable", err)
	}
	if recovery.Version != 1 || transaction.record.Version != 1 || bytes.Contains(recovery.Candidate, []byte("api_token_env")) {
		t.Fatal("fixture no longer proves historical mapping")
	}
	current, err := PrepareLegacyMigration(t.Context(), recovery.Source.Contents, recovery.Source.Path, "work")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := current.Candidate()
	if err != nil || !bytes.Contains(candidate, []byte("api_token_env")) || bytes.Equal(candidate, recovery.Candidate) {
		t.Fatal("new mapping no longer distinguishes old bytes", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("historical config-only receipt could not roll back", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("historical destination absence lost", err)
	}
	source, err := os.ReadFile(recovery.Source.Path)
	if err != nil || !bytes.Equal(source, recovery.Source.Contents) {
		t.Fatal("complete historical source changed", err)
	}
	reopened, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || reopened.record.Version != 1 || reopened.configuration.record.Version != 1 || reopened.record.Status != "rolled_back" {
		t.Fatal("rollback upgraded historical policy", err)
	}
	recovery.Version = 2
	if err := recovery.validate(t.Context()); err == nil {
		t.Fatal("new mapping silently accepted old candidate bytes")
	}
}
