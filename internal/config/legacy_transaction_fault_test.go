package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestLegacyTransactionRecoversEveryApplyMutationFailure(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"backup", "apply", "configuration"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", operation, after), func(t *testing.T) {
				f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
				failure := errors.New("native failure contains " + f.source.value)
				f.provider.onMutation = func(current string, completed bool) error {
					if current == operation && completed == after {
						return failure
					}
					return nil
				}
				if operation == "configuration" {
					realApply := f.transaction.ops.applyConfig
					f.transaction.ops.applyConfig = func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
						if after {
							if _, err := realApply(ctx, recovery); err != nil {
								return false, err
							}
						}
						// Simulate the shared writer's possibly applied false+error.
						return false, failure
					}
				}
				receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
				if !errors.Is(err, failure) || receipt.Status == "applied" || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, receipt), f.source.value) {
					t.Fatal("failed mutation lost cause, claimed completion or rendered secret")
				}
				last := receipt.Effects[len(receipt.Effects)-1]
				if operation == "configuration" {
					if last.Status != "uncertain" || last.Attempted != nil {
						t.Fatal("file false+error was misreported as no write")
					}
				} else if last.Attempted == nil || *last.Attempted != after || after && last.Status != "uncertain" || !after && last.Status != "failed" {
					t.Fatal("native entry facts were lost")
				}
				f.provider.onMutation = nil
				if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil {
					t.Fatal(err)
				}
				f.assertRestored(t)
			})
		}
	}
}

func TestLegacyTransactionRecoversEveryJournalPublicationFailure(t *testing.T) {
	t.Parallel()
	// State, three intent/outcome pairs and final completion all have their
	// own publication boundary. Exercise errors before and after replacement.
	for boundary := 1; boundary <= 8; boundary++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("publication=%d/after=%v", boundary, after), func(t *testing.T) {
				f := newTransactionTest(t, core.CredentialBackendKeyring, "absent")
				failure := errors.New("journal durability failure")
				calls := 0
				originalSave := f.transaction.ops.saveJournal
				f.transaction.ops.saveJournal = func(ctx context.Context, file *File, data []byte, validate func([]byte) error) error {
					calls++
					if calls == boundary && !after {
						return failure
					}
					if err := originalSave(ctx, file, data, validate); err != nil {
						return err
					}
					if calls == boundary {
						return failure
					}
					return nil
				}
				receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
				if !errors.Is(err, failure) || calls != boundary || receipt.JournalDurable {
					t.Fatal("journal failure was bypassed or falsely durable")
				}
				backup, apply := false, false
				for _, operation := range f.provider.mutations {
					backup = backup || operation == "backup"
					apply = apply || operation == "apply"
				}
				if backup != (boundary > 2) || apply != (boundary > 4) {
					t.Fatal("dependent mutation ran without acknowledged intent publication")
				}
				if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil {
					t.Fatal(err)
				}
				f.assertRestored(t)
			})
		}
	}
}

func TestLegacyTransactionCancellationRetainsAcknowledgedWriteAndPendingIntent(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.provider.onMutation = func(operation string, after bool) error {
		if operation == "apply" && after {
			cancel()
		}
		return nil
	}
	receipt, err := f.transaction.Apply(ctx, f.provider.store)
	if !errors.Is(err, context.Canceled) || receipt.JournalDurable || receipt.Effects[1].Status != "acknowledged" || !*receipt.Effects[1].Attempted {
		t.Fatal("cancellation discarded acknowledged native outcome")
	}
	onDisk := f.reload(t)
	if onDisk.record.Effects[1].Status != "pending" || onDisk.record.Effects[1].Attempted != nil || len(f.provider.mutations) != 2 {
		t.Fatal("canceled invocation fabricated durability or detached cleanup")
	}
	data, err := os.ReadFile(f.targetPath)
	if err != nil || !bytes.Equal(data, f.original) || f.provider.values[f.target.location] == f.target {
		t.Fatal("canceled write did not preserve actual independent postimages")
	}
	f.provider.onMutation = nil
	if _, err := onDisk.Rollback(t.Context(), f.provider.store); err != nil {
		t.Fatal(err)
	}
	f.assertRestored(t)
}

func TestLegacyTransactionBackupValidationPreservesCancellationCause(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.provider.onReadBackup = cancel
	receipt, err := f.transaction.Apply(ctx, f.provider.store)
	if !errors.Is(err, context.Canceled) || len(f.provider.mutations) != 1 || len(receipt.Effects) != 1 || receipt.Effects[0].Status != "acknowledged" {
		t.Fatal("post-backup validation lost cancellation or entered primary write")
	}
	data, err := os.ReadFile(f.targetPath)
	if err != nil || !bytes.Equal(data, f.original) || f.provider.values[f.target.location] != f.target {
		t.Fatal("canceled validation changed a primary preimage")
	}
}

