package config

import (
	"context"
	"errors"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// Apply converts the selected raw credential through the credential-record owner,
// backs up the destination inside that provider and establishes its expected
// postimage before publishing configuration. Every mutation has durable intent.
// On failure, retain records for explicit rollback with a fresh caller context.
func (t *LegacyTransaction) Apply(ctx context.Context, stores MigrationStores) (MigrationReceipt, error) {
	err := t.apply(ctx, stores)
	if err != nil {
		return t.Receipt(), &legacyTransactionFailure{cause: err}
	}
	return t.Receipt(), nil
}

func (t *LegacyTransaction) apply(ctx context.Context, stores MigrationStores) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	if t.record.Status != "prepared" || t.record.CredentialState != "unexamined" || len(t.record.Effects) != 0 {
		return ErrLegacyTransactionStarted
	}
	if err := t.checkApplyPreimages(ctx); err != nil {
		return err
	}
	if t.record.SourceCredential.Backend == core.CredentialBackendEnvironment {
		// The immutable candidate names the historical variable, not its value.
		// Only configuration is owned; no provider or process environment is read.
		t.record.CredentialState, t.record.Status = "external", "applying"
		if err := t.persist(ctx); err != nil {
			return err
		}
		return t.applyConfiguration(ctx, nil, nil)
	}
	if stores == nil {
		return core.ErrCredentialRecoveryInvalid
	}
	sourceStore, err := stores(ctx, t.record.SourceCredential)
	if err != nil {
		return err
	}
	source, err := captureMigrationCredential(ctx, sourceStore, t.record.SourceCredential.Account)
	if err != nil {
		return err
	}
	location := source.Location()
	selected := t.record.SourceCredential
	if selected.Backend == core.CredentialBackendKeyring && location != selected ||
		selected.Backend == core.CredentialBackendOnePassword && (location.Backend != selected.Backend ||
			location.Account != selected.Account || location.SDKAccount != selected.SDKAccount || location.Service != "" || location.EnvironmentKey != "" || location.Vault == "" || location.Item == "") {
		return core.ErrCredentialRecoveryInvalid
	}
	secret, present := source.Secret()
	if !present {
		t.record.CredentialState, t.record.Status = "absent", "applying"
		if err := t.persist(ctx); err != nil {
			return err
		}
		// No destination credential is fabricated, overwritten or backed up.
		return t.applyConfiguration(ctx, sourceStore, source)
	}
	migration, err := prepareLegacyMigration(ctx, t.configuration.record.Source.Contents, t.configuration.record.Source.Path, t.configuration.record.Profile, t.configuration.record.Version)
	if err != nil {
		return err
	}
	selection := migration.Selection()
	// A JSON-looking value can be a partially migrated record. Never wrap it
	// again as a token or guess that an unrelated record belongs to this site.
	if strings.HasPrefix(strings.TrimSpace(secret), "{") {
		return core.ErrCredentialRecoveryUnsupported
	}
	record := credentials.Record{Site: selection.Site, Email: selection.Email, Token: secret, CloudID: selection.CloudID}
	if !record.Complete() {
		return core.ErrCredentialRecoveryInvalid
	}
	sink := &migrationCredentialSink{}
	if err := credentials.Save(ctx, sink, record); err != nil {
		return err
	}
	targetStore := sourceStore
	before := source
	if selection.Backend == core.CredentialBackendKeyring {
		targetStore, err = stores(ctx, t.record.TargetCredential)
		if err != nil {
			return err
		}
		before, err = captureMigrationCredential(ctx, targetStore, t.record.TargetCredential.Account)
		if err != nil {
			return err
		}
	}
	expected, err := targetStore.Replacement(before, sink.value)
	if err != nil {
		return err
	}
	if expected == nil || !expected.Matches(expected) || expected.Location() != before.Location() {
		return core.ErrCredentialRecoveryInvalid
	}
	t.record.SourceCredential, t.record.TargetCredential = source.Location(), before.Location()
	t.record.BeforeCommitment, t.record.ExpectedCommitment = before.Commitment(), expected.Commitment()
	t.record.CredentialState, t.record.Status = "present", "applying"
	if err := t.persist(ctx); err != nil {
		return err
	}
	if err := t.runEffect(ctx, migrationBackup, func() (*bool, error) {
		if err := t.checkApplyPreimages(ctx); err != nil {
			return migrationAttempt(false), err
		}
		if err := checkMigrationCredential(ctx, sourceStore, source); err != nil {
			return migrationAttempt(false), err
		}
		if err := targetStore.Backup(ctx, t.record.CredentialBackup, before, expected); err != nil {
			return providerMigrationAttempt(err), err
		}
		return migrationAttempt(true), nil
	}); err != nil {
		return err
	}
	old, next, err := targetStore.ReadBackup(ctx, t.record.CredentialBackup)
	if err != nil {
		return err
	}
	if !before.Matches(old) || !expected.Matches(next) {
		return core.ErrCredentialRecoveryInvalid
	}
	if err := t.validateBackup(ctx, old, next); err != nil {
		return err
	}
	if err := t.runEffect(ctx, migrationApply, func() (*bool, error) {
		if err := t.checkApplyPreimages(ctx); err != nil {
			return migrationAttempt(false), err
		}
		if err := checkMigrationCredential(ctx, sourceStore, source); err != nil {
			return migrationAttempt(false), err
		}
		old, next, err := targetStore.ReadBackup(ctx, t.record.CredentialBackup)
		if err != nil {
			return migrationAttempt(false), err
		}
		if err := t.validateBackup(ctx, old, next); err != nil {
			return migrationAttempt(false), err
		}
		attempted, err := targetStore.Apply(ctx, t.record.TargetCredential.Account, t.record.CredentialBackup)
		return migrationAttempt(attempted), err
	}); err != nil {
		return err
	}
	return t.applyConfiguration(ctx, targetStore, expected)
}

