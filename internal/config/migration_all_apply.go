package config

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type profileCredentialCapture struct {
	sourceStore, targetStore core.CredentialRecoveryStore
	source, before, expected core.CredentialSnapshot
}

func (t *ProfileTransaction) Apply(ctx context.Context, stores MigrationStores) (MigrationReceipt, error) {
	err := t.apply(ctx, stores)
	if err != nil {
		return t.Receipt(), &legacyTransactionFailure{cause: err}
	}
	return t.Receipt(), nil
}

func (t *ProfileTransaction) checkPreimages(ctx context.Context) error {
	if err := t.configuration.check(ctx); err != nil {
		return err
	}
	if _, err := t.configuration.record.Source.open(ctx); err != nil {
		return err
	}
	_, err := t.configuration.record.Target.open(ctx)
	return err
}

func (t *ProfileTransaction) apply(ctx context.Context, stores MigrationStores) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	if t.record.Status != "prepared" || len(t.record.Effects) != 0 {
		return ErrLegacyTransactionStarted
	}
	if err := t.checkPreimages(ctx); err != nil {
		return err
	}
	cr := t.configuration.record
	migration, err := prepareProfileMigration(ctx, cr.Source.Contents, cr.Source.Path, cr.SourceProfile, cr.MappingVersion, cr.ProfileIDs)
	if err != nil {
		return err
	}
	resources := slices.Clone(t.record.Resources)
	captures := make([]profileCredentialCapture, len(resources))
	locations := map[core.CredentialLocation]bool{}
	// Resolve the entire write set before even the first provider backup. Two
	// SDK names may select one physical item despite differing authored strings.
	for i := range resources {
		resource := &resources[i]
		if resource.Source.Backend == core.CredentialBackendEnvironment {
			resource.State = "external"
			continue
		}
		if stores == nil {
			return core.ErrCredentialRecoveryInvalid
		}
		capture, err := prepareProfileCredential(ctx, stores, *resource, migration.selections[resource.Profile])
		if err != nil {
			return err
		}
		captures[i] = capture
		_, present := capture.source.Secret()
		if !present {
			resource.State = "absent"
			continue
		}
		if locations[migrationPhysicalLocation(capture.before.Location())] {
			return core.ErrCredentialRecoveryInvalid
		}
		locations[migrationPhysicalLocation(capture.before.Location())] = true
		resource.State = "present"
		resource.Source, resource.Target = capture.source.Location(), capture.before.Location()
		resource.Before, resource.Expected = capture.before.Commitment(), capture.expected.Commitment()
	}
	t.record.Resources = resources
	t.record.Status = "applying"
	if err := t.persist(ctx); err != nil {
		return err
	}
	for i, resource := range t.record.Resources {
		if resource.State != "present" {
			continue
		}
		capture := captures[i]
		if err := t.runEffect(ctx, resource.Profile, migrationBackup, func() (*bool, error) {
			if err := t.checkPreimages(ctx); err != nil {
				return migrationAttempt(false), err
			}
			if err := checkMigrationCredential(ctx, capture.sourceStore, capture.source); err != nil {
				return migrationAttempt(false), err
			}
			if err := capture.targetStore.Backup(ctx, resource.Backup, capture.before, capture.expected); err != nil {
				return providerMigrationAttempt(err), err
			}
			return migrationAttempt(true), nil
		}); err != nil {
			return err
		}
		if err := t.runEffect(ctx, resource.Profile, migrationApply, func() (*bool, error) {
			if err := t.checkPreimages(ctx); err != nil {
				return migrationAttempt(false), err
			}
			if err := checkMigrationCredential(ctx, capture.sourceStore, capture.source); err != nil {
				return migrationAttempt(false), err
			}
			before, expected, err := capture.targetStore.ReadBackup(ctx, resource.Backup)
			if err != nil {
				return migrationAttempt(false), err
			}
			if err := t.validateBackup(ctx, resource, before, expected); err != nil {
				return migrationAttempt(false), err
			}
			attempted, err := capture.targetStore.Apply(ctx, resource.Target.Account, resource.Backup)
			return migrationAttempt(attempted), err
		}); err != nil {
			return err
		}
	}
	if err := t.runEffect(ctx, "", migrationConfigApply, func() (*bool, error) {
		for i, resource := range t.record.Resources {
			capture := captures[i]
			switch resource.State {
			case "present":
				if err := checkMigrationCredential(ctx, capture.targetStore, capture.expected); err != nil {
					return migrationAttempt(false), err
				}
			case "absent":
				if err := checkMigrationCredential(ctx, capture.sourceStore, capture.source); err != nil {
					return migrationAttempt(false), err
				}
			case "external":
			default:
				return migrationAttempt(false), core.ErrCredentialRecoveryInvalid
			}
		}
		changed, err := t.ops.applyConfig(ctx, t.configuration)
		if err != nil {
			return nil, err
		}
		return migrationAttempt(changed), nil
	}); err != nil {
		return err
	}
	if err := t.configuration.checkApplied(ctx); err != nil {
		return err
	}
	t.record.Status = "applied"
	return t.persist(ctx)
}

