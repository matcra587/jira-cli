package config

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	koanftoml "github.com/knadh/koanf/parsers/toml"

	"github.com/matcra587/jira-cli/internal/core"
)

// Named recovery has its own mapping policy. Existing policies 1–8 continue to
// derive their original single-profile bytes, including during rollback.
const namedMigrationVersion = 10

// ProfileMigration describes a configuration-only preparation. Tokens and
// provider snapshots are deliberately unavailable during this phase.
type ProfileMigration struct {
	candidate                  []byte
	profiles                   []string
	selections                 map[string]LegacySelection
	ids                        map[string]string
	mapped, unmapped           []string
	sourceKind, defaultProfile string
	sourceSHA256               string
	policy                     int
}

func (m *ProfileMigration) SourceSHA256() string   { return m.sourceSHA256 }
func (m *ProfileMigration) Profiles() []string     { return slices.Clone(m.profiles) }
func (m *ProfileMigration) DefaultProfile() string { return m.defaultProfile }
func (m *ProfileMigration) Mapped() []string       { return slices.Clone(m.mapped) }
func (m *ProfileMigration) Unmapped() []string     { return slices.Clone(m.unmapped) }
func (m *ProfileMigration) Candidate() ([]byte, error) {
	if m == nil || len(m.profiles) == 0 {
		return nil, ErrLegacyConfig
	}
	if len(m.unmapped) != 0 {
		return nil, ErrLegacyPreferencesUnmapped
	}
	return bytes.Clone(m.candidate), nil
}

// PrepareProfileMigration retains all legacy profiles by default. A source
// selector explicitly limits legacy conversion; a flat source becomes default.
// UUID assignment is public metadata and performs no provider/environment reads.
func PrepareProfileMigration(ctx context.Context, data []byte, path, selected string) (*ProfileMigration, error) {
	return prepareProfileMigration(ctx, data, path, selected, legacyMigrationVersion, nil)
}

func prepareProfileMigration(ctx context.Context, data []byte, path, selected string, policy int, ids map[string]string) (*ProfileMigration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fields, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		return nil, &legacyConfigFailure{cause: err}
	}
	if _, named := fields["format_version"]; named {
		return nil, ErrLegacyConfig
	}
	m := &ProfileMigration{selections: map[string]LegacySelection{}, ids: map[string]string{}, policy: policy, sourceSHA256: recoveryDigest(data)}
	globals := map[string]any{}
	profiles := map[string]any{}
	legacy, legacyFields, legacyErr := decodeLegacyMigration(data)
	detection, err := canonicalLegacyTable(fields)
	if err != nil {
		return nil, err
	}
	_, hasProfiles := detection["profiles"]
	_, hasDefault := detection["default_profile"]
	_, hasTUI := detection["tui"]
	_, hasTheme := detection["theme"].(map[string]any)
	if !hasProfiles && !hasDefault && !hasTUI && !hasTheme {
		if selected != "" {
			return nil, ErrLegacyConfig
		}
		if err := ValidateFile(data); err != nil {
			return nil, err
		}
		flat, _, err := decodeFile(data)
		if err != nil {
			return nil, err
		}
		m.sourceKind, m.defaultProfile = "flat", "default"
		m.profiles = []string{"default"}
		profile := maps.Clone(flat)
		splitMigrationGlobals(profile, globals)
		if editor, ok := profile["editor"]; ok {
			globals["editor"] = editor
			delete(profile, "editor")
		}
		if id, ok := profile["credential_id"].(string); ok && id != "" {
			m.ids["default"] = id
		} else {
			m.ids["default"] = "default"
		}
		profile["credential_id"] = m.ids["default"]
		profiles["default"] = profile
		m.mapped = slices.Sorted(maps.Keys(flat))
	} else {
		if legacyErr != nil {
			return nil, legacyErr
		}
		m.sourceKind, m.defaultProfile = "legacy", legacy.DefaultProfile
		if _, declared := legacyFields["editor"]; declared {
			globals["editor"] = strings.TrimSpace(legacy.Editor)
		}
		names := make([]string, 0, len(legacy.Profiles))
		for _, profile := range legacy.Profiles {
			names = append(names, profile.Name)
		}
		slices.Sort(names)
		if selected != "" {
			names = []string{selected}
			m.defaultProfile = selected
		}
		for _, name := range names {
			migration, err := prepareLegacyMigration(ctx, data, path, name, policy)
			if err != nil {
				return nil, err
			}
			m.profiles = append(m.profiles, name)
			m.selections[name] = migration.Selection()
			m.unmapped = append(m.unmapped, migration.Unmapped()...)
			m.mapped = append(m.mapped, migration.Mapped()...)
			// Decode even an unmapped preparation so previews can report every field;
			// Candidate still refuses publication until all mappings are resolved.
			profile, err := koanftoml.Parser().Unmarshal(migration.candidate)
			if err != nil {
				return nil, ErrLegacyConfig
			}
			splitMigrationGlobals(profile, globals)
			id := ids[name]
			if ids == nil {
				id = uuid.NewString()
			}
			if id == "" {
				return nil, ErrLegacyRecovery
			}
			m.ids[name] = id
			profile["credential_id"] = id
			profiles[name] = profile
		}
	}
	if ids != nil && !maps.Equal(ids, m.ids) {
		return nil, ErrLegacyRecovery
	}
	document := maps.Clone(globals)
	document["format_version"], document["default_profile"], document["profiles"] = int64(1), m.defaultProfile, profiles
	m.candidate, err = koanftoml.Parser().Marshal(document)
	if err != nil {
		return nil, ErrLegacyConfig
	}
	if err := ValidateFile(m.candidate); err != nil {
		return nil, err
	}
	slices.Sort(m.mapped)
	m.mapped = slices.Compact(m.mapped)
	slices.Sort(m.unmapped)
	m.unmapped = slices.Compact(m.unmapped)
	return m, ctx.Err()
}

