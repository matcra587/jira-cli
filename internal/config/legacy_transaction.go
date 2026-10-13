package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// ErrLegacyTransaction identifies invalid or incomplete migration orchestration.
// Public errors never render configuration or credential material.
var ErrLegacyTransaction = errors.New("legacy migration transaction could not complete")

// ErrLegacyTransactionStarted requires explicit rollback after an apply attempt.
var ErrLegacyTransactionStarted = errors.New("legacy migration already started; use its recovery record to roll back")

type legacyTransactionFailure struct{ cause error }

func (*legacyTransactionFailure) Error() string { return ErrLegacyTransaction.Error() }
func (e *legacyTransactionFailure) Unwrap() error {
	return errors.Join(ErrLegacyTransaction, e.cause)
}

// Public recovery models are shared with typed envelopes through the domain
// owner, without introducing a configuration dependency on command/output code.
type (
	MigrationEffect  = core.MigrationEffect
	MigrationReceipt = core.MigrationReceipt
)

// MigrationStores constructs the named provider lazily with the command context.
// The composition root owns adapter selection. Preparing, creating and loading
// records never calls this factory or opens credentials.
type MigrationStores func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error)

// LegacyTransactionPlan is a credential-free, immutable selected-profile plan.
type LegacyTransactionPlan struct {
	configuration *LegacyRecoveryPlan
	selection     LegacySelection
	service       string
}

// CredentialLocations returns planned public source/destination names; actual
// SDK IDs become available only during real provider capture.
func (p *LegacyTransactionPlan) CredentialLocations() (core.CredentialLocation, core.CredentialLocation) {
	if p == nil {
		return core.CredentialLocation{}, core.CredentialLocation{}
	}
	return migrationLocations(p.selection, p.service)
}

// PrepareLegacyTransaction prepares exact configuration recovery and historical
// credential locations. service selects the old explicit keyring namespace;
// blank means jira-cli. Environment references require only configuration
// publication; their externally managed values are never inspected or copied.
func PrepareLegacyTransaction(ctx context.Context, source, target *File, profile, service string) (*LegacyTransactionPlan, error) {
	plan, err := PrepareLegacyRecovery(ctx, source, target, profile)
	if err != nil {
		return nil, err
	}
	migration, err := prepareLegacyMigration(ctx, plan.record.Source.Contents, plan.record.Source.Path, plan.record.Profile, plan.record.Version)
	if err != nil {
		return nil, err
	}
	selection := migration.Selection()
	service = strings.TrimSpace(service)
	if service == "" {
		service = "jira-cli"
	}
	return &LegacyTransactionPlan{configuration: plan, selection: selection, service: service}, nil
}

// Create publishes immutable configuration preimages and then a mutable intent
// journal, both at mode 0600 with no-clobber creation. No provider is opened.
// published=true retains a published configuration artifact even when journal
// publication fails. Nothing applies until both records are acknowledged.
func (p *LegacyTransactionPlan) Create(ctx context.Context, directory string) (*LegacyTransaction, bool, error) {
	return p.create(ctx, directory, fileCreationOps{resolve: resolveCreationPath, link: os.Link, syncDirectory: syncCreationDirectory})
}

func (p *LegacyTransactionPlan) create(ctx context.Context, directory string, ops fileCreationOps) (*LegacyTransaction, bool, error) {
	paths, err := p.RecoveryPaths(ctx, directory)
	if err != nil {
		return nil, false, err
	}
	journal, err := OpenNewFile(ctx, paths.Journal)
	if err != nil {
		return nil, false, &legacyTransactionFailure{cause: err}
	}
	recovery, published, err := p.configuration.create(ctx, paths.Configuration, ops)
	if err != nil {
		return nil, published, &legacyTransactionFailure{cause: err}
	}
	record := legacyTransactionRecord{
		Version: legacyTransactionVersion, Configuration: recovery.file.requested, ConfigurationSHA256: recoveryDigest(recovery.file.original),
		LegacyService: p.service, Status: "prepared", CredentialState: "unexamined",
	}
	if p.selection.Backend != core.CredentialBackendEnvironment {
		record.CredentialBackup = core.NewCredentialBackupID()
	}
	record.SourceCredential, record.TargetCredential = migrationLocations(p.selection, p.service)
	transaction := newLegacyTransaction(record, recovery, journal)
	if err := transaction.createJournal(ctx, ops); err != nil {
		return nil, true, &legacyTransactionFailure{cause: err}
	}
	return transaction, true, nil
}

// RecoveryPaths validates both absent publication targets and original config
// snapshots without opening providers, creating directories or publishing data.
func (p *LegacyTransactionPlan) RecoveryPaths(ctx context.Context, directory string) (core.MigrationRecoveryPaths, error) {
	var zero core.MigrationRecoveryPaths
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if p == nil || p.configuration == nil || strings.TrimSpace(directory) == "" {
		return zero, ErrLegacyTransaction
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return zero, &legacyTransactionFailure{cause: err}
	}
	paths := core.MigrationRecoveryPaths{Directory: directory, Configuration: filepath.Join(directory, "configuration.json"), Journal: filepath.Join(directory, "migration.json")}
	for _, path := range []string{paths.Configuration, paths.Journal} {
		file, err := OpenNewFile(ctx, path)
		if err != nil {
			return zero, &legacyTransactionFailure{cause: err}
		}
		for _, snapshot := range []legacyRecoverySnapshot{p.configuration.record.Source, p.configuration.record.Target} {
			if file.path == snapshot.Path {
				return zero, ErrLegacyTransaction
			}
		}
	}
	for _, snapshot := range []legacyRecoverySnapshot{p.configuration.record.Source, p.configuration.record.Target} {
		if _, err := snapshot.open(ctx); err != nil {
			return zero, err
		}
	}
	return paths, nil
}

func migrationLocations(selection LegacySelection, service string) (core.CredentialLocation, core.CredentialLocation) {
	if selection.Backend == core.CredentialBackendEnvironment {
		location := core.CredentialLocation{Backend: selection.Backend, Account: "default", EnvironmentKey: selection.EnvTokenKey()}
		return location, location
	}
	if selection.Backend == core.CredentialBackendOnePassword {
		location := core.CredentialLocation{
			Backend: selection.Backend, Account: "default", SDKAccount: selection.Account,
			Vault: selection.Vault, Item: selection.Item,
		}
		return location, location
	}
	return core.CredentialLocation{Backend: core.CredentialBackendKeyring, Service: service, Account: selection.KeyringName()},
		core.CredentialLocation{Backend: core.CredentialBackendKeyring, Service: "jira-cli", Account: "default"}
}
