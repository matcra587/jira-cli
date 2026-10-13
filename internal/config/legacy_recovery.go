package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	jsonv2 "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrLegacyRecovery identifies an invalid or incomplete configuration recovery
// record. Its public message never includes embedded configuration bytes.
var ErrLegacyRecovery = errors.New("invalid legacy configuration recovery")

type legacyRecoveryFailure struct{ cause error }

func (*legacyRecoveryFailure) Error() string   { return ErrLegacyRecovery.Error() }
func (e *legacyRecoveryFailure) Unwrap() error { return errors.Join(ErrLegacyRecovery, e.cause) }

type legacyRecoverySnapshot struct {
	Requested string `json:"requested"`
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Contents  []byte `json:"contents"`
	SHA256    string `json:"sha256"`
}

type legacyRecoveryRecord struct {
	Version         int                    `json:"version"`
	Profile         string                 `json:"profile"`
	Source          legacyRecoverySnapshot `json:"source"`
	Target          legacyRecoverySnapshot `json:"target"`
	Candidate       []byte                 `json:"candidate"`
	CandidateSHA256 string                 `json:"candidate_sha256"`
	ProfileIDs      map[string]string      `json:"profile_ids,omitempty"`
	SourceKind      string                 `json:"source_kind,omitempty"`
	SourceProfile   string                 `json:"source_profile,omitempty"`
	MappingVersion  int                    `json:"mapping_version,omitempty"`
}

// LegacyRecoveryPlan owns configuration preimages before publication. It has no
// credential state and cannot apply a migration. Provider backups and complete
// transaction orchestration remain separate requirements.
type LegacyRecoveryPlan struct{ record legacyRecoveryRecord }

// LegacyRecovery owns a published, immutable configuration recovery record.
// Every operation rechecks its record and expected configuration snapshot. These
// checks do not supply atomic compare-and-swap against external filesystem writers.
type LegacyRecovery struct {
	record legacyRecoveryRecord
	file   *File
}

// PrepareLegacyRecovery validates one selected legacy source and the destination
// preimage without creating directories or publishing configuration. Snapshots
// must come from OpenFile; unsupported or secret-bearing preimages are refused.
func PrepareLegacyRecovery(ctx context.Context, source, target *File, profile string) (*LegacyRecoveryPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || target == nil || !source.exists {
		return nil, ErrLegacyRecovery
	}
	migration, err := PrepareLegacyMigration(ctx, source.original, source.path, profile)
	if err != nil {
		return nil, err
	}
	candidate, err := migration.Candidate()
	if err != nil {
		return nil, err
	}
	record := legacyRecoveryRecord{
		Version: legacyMigrationVersion, Profile: migration.selection.Name,
		Source: recoverySnapshot(source), Target: recoverySnapshot(target),
		Candidate: candidate, CandidateSHA256: recoveryDigest(candidate),
	}
	if err := record.validate(ctx); err != nil {
		return nil, err
	}
	if err := source.checkSnapshot(ctx); err != nil {
		return nil, err
	}
	if err := target.checkSnapshot(ctx); err != nil {
		return nil, err
	}
	return &LegacyRecoveryPlan{record: record}, nil
}

// Create publishes a protected recovery record without replacing an existing
// name. No destination configuration is changed. A publication followed by a
// durability/read error returns created=true and no usable recovery owner.
func (p *LegacyRecoveryPlan) Create(ctx context.Context, path string) (*LegacyRecovery, bool, error) {
	return p.create(ctx, path, fileCreationOps{resolve: resolveCreationPath, link: os.Link, syncDirectory: syncCreationDirectory})
}

func (p *LegacyRecoveryPlan) create(ctx context.Context, path string, ops fileCreationOps) (*LegacyRecovery, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if p == nil || path == "" {
		return nil, false, ErrLegacyRecovery
	}
	if err := p.record.validate(ctx); err != nil {
		return nil, false, err
	}
	for _, snapshot := range []legacyRecoverySnapshot{p.record.Source, p.record.Target} {
		if _, err := snapshot.open(ctx); err != nil {
			return nil, false, err
		}
	}
	file, err := OpenNewFile(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if file.path == p.record.Source.Path || file.path == p.record.Target.Path {
		return nil, false, ErrLegacyRecovery
	}
	data, err := jsonv2.Marshal(p.record)
	if err != nil {
		return nil, false, ErrLegacyRecovery
	}
	created, err := file.createValidated(ctx, data, func(data []byte) error {
		_, err := decodeLegacyRecovery(ctx, data)
		return err
	}, ops)
	if err != nil || !created {
		return nil, created, err
	}
	recovery, err := LoadLegacyRecovery(ctx, path)
	if err == nil && !bytes.Equal(recovery.file.original, data) {
		return nil, true, ErrFileChanged
	}
	return recovery, true, err
}

// LoadLegacyRecovery reads and verifies a protected configuration record. It
// does not access any credential store or change the target configuration.
func LoadLegacyRecovery(ctx context.Context, path string) (*LegacyRecovery, error) {
	if path == "" {
		return nil, ErrLegacyRecovery
	}
	file, err := ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	record, err := decodeLegacyRecovery(ctx, file.original)
	if err != nil {
		return nil, err
	}
	if file.path == record.Source.Path || file.path == record.Target.Path {
		return nil, ErrLegacyRecovery
	}
	return &LegacyRecovery{record: *record, file: file}, nil
}

// ApplyConfig publishes the prepared flat configuration only after a recovery
// record exists and both expected preimages remain unchanged. Credential writes
// must be orchestrated separately before invoking this configuration boundary.
// Replacement errors can follow a completed rename; false does not prove no write.
func (r *LegacyRecovery) ApplyConfig(ctx context.Context) (bool, error) {
	if err := r.check(ctx); err != nil {
		return false, err
	}
	if _, err := r.record.Source.open(ctx); err != nil {
		return false, err
	}
	target, err := r.record.Target.open(ctx)
	if err != nil {
		return false, err
	}
	if target.exists {
		return target.Save(ctx, r.record.Candidate)
	}
	return target.Create(ctx, r.record.Candidate)
}

// RestoreConfig restores the exact prior configuration, or removes an originally
// absent destination. Changed postimages refuse rollback. An already restored
// preimage is a no-op; the recovery record remains available after every outcome.
// Replacement errors retain the same uncertainty as File.Save.
func (r *LegacyRecovery) RestoreConfig(ctx context.Context) (bool, error) {
	target, restored, err := r.checkRestore(ctx)
	if err != nil || restored {
		return false, err
	}
	if !r.record.Target.Exists {
		return target.remove(ctx)
	}
	return target.saveValidated(ctx, r.record.Target.Contents, validateRecoveryPreimage)
}

// Orchestration preflights all resources before its first rollback write. The
// actual restore repeats this check through the same owner immediately before
// publication; neither check provides filesystem compare-and-swap.
func (r *LegacyRecovery) checkRestore(ctx context.Context) (*File, bool, error) {
	if err := r.check(ctx); err != nil {
		return nil, false, err
	}
	target, err := OpenFile(ctx, r.record.Target.Requested)
	if err != nil {
		return nil, false, err
	}
	if target.path != r.record.Target.Path {
		return nil, false, ErrFileChanged
	}
	if target.exists == r.record.Target.Exists && bytes.Equal(target.original, r.record.Target.Contents) {
		return target, true, nil
	}
	if !target.exists || !bytes.Equal(target.original, r.record.Candidate) {
		return nil, false, ErrFileChanged
	}
	return target, false, nil
}

func (r *LegacyRecovery) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.file == nil {
		return ErrLegacyRecovery
	}
	return r.file.checkSnapshot(ctx)
}