func TestLegacyTransactionSuccessfulNoopCannotFabricateConfigurationPostimage(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
	f.transaction.ops.applyConfig = func(context.Context, *LegacyRecovery) (bool, error) { return false, nil }
	if receipt, err := f.transaction.Apply(t.Context(), f.provider.store); !errors.Is(err, ErrFileChanged) || receipt.Status == "applied" {
		t.Fatal("successful no-op acknowledgment fabricated a configuration postimage")
	}
	if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil {
		t.Fatal(err)
	}
	f.assertRestored(t)
}

func TestLegacyTransactionRollbackPreflightsAllResourcesBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"configuration", "credential", "source", "backup", "backup-before-value", "backup-before-metadata", "backup-expected-token", "backup-and-credential-token", "artifact", "journal"} {
		t.Run(changed, func(t *testing.T) {
			f := newTransactionTest(t, core.CredentialBackendKeyring, "existing")
			if _, err := f.transaction.Apply(t.Context(), f.provider.store); err != nil {
				t.Fatal(err)
			}
			transaction := f.reload(t)
			switch changed {
			case "configuration":
				writeTransactionFile(t, f.targetPath, []byte("read_only=false\n"))
			case "credential":
				state := f.provider.values[f.target.location]
				state.value = "external credential"
				f.provider.values[f.target.location] = state
			case "source":
				writeTransactionFile(t, f.sourcePath, []byte(transactionFixture+"editor='other-editor'\n"))
			case "backup":
				pair := f.provider.backups[transaction.record.CredentialBackup]
				pair.expected.value = `{"version":1,"site":"https://other.invalid","email":"other@example.invalid","token":"different"}`
				f.provider.backups[transaction.record.CredentialBackup] = pair
			case "backup-before-value", "backup-before-metadata", "backup-expected-token", "backup-and-credential-token":
				pair := f.provider.backups[transaction.record.CredentialBackup]
				switch changed {
				case "backup-before-value":
					pair.before.value = "altered preimage"
				case "backup-before-metadata":
					pair.before.metadata = "altered metadata"
				case "backup-expected-token", "backup-and-credential-token":
					pair.expected.value = strings.ReplaceAll(pair.expected.value, f.source.value, "altered token")
					if changed == "backup-and-credential-token" {
						f.provider.values[f.target.location] = pair.expected
					}
				}
				f.provider.backups[transaction.record.CredentialBackup] = pair
			case "artifact":
				writeTransactionFile(t, transaction.configuration.file.path, []byte("{}"))
			case "journal":
				writeTransactionFile(t, transaction.file.path, []byte("{}"))
			}
			count := len(f.provider.mutations)
			configBefore, err := os.ReadFile(f.targetPath)
			if err != nil {
				t.Fatal(err)
			}
			credentialBefore := f.provider.values[f.target.location]
			if _, err := transaction.Rollback(t.Context(), f.provider.store); err == nil {
				t.Fatal("changed resource allowed rollback")
			}
			configAfter, err := os.ReadFile(f.targetPath)
			if err != nil || !bytes.Equal(configBefore, configAfter) || len(f.provider.mutations) != count || f.provider.values[f.target.location] != credentialBefore {
				t.Fatal("rollback wrote one resource before validating the other")
			}
		})
	}
}

func TestLegacyTransactionInterruptedRollbackCanReopenAndFinish(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"restore", "configuration", "journal"} {
		t.Run(operation, func(t *testing.T) {
			f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
			if _, err := f.transaction.Apply(t.Context(), f.provider.store); err != nil {
				t.Fatal(err)
			}
			transaction := f.reload(t)
			failure := errors.New("rollback boundary failed")
			if operation == "restore" {
				f.provider.onMutation = func(current string, after bool) error {
					if current == "restore" && after {
						return failure
					}
					return nil
				}
			}
			if operation == "configuration" {
				realRestore := transaction.ops.restoreConfig
				transaction.ops.restoreConfig = func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
					if _, err := realRestore(ctx, recovery); err != nil {
						return false, err
					}
					return false, failure
				}
			}
			if operation == "journal" {
				realSave, calls := transaction.ops.saveJournal, 0
				transaction.ops.saveJournal = func(ctx context.Context, file *File, data []byte, validate func([]byte) error) error {
					calls++
					if calls == 3 {
						return failure
					}
					return realSave(ctx, file, data, validate)
				}
			}
			if _, err := transaction.Rollback(t.Context(), f.provider.store); !errors.Is(err, failure) {
				t.Fatal("rollback fault did not reach named boundary")
			}
			f.provider.onMutation = nil
			if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil {
				t.Fatal(err)
			}
			f.assertRestored(t)
		})
	}
}

