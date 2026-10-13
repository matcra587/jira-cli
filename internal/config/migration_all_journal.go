package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

type profileMigrationResource struct {
	Profile  string                  `json:"profile"`
	State    string                  `json:"state"`
	Source   core.CredentialLocation `json:"source"`
	Target   core.CredentialLocation `json:"target"`
	Backup   string                  `json:"backup"`
	Before   string                  `json:"before,omitempty"`
	Expected string                  `json:"expected,omitempty"`
}

type profileTransactionRecord struct {
	Kind                string                     `json:"kind"`
	Version             int                        `json:"version"`
	Configuration       string                     `json:"configuration"`
	ConfigurationSHA256 string                     `json:"configuration_sha256"`
	Service             string                     `json:"legacy_service"`
	Status              string                     `json:"status"`
	Resources           []profileMigrationResource `json:"resources"`
	Effects             []MigrationEffect          `json:"effects"`
}

// ProfileTransaction publishes all credentials before the single named document.
// It retains every preimage and every pending operation for explicit rollback.
type ProfileTransaction struct {
	record        profileTransactionRecord
	configuration *LegacyRecovery
	file          *File
	durable       bool
	ops           legacyTransactionOps
}

func (p *ProfileTransactionPlan) Create(ctx context.Context, directory string) (*ProfileTransaction, bool, error) {
	paths, err := p.RecoveryPaths(ctx, directory)
	if err != nil {
		return nil, false, err
	}
	journal, err := OpenNewFile(ctx, paths.Journal)
	if err != nil {
		return nil, false, err
	}
	recovery, published, err := p.configuration.Create(ctx, paths.Configuration)
	if err != nil {
		return nil, published, err
	}
	record := profileTransactionRecord{Kind: "profiles", Version: 1, Configuration: recovery.file.requested, ConfigurationSHA256: recoveryDigest(recovery.file.original), Service: p.service, Status: "prepared"}
	if p.migration.sourceKind == "legacy" {
		for _, name := range p.migration.profiles {
			source, target := profileMigrationLocations(p.migration.selections[name], p.service, p.migration.ids[name])
			resource := profileMigrationResource{Profile: name, State: "unexamined", Source: source, Target: target}
			if source.Backend != core.CredentialBackendEnvironment {
				resource.Backup = core.NewCredentialBackupID()
			}
			record.Resources = append(record.Resources, resource)
		}
	}
	t := newProfileTransaction(record, recovery, journal)
	data, err := t.encode(ctx)
	if err != nil {
		return nil, true, err
	}
	_, err = journal.createValidated(ctx, data, t.validator(ctx), fileCreationOps{resolve: resolveCreationPath, link: os.Link, syncDirectory: syncCreationDirectory})
	if err != nil {
		return nil, true, err
	}
	if err := t.reopen(ctx, data); err != nil {
		return nil, true, err
	}
	return t, true, nil
}

func profileMigrationLocations(selection LegacySelection, service, id string) (core.CredentialLocation, core.CredentialLocation) {
	source, target := migrationLocations(selection, service)
	if target.Backend == core.CredentialBackendKeyring {
		target.Account = id
	}
	return source, target
}

func newProfileTransaction(record profileTransactionRecord, recovery *LegacyRecovery, file *File) *ProfileTransaction {
	base := newLegacyTransaction(legacyTransactionRecord{}, recovery, file)
	return &ProfileTransaction{record: record, configuration: recovery, file: file, durable: true, ops: base.ops}
}

// MigrationTransaction is the operation boundary shared by historical journals
// and named-profile journals. Reopening never selects current runtime settings.
type MigrationTransaction interface {
	Receipt() MigrationReceipt
	PreviewRollback(context.Context) (core.MigrationRollbackPreview, error)
	Rollback(context.Context, MigrationStores) (MigrationReceipt, error)
}

