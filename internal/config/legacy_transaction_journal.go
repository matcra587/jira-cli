package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

const (
	legacyTransactionVersion = 2
	migrationBackup          = "credential_backup"
	migrationApply           = "credential_apply"
	migrationConfigApply     = "configuration_apply"
	migrationRestore         = "credential_restore"
	migrationConfigRestore   = "configuration_restore"
)

type legacyTransactionRecord struct {
	Version             int                     `json:"version"`
	Configuration       string                  `json:"configuration"`
	ConfigurationSHA256 string                  `json:"configuration_sha256"`
	LegacyService       string                  `json:"legacy_service"`
	Status              string                  `json:"status"`
	CredentialState     string                  `json:"credential_state"`
	SourceCredential    core.CredentialLocation `json:"source_credential"`
	TargetCredential    core.CredentialLocation `json:"target_credential"`
	CredentialBackup    string                  `json:"credential_backup"`
	BeforeCommitment    string                  `json:"before_commitment,omitempty"`
	ExpectedCommitment  string                  `json:"expected_commitment,omitempty"`
	Effects             []MigrationEffect       `json:"effects"`
}

// LegacyTransaction keeps recovery for explicit rollback after every outcome.
// Apply is single-use; an interrupted invocation must roll back, never replay
// unknown writes. Fresh processes reopen the journal through LoadLegacyTransaction.
type LegacyTransaction struct {
	record        legacyTransactionRecord
	configuration *LegacyRecovery
	file          *File
	durable       bool
	ops           legacyTransactionOps
}

// Narrow per-instance fault boundaries retain the shared filesystem owners.
type legacyTransactionOps struct {
	saveJournal   func(context.Context, *File, []byte, func([]byte) error) error
	applyConfig   func(context.Context, *LegacyRecovery) (bool, error)
	restoreConfig func(context.Context, *LegacyRecovery) (bool, error)
}

func newLegacyTransaction(record legacyTransactionRecord, recovery *LegacyRecovery, file *File) *LegacyTransaction {
	return &LegacyTransaction{record: record, configuration: recovery, file: file, durable: true, ops: legacyTransactionOps{
		saveJournal: func(ctx context.Context, file *File, data []byte, validate func([]byte) error) error {
			_, err := file.saveValidated(ctx, data, validate)
			return err
		},
		applyConfig: func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
			return recovery.ApplyConfig(ctx)
		},
		restoreConfig: func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
			return recovery.RestoreConfig(ctx)
		},
	}}
}

// LoadLegacyTransaction strictly checks journal structure and the immutable
// configuration artifact/hash without opening credentials or changing files.
func LoadLegacyTransaction(ctx context.Context, path string) (*LegacyTransaction, error) {
	if path == "" {
		return nil, ErrLegacyTransaction
	}
	file, err := ReadFile(ctx, path)
	if err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	var record legacyTransactionRecord
	if err := jsonv2.Unmarshal(file.original, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	recovery, err := LoadLegacyRecovery(ctx, record.Configuration)
	if err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	transaction := newLegacyTransaction(record, recovery, file)
	if err := transaction.validate(ctx, record); err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	return transaction, nil
}

func (t *LegacyTransaction) createJournal(ctx context.Context, ops fileCreationOps) error {
	data, err := t.encode(ctx)
	if err != nil {
		return err
	}
	_, err = t.file.createValidated(ctx, data, t.validator(ctx), ops)
	if err != nil {
		t.durable = false
		return err
	}
	return t.reopenJournal(ctx, data)
}

func (t *LegacyTransaction) persist(ctx context.Context) error {
	t.durable = false
	data, err := t.encode(ctx)
	if err != nil {
		return err
	}
	if err := t.configuration.check(ctx); err != nil {
		return err
	}
	if err := t.ops.saveJournal(ctx, t.file, data, t.validator(ctx)); err != nil {
		return err
	}
	return t.reopenJournal(ctx, data)
}

func (t *LegacyTransaction) reopenJournal(ctx context.Context, expected []byte) error {
	file, err := ReadFile(ctx, t.file.requested)
	if err != nil {
		return err
	}
	if file.path != t.file.path || !bytes.Equal(file.original, expected) {
		return ErrFileChanged
	}
	t.file, t.durable = file, true
	return nil
}

func (t *LegacyTransaction) encode(ctx context.Context) ([]byte, error) {
	if err := t.validate(ctx, t.record); err != nil {
		return nil, err
	}
	return jsonv2.Marshal(t.record)
}

func (t *LegacyTransaction) validator(ctx context.Context) func([]byte) error {
	return func(data []byte) error {
		var record legacyTransactionRecord
		if err := jsonv2.Unmarshal(data, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
			return &legacyTransactionFailure{cause: err}
		}
		return t.validate(ctx, record)
	}
}

func (t *LegacyTransaction) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.file == nil || t.configuration == nil || !t.durable {
		return ErrLegacyTransaction
	}
	if err := t.configuration.check(ctx); err != nil {
		return err
	}
	return t.file.checkSnapshot(ctx)
}

