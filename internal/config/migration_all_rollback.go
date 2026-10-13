package config

import (
	"context"

	"github.com/matcra587/jira-cli/internal/core"
)

func (t *ProfileTransaction) PreviewRollback(ctx context.Context) (core.MigrationRollbackPreview, error) {
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
	required := false
	for _, resource := range t.record.Resources {
		required = required || t.hasEffect(resource.Profile, migrationApply)
	}
	return core.MigrationRollbackPreview{Receipt: t.Receipt(), WouldRestoreConfiguration: !restored, CredentialInspectionRequired: required}, nil
}

func (t *ProfileTransaction) Rollback(ctx context.Context, stores MigrationStores) (MigrationReceipt, error) {
	err := t.rollback(ctx, stores)
	if err != nil {
		return t.Receipt(), &legacyTransactionFailure{cause: err}
	}
	return t.Receipt(), nil
}

func (t *ProfileTransaction) checkRollbackConfiguration(ctx context.Context) error {
	_, restored, err := t.configuration.checkRestore(ctx)
	if err != nil {
		return err
	}
	if !t.hasEffect("", migrationConfigApply) && !restored || t.record.Status == "rolled_back" && !restored {
		return ErrFileChanged
	}
	if t.configuration.record.Source.Path != t.configuration.record.Target.Path {
		_, err := t.configuration.record.Source.open(ctx)
		return err
	}
	return nil
}

func (t *ProfileTransaction) rollback(ctx context.Context, stores MigrationStores) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	if err := t.checkRollbackConfiguration(ctx); err != nil {
		return err
	}
	captures := make([]profileCredentialCapture, len(t.record.Resources))
	allRestored := true
	// Verify every affected resource before the first restoration. A refusal on
	// the last profile must not restore the earlier ones behind the user's back.
	for i, resource := range t.record.Resources {
		if !t.hasEffect(resource.Profile, migrationApply) {
			continue
		}
		if stores == nil {
			return core.ErrCredentialRecoveryInvalid
		}
		store, err := stores(ctx, resource.Target)
		if err != nil {
			return err
		}
		if store == nil {
			return core.ErrCredentialRecoveryInvalid
		}
		before, expected, err := store.ReadBackup(ctx, resource.Backup)
		if err != nil {
			return err
		}
		if err := t.validateBackup(ctx, resource, before, expected); err != nil {
			return err
		}
		current, err := captureMigrationCredential(ctx, store, resource.Target.Account)
		if err != nil {
			return err
		}
		if !before.Matches(current) && !expected.Matches(current) {
			return core.ErrCredentialChanged
		}
		if t.record.Status == "rolled_back" && !before.Matches(current) {
			return core.ErrCredentialChanged
		}
		allRestored = allRestored && before.Matches(current)
		captures[i] = profileCredentialCapture{targetStore: store, before: before, expected: expected}
	}
	if t.record.Status == "rolled_back" {
		return nil
	}
	if t.acknowledged("", migrationConfigRestore) {
		_, restored, err := t.configuration.checkRestore(ctx)
		if err != nil {
			return err
		}
		if !allRestored || !restored {
			return core.ErrCredentialChanged
		}
		t.record.Status = "rolled_back"
		return t.persist(ctx)
	}
	t.record.Status = "rolling_back"
	if err := t.persist(ctx); err != nil {
		return err
	}
	for i := len(t.record.Resources) - 1; i >= 0; i-- {
		resource, capture := t.record.Resources[i], captures[i]
		if capture.targetStore == nil {
			continue
		}
		if err := t.runEffect(ctx, resource.Profile, migrationRestore, func() (*bool, error) {
			if err := t.checkRollbackConfiguration(ctx); err != nil {
				return migrationAttempt(false), err
			}
			before, expected, err := capture.targetStore.ReadBackup(ctx, resource.Backup)
			if err != nil {
				return migrationAttempt(false), err
			}
			if err := t.validateBackup(ctx, resource, before, expected); err != nil {
				return migrationAttempt(false), err
			}
			attempted, err := capture.targetStore.Restore(ctx, resource.Target.Account, resource.Backup)
			return migrationAttempt(attempted), err
		}); err != nil {
			return err
		}
	}
	if err := t.runEffect(ctx, "", migrationConfigRestore, func() (*bool, error) {
		for _, capture := range captures {
			if capture.targetStore != nil {
				if err := checkMigrationCredential(ctx, capture.targetStore, capture.before); err != nil {
					return migrationAttempt(false), err
				}
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