func (t *LegacyTransaction) checkApplyPreimages(ctx context.Context) error {
	if err := t.configuration.check(ctx); err != nil {
		return err
	}
	if _, err := t.configuration.record.Source.open(ctx); err != nil {
		return err
	}
	_, err := t.configuration.record.Target.open(ctx)
	return err
}

func (t *LegacyTransaction) applyConfiguration(ctx context.Context, store core.CredentialRecoveryStore, expected core.CredentialSnapshot) error {
	if err := t.runEffect(ctx, migrationConfigApply, func() (*bool, error) {
		if t.record.SourceCredential.Backend == core.CredentialBackendEnvironment && t.record.CredentialState == "external" {
			if store != nil || expected != nil {
				return migrationAttempt(false), core.ErrCredentialRecoveryInvalid
			}
		} else {
			if err := checkMigrationCredential(ctx, store, expected); err != nil {
				return migrationAttempt(false), err
			}
		}
		changed, err := t.ops.applyConfig(ctx, t.configuration)
		if err != nil {
			// The shared writer may fail after rename with changed=false.
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

func captureMigrationCredential(ctx context.Context, store core.CredentialRecoveryStore, account string) (core.CredentialSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, core.ErrCredentialRecoveryInvalid
	}
	snapshot, err := store.Capture(ctx, account)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil || !snapshot.Matches(snapshot) {
		return nil, core.ErrCredentialRecoveryInvalid
	}
	return snapshot, nil
}

func checkMigrationCredential(ctx context.Context, store core.CredentialRecoveryStore, expected core.CredentialSnapshot) error {
	if expected == nil {
		return core.ErrCredentialRecoveryInvalid
	}
	current, err := captureMigrationCredential(ctx, store, expected.Location().Account)
	if err != nil {
		return err
	}
	if !expected.Matches(current) {
		return core.ErrCredentialChanged
	}
	return nil
}

func migrationAttempt(value bool) *bool { return &value }

func providerMigrationAttempt(err error) *bool {
	var provider *core.CredentialStoreError
	if errors.As(err, &provider) {
		return migrationAttempt(provider.Attempted)
	}
	return nil
}

func (t *LegacyTransaction) runEffect(ctx context.Context, operation string, apply func() (*bool, error)) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	t.record.Effects = append(t.record.Effects, MigrationEffect{
		Sequence: len(t.record.Effects) + 1, Operation: operation, Status: "pending",
	})
	if err := t.persist(ctx); err != nil {
		return err
	}
	// Recheck both records after intent publication and before provider/file entry.
	if err := t.check(ctx); err != nil {
		return err
	}
	attempted, operationErr := apply()
	effect := &t.record.Effects[len(t.record.Effects)-1]
	effect.Attempted, effect.ErrorCode = attempted, migrationErrorCode(operationErr)
	switch {
	case operationErr == nil:
		effect.Status, effect.ErrorCode = "acknowledged", ""
	case attempted == nil || *attempted:
		effect.Status = "uncertain"
	default:
		effect.Status = "failed"
	}
	// A canceled context leaves on-disk pending intent. Never detach context to
	// pretend an outcome was durably recorded or to perform automatic cleanup.
	return errors.Join(operationErr, t.persist(ctx))
}

// The existing credentials.Save owner supplies encoding without another codec
// or persistent store. This private sink must never be rendered.
type migrationCredentialSink struct{ value string }

func (*migrationCredentialSink) String() string   { return "credential conversion" }
func (*migrationCredentialSink) GoString() string { return "credential conversion" }
func (s *migrationCredentialSink) Get(ctx context.Context, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.value == "" {
		return "", core.ErrNoCredential
	}
	return s.value, nil
}

func (s *migrationCredentialSink) Set(ctx context.Context, _, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.value = value
	return nil
}

func (*migrationCredentialSink) Delete(context.Context, string) error {
	return core.ErrCredentialRecoveryUnsupported
}
