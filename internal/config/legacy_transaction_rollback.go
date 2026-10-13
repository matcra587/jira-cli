package config

import (
	"context"
	"errors"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// PreviewRollback checks the journal, artifact and configuration snapshots only.
// It never opens a provider and therefore cannot verify credential postimages.
func (t *LegacyTransaction) PreviewRollback(ctx context.Context) (core.MigrationRollbackPreview, error) {
	var zero core.MigrationRollbackPreview
	if err := t.check(ctx); err != nil {
		return zero, err
	}
	if err := t.checkRollbackConfiguration(ctx); err != nil {
		return zero, err
	}
	_, restored, err := t.configuration.checkRestore(ctx)
	if err != nil {
		return zero, err
	}
	return core.MigrationRollbackPreview{
		Receipt: t.Receipt(), WouldRestoreConfiguration: !restored,
		CredentialInspectionRequired: t.hasEffect(migrationApply),
	}, nil
}

// Rollback preflights configuration and any possibly changed credential before
// its first write. It restores credentials before exact config bytes or absence.
// Backups and journals remain retained; retries require the expected states.
// This is recoverable orchestration, not an atomic cross-provider transaction.
func (t *LegacyTransaction) Rollback(ctx context.Context, stores MigrationStores) (MigrationReceipt, error) {
	err := t.rollback(ctx, stores)
	if err != nil {
		return t.Receipt(), &legacyTransactionFailure{cause: err}
	}
	return t.Receipt(), nil
}

func (t *LegacyTransaction) rollback(ctx context.Context, stores MigrationStores) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	if err := t.checkRollbackConfiguration(ctx); err != nil {
		return err
	}
	var store core.CredentialRecoveryStore
	var before, expected core.CredentialSnapshot
	credentialRestored := true
	if t.hasEffect(migrationApply) {
		if stores == nil {
			return core.ErrCredentialRecoveryInvalid
		}
		var err error
		store, err = stores(ctx, t.record.TargetCredential)
		if err != nil {
			return err
		}
		if store == nil {
			return core.ErrCredentialRecoveryInvalid
		}
		before, expected, err = store.ReadBackup(ctx, t.record.CredentialBackup)
		if err != nil {
			return err
		}
		if err := t.validateBackup(ctx, before, expected); err != nil {
			return err
		}
		current, err := captureMigrationCredential(ctx, store, t.record.TargetCredential.Account)
		if err != nil {
			return err
		}
		if !before.Matches(current) && !expected.Matches(current) {
			return core.ErrCredentialChanged
		}
		credentialRestored = before.Matches(current)
		if t.record.Status == "rolled_back" && !before.Matches(current) {
			return core.ErrCredentialChanged
		}
	}
	if t.record.Status == "rolled_back" {
		return nil
	}
	// The final status publication may fail after both restoration outcomes
	// were persisted. Independently verified preimages permit finalization
	// without appending a restore stage after acknowledged config restoration.
	if t.acknowledgedEffect(migrationConfigRestore) {
		_, configRestored, err := t.configuration.checkRestore(ctx)
		if err != nil {
			return err
		}
		if !credentialRestored || !configRestored {
			return core.ErrCredentialChanged
		}
		t.record.Status = "rolled_back"
		return t.persist(ctx)
	}
	t.record.Status = "rolling_back"
	if err := t.persist(ctx); err != nil {
		return err
	}
	if store != nil {
		if err := t.runEffect(ctx, migrationRestore, func() (*bool, error) {
			if err := t.checkRollbackConfiguration(ctx); err != nil {
				return migrationAttempt(false), err
			}
			old, next, err := store.ReadBackup(ctx, t.record.CredentialBackup)
			if err != nil {
				return migrationAttempt(false), err
			}
			if !before.Matches(old) || !expected.Matches(next) {
				return migrationAttempt(false), core.ErrCredentialChanged
			}
			attempted, err := store.Restore(ctx, t.record.TargetCredential.Account, t.record.CredentialBackup)
			return migrationAttempt(attempted), err
		}); err != nil {
			return err
		}
	}
	if err := t.runEffect(ctx, migrationConfigRestore, func() (*bool, error) {
		if store != nil {
			if err := checkMigrationCredential(ctx, store, before); err != nil {
				return migrationAttempt(false), err
			}
		}
		if err := t.checkRollbackConfiguration(ctx); err != nil {
			return migrationAttempt(false), err
		}
		changed, err := t.ops.restoreConfig(ctx, t.configuration)
		if err != nil {
			return nil, err
		}
		return migrationAttempt(changed), nil
	}); err != nil {
		return err
	}
	_, restored, err := t.configuration.checkRestore(ctx)
	if err != nil {
		return err
	}
	if !restored {
		return ErrFileChanged
	}
	t.record.Status = "rolled_back"
	return t.persist(ctx)
}

func (t *LegacyTransaction) checkRollbackConfiguration(ctx context.Context) error {
	_, restored, err := t.configuration.checkRestore(ctx)
	if err != nil {
		return err
	}
	// Without config intent, this transaction could not have changed that file.
	if !t.hasEffect(migrationConfigApply) && !restored || t.record.Status == "rolled_back" && !restored {
		return ErrFileChanged
	}
	if t.configuration.record.Source.Path != t.configuration.record.Target.Path {
		_, err := t.configuration.record.Source.open(ctx)
		return err
	}
	return nil
}

func (t *LegacyTransaction) hasEffect(operation string) bool {
	for _, effect := range t.record.Effects {
		if effect.Operation == operation {
			return true
		}
	}
	return false
}

func (t *LegacyTransaction) acknowledgedEffect(operation string) bool {
	for _, effect := range t.record.Effects {
		if effect.Operation == operation && effect.Status == "acknowledged" {
			return true
		}
	}
	return false
}

func (t *LegacyTransaction) validateBackup(ctx context.Context, before, expected core.CredentialSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if before == nil || expected == nil || !before.Matches(before) || !expected.Matches(expected) ||
		before.Location() != t.record.TargetCredential || expected.Location() != t.record.TargetCredential ||
		before.Commitment() != t.record.BeforeCommitment || expected.Commitment() != t.record.ExpectedCommitment {
		return core.ErrCredentialRecoveryInvalid
	}
	raw, present := expected.Secret()
	if !present {
		return core.ErrCredentialRecoveryInvalid
	}
	record, err := credentials.Load(ctx, &migrationCredentialSink{value: raw})
	if err != nil {
		return errors.Join(core.ErrCredentialRecoveryInvalid, err)
	}
	if !record.Complete() {
		return core.ErrCredentialRecoveryInvalid
	}
	migration, err := prepareLegacyMigration(ctx, t.configuration.record.Source.Contents, t.configuration.record.Source.Path, t.configuration.record.Profile, t.configuration.record.Version)
	if err != nil {
		return err
	}
	selection := migration.Selection()
	if record.Site != credentials.NormalizeSite(selection.Site) || record.CloudID != selection.CloudID || !strings.EqualFold(strings.TrimSpace(record.Email), selection.Email) {
		return core.ErrCredentialRecoveryInvalid
	}
	return ctx.Err()
}