func (t *LegacyTransaction) validate(ctx context.Context, r legacyTransactionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.file == nil || t.configuration == nil || r.Version < 1 || r.Version > legacyTransactionVersion ||
		r.Configuration != t.configuration.file.requested || !filepath.IsAbs(r.Configuration) ||
		r.ConfigurationSHA256 != recoveryDigest(t.configuration.file.original) ||
		r.LegacyService == "" || strings.TrimSpace(r.LegacyService) != r.LegacyService {
		return ErrLegacyTransaction
	}
	for _, path := range []string{t.configuration.file.path, t.configuration.record.Source.Path, t.configuration.record.Target.Path} {
		if t.file.path == path {
			return ErrLegacyTransaction
		}
	}
	migration, err := prepareLegacyMigration(ctx, t.configuration.record.Source.Contents, t.configuration.record.Source.Path, t.configuration.record.Profile, t.configuration.record.Version)
	if err != nil {
		return err
	}
	selection := migration.Selection()
	if selection.Backend == core.CredentialBackendEnvironment {
		if r.Version < 2 || t.configuration.record.Version < 2 || r.CredentialBackup != "" ||
			r.CredentialState != "unexamined" && r.CredentialState != "external" {
			return ErrLegacyTransaction
		}
	} else if !core.ValidCredentialBackupID(r.CredentialBackup) || r.CredentialState == "external" {
		return ErrLegacyTransaction
	}
	source, target := migrationLocations(selection, r.LegacyService)
	if r.CredentialState != "unexamined" && r.CredentialState != "absent" && r.CredentialState != "present" && r.CredentialState != "external" {
		return ErrLegacyTransaction
	}
	if selection.Backend == core.CredentialBackendOnePassword && r.CredentialState == "present" {
		if r.SourceCredential != r.TargetCredential || r.TargetCredential.Backend != target.Backend ||
			r.TargetCredential.Account != target.Account || r.TargetCredential.SDKAccount != target.SDKAccount ||
			r.TargetCredential.Service != "" || r.TargetCredential.EnvironmentKey != "" || r.TargetCredential.Vault == "" || r.TargetCredential.Item == "" {
			return ErrLegacyTransaction
		}
	} else if r.SourceCredential != source || r.TargetCredential != target {
		return ErrLegacyTransaction
	}
	if r.CredentialState == "present" {
		if !validCommitment(r.BeforeCommitment) || !validCommitment(r.ExpectedCommitment) {
			return ErrLegacyTransaction
		}
	} else if r.BeforeCommitment != "" || r.ExpectedCommitment != "" {
		return ErrLegacyTransaction
	}
	return validateMigrationEffects(r)
}