func TestLegacyTransactionRecoversEveryRollbackJournalPublicationFailure(t *testing.T) {
	t.Parallel()
	for boundary := 1; boundary <= 6; boundary++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("publication=%d/after=%v", boundary, after), func(t *testing.T) {
				f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
				if _, err := f.transaction.Apply(t.Context(), f.provider.store); err != nil {
					t.Fatal(err)
				}
				transaction := f.reload(t)
				originalSave, calls := transaction.ops.saveJournal, 0
				failure := errors.New("rollback journal publication failure")
				transaction.ops.saveJournal = func(ctx context.Context, file *File, data []byte, validate func([]byte) error) error {
					calls++
					if calls == boundary && !after {
						return failure
					}
					if err := originalSave(ctx, file, data, validate); err != nil {
						return err
					}
					if calls == boundary {
						return failure
					}
					return nil
				}
				if receipt, err := transaction.Rollback(t.Context(), f.provider.store); !errors.Is(err, failure) || calls != boundary || receipt.JournalDurable {
					t.Fatal("rollback publication fault did not preserve uncertainty")
				}
				fresh := f.reload(t)
				if _, err := fresh.Rollback(t.Context(), f.provider.store); err != nil {
					t.Fatal("fresh rollback could not finalize acknowledged partial effects:", err)
				}
				f.assertRestored(t)
			})
		}
	}
}

func TestLegacyTransactionAbsentSourceAcceptsAlreadyMatchingConfiguration(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "candidate")
	delete(f.provider.values, f.source.location)
	receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
	if err != nil || receipt.Status != "applied" || len(f.provider.mutations) != 0 ||
		len(receipt.Effects) != 1 || receipt.Effects[0].Attempted == nil || *receipt.Effects[0].Attempted {
		t.Fatal("already matching config was rejected or rewritten with absent source")
	}
}

func TestLegacyTransactionCreationFailureRetainsArtifactWithoutDependentWrites(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"artifact-sync", "journal-link", "journal-sync", "journal-collision"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "source.toml")
			writeTransactionFile(t, sourcePath, []byte(transactionFixture))
			source, err := OpenFile(t.Context(), sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PrepareLegacyTransaction(t.Context(), source, source, "", "")
			if err != nil {
				t.Fatal(err)
			}
			recoveryDir := filepath.Join(dir, "recovery")
			failure := errors.New("creation boundary failed")
			links, syncs := 0, 0
			ops := fileCreationOps{
				resolve: resolveCreationPath,
				link: func(from, to string) error {
					links++
					if links == 2 && boundary == "journal-link" {
						return failure
					}
					if links == 2 && boundary == "journal-collision" {
						writeTransactionFile(t, to, []byte("external journal"))
					}
					return os.Link(from, to)
				},
				syncDirectory: func(path string) error {
					syncs++
					if syncs == 1 && boundary == "artifact-sync" || syncs == 2 && boundary == "journal-sync" {
						return failure
					}
					return syncCreationDirectory(path)
				},
			}
			transaction, published, err := plan.create(t.Context(), recoveryDir, ops)
			if transaction != nil || !published || err == nil {
				t.Fatal("unacknowledged record publication returned a usable transaction")
			}
			if _, err := LoadLegacyRecovery(t.Context(), filepath.Join(recoveryDir, "configuration.json")); err != nil {
				t.Fatal("creation failure discarded immutable recovery artifact")
			}
			data, err := os.ReadFile(sourcePath)
			if err != nil || !bytes.Equal(data, []byte(transactionFixture)) {
				t.Fatal("recovery publication failure changed primary configuration")
			}
			journalPath := filepath.Join(recoveryDir, "migration.json")
			if boundary == "journal-collision" {
				data, err := os.ReadFile(journalPath)
				if err != nil || string(data) != "external journal" {
					t.Fatal("journal creation clobbered an external publication")
				}
			}
			if boundary == "artifact-sync" || boundary == "journal-link" {
				if _, err := os.Stat(journalPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed prerequisite permitted dependent publication")
				}
			}
		})
	}
}

func TestLegacyTransactionStrictJournalAndImmutableArtifactBinding(t *testing.T) {
	t.Parallel()
	for _, corruption := range []string{"version", "hash", "profile-location", "backup-id", "sequence", "status", "unknown", "duplicate", "trailing"} {
		t.Run(corruption, func(t *testing.T) {
			f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
			if _, err := f.transaction.Apply(t.Context(), f.provider.store); err != nil {
				t.Fatal(err)
			}
			record := f.transaction.record
			switch corruption {
			case "version":
				record.Version++
			case "hash":
				record.ConfigurationSHA256 = strings.Repeat("0", 64)
			case "profile-location":
				record.SourceCredential.Account = "other/profile"
			case "backup-id":
				record.CredentialBackup = "default"
			case "sequence":
				record.Effects[1].Sequence = 10
			case "status":
				record.Status = "rolled_back"
			}
			data, err := jsonv2.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "unknown":
				data = append([]byte(`{"unexpected":1,`), data[1:]...)
			case "duplicate":
				data = append([]byte(`{"version":1,`), data[1:]...)
			case "trailing":
				data = append(data, []byte(" {}")...)
			}
			writeTransactionFile(t, f.transaction.file.path, data)
			if _, err := LoadLegacyTransaction(t.Context(), f.transaction.file.path); err == nil {
				t.Fatal("invalid journal accepted")
			}
		})
	}
}