func (r *LegacyRecovery) checkApplied(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	target, err := OpenFile(ctx, r.record.Target.Requested)
	if err != nil {
		return err
	}
	if target.path != r.record.Target.Path || !target.exists || !bytes.Equal(target.original, r.record.Candidate) {
		return ErrFileChanged
	}
	return nil
}

func recoverySnapshot(file *File) legacyRecoverySnapshot {
	return legacyRecoverySnapshot{
		Requested: file.requested, Path: file.path, Exists: file.exists,
		Contents: bytes.Clone(file.original), SHA256: recoveryDigest(file.original),
	}
}

func (s legacyRecoverySnapshot) open(ctx context.Context) (*File, error) {
	file, err := OpenFile(ctx, s.Requested)
	if err != nil {
		return nil, err
	}
	if file.path != s.Path || file.exists != s.Exists || !bytes.Equal(file.original, s.Contents) {
		return nil, ErrFileChanged
	}
	return file, nil
}

func recoveryDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validateRecoveryPreimage(data []byte) error {
	if fields, _, err := decodeFile(data); err == nil {
		for key := range fields {
			for secretKey := range secretKeys {
				if strings.EqualFold(key, secretKey) {
					return ErrLegacyRecovery
				}
			}
		}
		return nil
	}
	if _, _, err := decodeLegacyMigration(data); err == nil {
		return nil
	}
	return ErrLegacyRecovery
}

func (r legacyRecoveryRecord) validate(ctx context.Context) error {
	if r.Version < 1 || r.Version > namedMigrationVersion || !r.Source.Exists || !legacyProfileNamespace(r.Profile) {
		return ErrLegacyRecovery
	}
	for _, s := range []legacyRecoverySnapshot{r.Source, r.Target} {
		if !filepath.IsAbs(s.Requested) || !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != s.Path ||
			recoveryDigest(s.Contents) != s.SHA256 || !s.Exists && len(s.Contents) != 0 {
			return ErrLegacyRecovery
		}
		if s.Exists && validateRecoveryPreimage(s.Contents) != nil {
			return ErrLegacyRecovery
		}
	}
	if r.Version == namedMigrationVersion {
		if r.MappingVersion != legacyMigrationVersion {
			return ErrLegacyRecovery
		}
		migration, err := prepareProfileMigration(ctx, r.Source.Contents, r.Source.Path, r.SourceProfile, r.MappingVersion, r.ProfileIDs)
		if err != nil {
			return errors.Join(ErrLegacyRecovery, err)
		}
		candidate, err := migration.Candidate()
		if err != nil || migration.defaultProfile != r.Profile || migration.sourceKind != r.SourceKind || !bytes.Equal(candidate, r.Candidate) || recoveryDigest(r.Candidate) != r.CandidateSHA256 {
			return ErrLegacyRecovery
		}
		return ctx.Err()
	}
	if r.ProfileIDs != nil || r.SourceKind != "" || r.SourceProfile != "" || r.MappingVersion != 0 {
		return ErrLegacyRecovery
	}
	migration, err := prepareLegacyMigration(ctx, r.Source.Contents, r.Source.Path, r.Profile, r.Version)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrLegacyRecovery
	}
	candidate, err := migration.Candidate()
	if err != nil || !bytes.Equal(candidate, r.Candidate) || recoveryDigest(r.Candidate) != r.CandidateSHA256 {
		return ErrLegacyRecovery
	}
	return ctx.Err()
}

func decodeLegacyRecovery(ctx context.Context, data []byte) (*legacyRecoveryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var record legacyRecoveryRecord
	if err := jsonv2.Unmarshal(data, &record, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, &legacyRecoveryFailure{cause: err}
	}
	if err := record.validate(ctx); err != nil {
		return nil, err
	}
	return &record, nil
}