func LoadMigrationTransaction(ctx context.Context, path string) (MigrationTransaction, error) {
	file, err := ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if err := jsonv2.Unmarshal(file.original, &header); err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	if header.Kind == "" {
		return LoadLegacyTransaction(ctx, path)
	}
	if header.Kind != "profiles" {
		return nil, ErrLegacyTransaction
	}
	var record profileTransactionRecord
	if err := jsonv2.Unmarshal(file.original, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, &legacyTransactionFailure{cause: err}
	}
	recovery, err := LoadLegacyRecovery(ctx, record.Configuration)
	if err != nil {
		return nil, err
	}
	t := newProfileTransaction(record, recovery, file)
	if err := t.validate(ctx, record); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *ProfileTransaction) check(ctx context.Context) error {
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

func (t *ProfileTransaction) encode(ctx context.Context) ([]byte, error) {
	if err := t.validate(ctx, t.record); err != nil {
		return nil, err
	}
	return jsonv2.Marshal(t.record)
}

func (t *ProfileTransaction) validator(ctx context.Context) func([]byte) error {
	return func(data []byte) error {
		var record profileTransactionRecord
		if err := jsonv2.Unmarshal(data, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
			return &legacyTransactionFailure{cause: err}
		}
		return t.validate(ctx, record)
	}
}

func (t *ProfileTransaction) persist(ctx context.Context) error {
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
	return t.reopen(ctx, data)
}

func (t *ProfileTransaction) reopen(ctx context.Context, data []byte) error {
	file, err := ReadFile(ctx, t.file.requested)
	if err != nil {
		return err
	}
	if file.path != t.file.path || !bytes.Equal(file.original, data) {
		return ErrFileChanged
	}
	t.file, t.durable = file, true
	return nil
}

func (t *ProfileTransaction) validate(ctx context.Context, r profileTransactionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.file == nil || t.configuration == nil || t.configuration.record.Version != namedMigrationVersion || r.Kind != "profiles" || r.Version != 1 || r.Configuration != t.configuration.file.requested || r.ConfigurationSHA256 != recoveryDigest(t.configuration.file.original) || strings.TrimSpace(r.Service) == "" || r.Service != strings.TrimSpace(r.Service) {
		return ErrLegacyTransaction
	}
	for _, path := range []string{t.configuration.file.path, t.configuration.record.Source.Path, t.configuration.record.Target.Path} {
		if t.file.path == path {
			return ErrLegacyTransaction
		}
	}
	cr := t.configuration.record
	m, err := prepareProfileMigration(ctx, cr.Source.Contents, cr.Source.Path, cr.SourceProfile, cr.MappingVersion, cr.ProfileIDs)
	if err != nil {
		return err
	}
	expected := len(m.profiles)
	if m.sourceKind == "flat" {
		expected = 0
	}
	if len(r.Resources) != expected {
		return ErrLegacyTransaction
	}
	destinations := map[core.CredentialLocation]bool{}
	for i, resource := range r.Resources {
		if resource.Profile != m.profiles[i] {
			return ErrLegacyTransaction
		}
		source, target := profileMigrationLocations(m.selections[resource.Profile], r.Service, m.ids[resource.Profile])
		if resource.State != "unexamined" && resource.State != "external" && resource.State != "absent" && resource.State != "present" {
			return ErrLegacyTransaction
		}
		if source.Backend == core.CredentialBackendEnvironment {
			if resource.Backup != "" || resource.State != "unexamined" && resource.State != "external" {
				return ErrLegacyTransaction
			}
		} else if !core.ValidCredentialBackupID(resource.Backup) || resource.State == "external" {
			return ErrLegacyTransaction
		}
		if source.Backend == core.CredentialBackendOnePassword && resource.State == "present" {
			if resource.Source != resource.Target || !migrationSDKLocation(source, resource.Target) {
				return ErrLegacyTransaction
			}
		} else if resource.Source != source || resource.Target != target {
			return ErrLegacyTransaction
		}
		if resource.State == "present" {
			if !validCommitment(resource.Before) || !validCommitment(resource.Expected) || destinations[migrationPhysicalLocation(resource.Target)] {
				return ErrLegacyTransaction
			}
			destinations[migrationPhysicalLocation(resource.Target)] = true
		} else if resource.Before != "" || resource.Expected != "" {
			return ErrLegacyTransaction
		}
	}
	return validateProfileEffects(r)
}

// Account selectors can be aliases for one SDK account. Concrete vault/item
// IDs establish storage identity; a different selector cannot authorize writing
// that same field twice in one conversion.
func migrationPhysicalLocation(location core.CredentialLocation) core.CredentialLocation {
	if location.Backend == core.CredentialBackendOnePassword {
		location.SDKAccount = ""
	}
	return location
}

func migrationSDKLocation(selected, actual core.CredentialLocation) bool {
	return actual.Backend == selected.Backend && actual.Account == selected.Account && actual.SDKAccount == selected.SDKAccount && actual.Service == "" && actual.EnvironmentKey == "" && actual.Vault != "" && actual.Item != ""
}

func (t *ProfileTransaction) hasEffect(profile, operation string) bool {
	for _, effect := range t.record.Effects {
		if effect.Operation == operation && effect.Profile == profile {
			return true
		}
	}
	return false
}

func (t *ProfileTransaction) acknowledged(profile, operation string) bool {
	for _, effect := range t.record.Effects {
		if effect.Operation == operation && effect.Profile == profile && effect.Status == "acknowledged" {
			return true
		}
	}
	return false
}

func validateProfileEffects(r profileTransactionRecord) error {
	byName := map[string]profileMigrationResource{}
	for _, resource := range r.Resources {
		byName[resource.Profile] = resource
	}
	backup, apply, restored := map[string]bool{}, map[string]bool{}, map[string]bool{}
	configApply, configRestore, rolling := false, false, false
	for i, effect := range r.Effects {
		if effect.Sequence != i+1 || !validMigrationOutcome(effect) {
			return ErrLegacyTransaction
		}
		operation, name := effect.Operation, effect.Profile
		if name != "" {
			resource, ok := byName[name]
			if !ok || resource.State != "present" {
				return ErrLegacyTransaction
			}
		}
		switch operation {
		case migrationBackup:
			if name == "" || backup[name] || apply[name] || rolling || configApply {
				return ErrLegacyTransaction
			}
			backup[name] = effect.Status == "acknowledged"
		case migrationApply:
			if name == "" || !backup[name] || apply[name] || rolling || configApply {
				return ErrLegacyTransaction
			}
			apply[name] = true
		case migrationRestore:
			if name == "" || !apply[name] || configRestore {
				return ErrLegacyTransaction
			}
			rolling = true
			restored[name] = effect.Status == "acknowledged"
		case migrationConfigApply:
			if name != "" || configApply || rolling {
				return ErrLegacyTransaction
			}
			for _, resource := range r.Resources {
				if resource.State == "unexamined" || resource.State == "present" && !apply[resource.Profile] {
					return ErrLegacyTransaction
				}
			}
			configApply = true
		case migrationConfigRestore:
			if name != "" {
				return ErrLegacyTransaction
			}
			for name := range apply {
				if !restored[name] {
					return ErrLegacyTransaction
				}
			}
			rolling = true
			configRestore = effect.Status == "acknowledged"
		default:
			return ErrLegacyTransaction
		}
		if i+1 < len(r.Effects) && effect.Status != "acknowledged" && !strings.HasSuffix(r.Effects[i+1].Operation, "restore") {
			return ErrLegacyTransaction
		}
	}
	switch r.Status {
	case "prepared":
		if len(r.Effects) != 0 {
			return ErrLegacyTransaction
		}
		for _, resource := range r.Resources {
			if resource.State != "unexamined" {
				return ErrLegacyTransaction
			}
		}
	case "applying":
		if rolling {
			return ErrLegacyTransaction
		}
	case "applied":
		if !configApply || rolling || len(r.Effects) == 0 || r.Effects[len(r.Effects)-1].Status != "acknowledged" {
			return ErrLegacyTransaction
		}
	case "rolling_back":
	case "rolled_back":
		if !configRestore {
			return ErrLegacyTransaction
		}
	default:
		return ErrLegacyTransaction
	}
	return nil
}

func (t *ProfileTransaction) runEffect(ctx context.Context, profile, operation string, apply func() (*bool, error)) error {
	if err := t.check(ctx); err != nil {
		return err
	}
	t.record.Effects = append(t.record.Effects, MigrationEffect{Sequence: len(t.record.Effects) + 1, Operation: operation, Profile: profile, Status: "pending"})
	if err := t.persist(ctx); err != nil {
		return err
	}
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
	return errors.Join(operationErr, t.persist(ctx))
}

func (t *ProfileTransaction) Receipt() MigrationReceipt {
	if t == nil || t.configuration == nil || t.file == nil {
		return MigrationReceipt{}
	}
	effects := slices.Clone(t.record.Effects)
	for i := range effects {
		if effects[i].Attempted != nil {
			value := *effects[i].Attempted
			effects[i].Attempted = &value
		}
	}
	profiles := make([]core.MigrationProfileReceipt, 0, len(t.record.Resources))
	for _, resource := range t.record.Resources {
		profiles = append(profiles, core.MigrationProfileReceipt{Profile: resource.Profile, CredentialState: resource.State, SourceCredential: resource.Source, TargetCredential: resource.Target, CredentialBackup: resource.Backup})
	}
	state := "multiple"
	if t.configuration.record.SourceKind == "flat" {
		state = "preserved"
		profiles = append(profiles, core.MigrationProfileReceipt{Profile: t.configuration.record.Profile, CredentialState: state})
	}
	receipt := MigrationReceipt{Profile: t.configuration.record.Profile, Profiles: profiles, Status: t.record.Status, Journal: t.file.requested, Configuration: t.record.Configuration, SourceSHA256: t.configuration.record.Source.SHA256, CandidateSHA256: t.configuration.record.CandidateSHA256, CredentialState: state, Effects: effects, JournalDurable: t.durable}
	if len(profiles) == 1 {
		profile := profiles[0]
		receipt.CredentialState, receipt.SourceCredential, receipt.TargetCredential, receipt.CredentialBackup = profile.CredentialState, profile.SourceCredential, profile.TargetCredential, profile.CredentialBackup
	}
	return receipt
}