func prepareProfileCredential(ctx context.Context, stores MigrationStores, resource profileMigrationResource, selection LegacySelection) (profileCredentialCapture, error) {
	var capture profileCredentialCapture
	sourceStore, err := stores(ctx, resource.Source)
	if err != nil {
		return capture, err
	}
	source, err := captureMigrationCredential(ctx, sourceStore, resource.Source.Account)
	if err != nil {
		return capture, err
	}
	if resource.Source.Backend == core.CredentialBackendKeyring && source.Location() != resource.Source || resource.Source.Backend == core.CredentialBackendOnePassword && !migrationSDKLocation(resource.Source, source.Location()) {
		return capture, core.ErrCredentialRecoveryInvalid
	}
	capture.sourceStore, capture.source = sourceStore, source
	raw, present := source.Secret()
	if !present {
		return capture, nil
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return capture, core.ErrCredentialRecoveryUnsupported
	}
	record := credentials.Record{Site: selection.Site, Email: selection.Email, CloudID: selection.CloudID, Token: raw}
	if !record.Complete() {
		return capture, core.ErrCredentialRecoveryInvalid
	}
	sink := &migrationCredentialSink{}
	if err := credentials.Save(ctx, sink, record); err != nil {
		return capture, err
	}
	capture.targetStore, capture.before = sourceStore, source
	if resource.Target.Backend == core.CredentialBackendKeyring {
		capture.targetStore, err = stores(ctx, resource.Target)
		if err != nil {
			return capture, err
		}
		capture.before, err = captureMigrationCredential(ctx, capture.targetStore, resource.Target.Account)
		if err != nil {
			return capture, err
		}
		if capture.before.Location() != resource.Target {
			return capture, core.ErrCredentialRecoveryInvalid
		}
		// New UUID slots must be empty. An occupied slot is a conflict even if its
		// token happens to match; conversion never adopts another profile's slot.
		if _, present := capture.before.Secret(); present {
			return capture, core.ErrCredentialChanged
		}
	}
	capture.expected, err = capture.targetStore.Replacement(capture.before, sink.value)
	if err != nil {
		return capture, err
	}
	if capture.expected == nil || !capture.expected.Matches(capture.expected) || capture.expected.Location() != capture.before.Location() {
		return capture, core.ErrCredentialRecoveryInvalid
	}
	return capture, nil
}

func (t *ProfileTransaction) validateBackup(ctx context.Context, resource profileMigrationResource, before, expected core.CredentialSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if before == nil || expected == nil || !before.Matches(before) || !expected.Matches(expected) || before.Location() != resource.Target || expected.Location() != resource.Target || before.Commitment() != resource.Before || expected.Commitment() != resource.Expected {
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
	cr := t.configuration.record
	migration, err := prepareLegacyMigration(ctx, cr.Source.Contents, cr.Source.Path, resource.Profile, cr.MappingVersion)
	if err != nil {
		return err
	}
	selection := migration.Selection()
	if record.Site != credentials.NormalizeSite(selection.Site) || record.CloudID != selection.CloudID || !strings.EqualFold(strings.TrimSpace(record.Email), selection.Email) {
		return core.ErrCredentialRecoveryInvalid
	}
	return ctx.Err()
}