func splitMigrationGlobals(profile, globals map[string]any) {
	// Editor remains in each profile: legacy profiles may override the global
	// editor. Flattening that fallback into each profile preserves its behavior.
	for _, key := range []string{"theme", "theme_path", "glyphs", "queries_path", "aliases"} {
		if value, ok := profile[key]; ok {
			globals[key] = value
			delete(profile, key)
		}
	}
}

// ProfileTransactionPlan carries one final document and all credential resources.
type ProfileTransactionPlan struct {
	configuration *LegacyRecoveryPlan
	migration     *ProfileMigration
	service       string
}

func PrepareProfileTransaction(ctx context.Context, source, target *File, selected, service string) (*ProfileTransactionPlan, error) {
	if source == nil || target == nil || !source.exists {
		return nil, ErrLegacyRecovery
	}
	migration, err := PrepareProfileMigration(ctx, source.original, source.path, selected)
	if err != nil {
		return nil, err
	}
	candidate, err := migration.Candidate()
	if err != nil {
		return nil, err
	}
	record := legacyRecoveryRecord{Version: namedMigrationVersion, Profile: migration.defaultProfile, Source: recoverySnapshot(source), Target: recoverySnapshot(target), Candidate: candidate, CandidateSHA256: recoveryDigest(candidate), ProfileIDs: migration.ids, SourceKind: migration.sourceKind, SourceProfile: selected, MappingVersion: migration.policy}
	if err := record.validate(ctx); err != nil {
		return nil, err
	}
	if err := source.checkSnapshot(ctx); err != nil {
		return nil, err
	}
	if err := target.checkSnapshot(ctx); err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	if service == "" {
		service = "jira-cli"
	}
	return &ProfileTransactionPlan{configuration: &LegacyRecoveryPlan{record: record}, migration: migration, service: service}, nil
}

func (p *ProfileTransactionPlan) RecoveryPaths(ctx context.Context, directory string) (core.MigrationRecoveryPaths, error) {
	return (&LegacyTransactionPlan{configuration: p.configuration}).RecoveryPaths(ctx, directory)
}

// CredentialLocations preserves the single-profile preview fields when the plan
// contains exactly one provider resource. Multi-profile effects use Profiles.
func (p *ProfileTransactionPlan) CredentialLocations() (core.CredentialLocation, core.CredentialLocation) {
	if len(p.migration.profiles) != 1 || p.migration.sourceKind != "legacy" {
		return core.CredentialLocation{}, core.CredentialLocation{}
	}
	name := p.migration.profiles[0]
	return profileMigrationLocations(p.migration.selections[name], p.service, p.migration.ids[name])
}