func validCommitment(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func validateMigrationEffects(r legacyTransactionRecord) error {
	backup, apply, configApply, restoring, credentialRestored, configRestored := false, false, false, false, false, false
	for index, effect := range r.Effects {
		if effect.Profile != "" || effect.Sequence != index+1 || !validMigrationOutcome(effect) {
			return ErrLegacyTransaction
		}
		switch effect.Operation {
		case migrationBackup:
			if index != 0 || r.CredentialState != "present" || restoring {
				return ErrLegacyTransaction
			}
			backup = effect.Status == "acknowledged"
		case migrationApply:
			if !backup || apply || configApply || restoring || index != 1 {
				return ErrLegacyTransaction
			}
			apply = true
		case migrationConfigApply:
			if configApply || restoring || r.CredentialState == "unexamined" ||
				r.CredentialState == "present" && (!apply || r.Effects[index-1].Status != "acknowledged") {
				return ErrLegacyTransaction
			}
			configApply = true
		case migrationRestore:
			if !apply || configRestored {
				return ErrLegacyTransaction
			}
			restoring = true
			credentialRestored = effect.Status == "acknowledged"
		case migrationConfigRestore:
			if apply && !credentialRestored {
				return ErrLegacyTransaction
			}
			restoring = true
			configRestored = effect.Status == "acknowledged"
		default:
			return ErrLegacyTransaction
		}
		if index+1 < len(r.Effects) && effect.Status != "acknowledged" && !strings.HasSuffix(r.Effects[index+1].Operation, "restore") {
			return ErrLegacyTransaction
		}
	}
	switch r.Status {
	case "prepared":
		if len(r.Effects) != 0 || r.CredentialState != "unexamined" {
			return ErrLegacyTransaction
		}
	case "applying":
		if restoring || r.CredentialState == "unexamined" {
			return ErrLegacyTransaction
		}
	case "applied":
		if !configApply || restoring || r.Effects[len(r.Effects)-1].Status != "acknowledged" {
			return ErrLegacyTransaction
		}
	case "rolling_back":
	case "rolled_back":
		if !configRestored {
			return ErrLegacyTransaction
		}
	default:
		return ErrLegacyTransaction
	}
	return nil
}

func validMigrationOutcome(effect MigrationEffect) bool {
	switch effect.Status {
	case "pending":
		return effect.Attempted == nil && effect.ErrorCode == ""
	case "acknowledged":
		return effect.Attempted != nil && effect.ErrorCode == ""
	case "failed":
		return effect.Attempted != nil && !*effect.Attempted && validMigrationErrorCode(effect.ErrorCode)
	case "uncertain":
		return (effect.Attempted == nil || *effect.Attempted) && validMigrationErrorCode(effect.ErrorCode)
	default:
		return false
	}
}

func validMigrationErrorCode(code string) bool {
	return slices.Contains([]string{"canceled", "deadline", "changed", "invalid", "provider", "storage"}, code)
}

func migrationErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrFileChanged), errors.Is(err, core.ErrCredentialChanged):
		return "changed"
	case errors.Is(err, ErrLegacyTransaction), errors.Is(err, core.ErrCredentialRecoveryInvalid):
		return "invalid"
	default:
		var provider *core.CredentialStoreError
		if errors.As(err, &provider) {
			return "provider"
		}
		return "storage"
	}
}

// Receipt returns a caller-owned copy, including independent attempt bits.
func (t *LegacyTransaction) Receipt() MigrationReceipt {
	if t == nil || t.configuration == nil || t.file == nil {
		return MigrationReceipt{}
	}
	effects := slices.Clone(t.record.Effects)
	for index := range effects {
		if effects[index].Attempted != nil {
			value := *effects[index].Attempted
			effects[index].Attempted = &value
		}
	}
	return MigrationReceipt{
		Profile: t.configuration.record.Profile, Status: t.record.Status, Journal: t.file.requested,
		Configuration: t.record.Configuration, SourceSHA256: t.configuration.record.Source.SHA256,
		CandidateSHA256: t.configuration.record.CandidateSHA256, CredentialState: t.record.CredentialState,
		SourceCredential: t.record.SourceCredential, TargetCredential: t.record.TargetCredential,
		CredentialBackup: t.record.CredentialBackup, Effects: effects, JournalDurable: t.durable,
	}
}
