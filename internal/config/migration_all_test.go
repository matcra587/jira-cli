package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	koanftoml "github.com/knadh/koanf/parsers/toml"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

const allProfilesFixture = `# Retain these exact source bytes for recovery.
default_profile='second'
editor='global-editor'
[theme]
name='dracula'
[[profiles]]
name='first'
base_url='https://first.invalid'
email='first@example.invalid'
default_project='ONE'
timeout=0
editor='first-editor'
[[profiles]]
name='second'
base_url='https://second.invalid'
email='second@example.invalid'
default_project='TWO'
timeout=45
read_only=true
[[profiles]]
name='external'
base_url='https://external.invalid'
email='external@example.invalid'
secret_backend='env'
`

type profileMigrationTest struct {
	transaction            *ProfileTransaction
	provider               *transactionProvider
	sourcePath, targetPath string
	original               []byte
}

func newProfileMigrationTest(t *testing.T, fixture string, inPlace bool) *profileMigrationTest {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.toml")
	targetPath := filepath.Join(dir, "target.toml")
	writeTransactionFile(t, sourcePath, []byte(fixture))
	if inPlace {
		targetPath = sourcePath
	}
	source, err := OpenFile(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenFile(t.Context(), targetPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareProfileTransaction(t.Context(), source, target, "", "old-service")
	if err != nil {
		t.Fatal(err)
	}
	transaction, published, err := plan.Create(t.Context(), filepath.Join(dir, "recovery"))
	if err != nil || !published {
		t.Fatal("create recovery", published, err)
	}
	provider := &transactionProvider{values: map[core.CredentialLocation]transactionSnapshot{}, backups: map[string]transactionPair{}}
	for _, resource := range transaction.record.Resources {
		if resource.Source.Backend != core.CredentialBackendEnvironment {
			provider.values[resource.Source] = transactionSnapshot{location: resource.Source, value: "private-synthetic-" + resource.Profile, present: true, metadata: "original"}
		}
	}
	return &profileMigrationTest{transaction: transaction, provider: provider, sourcePath: sourcePath, targetPath: targetPath, original: []byte(fixture)}
}

func TestProfileMigrationRetainsEveryProfileAndDefault(t *testing.T) {
	migration, err := PrepareProfileMigration(t.Context(), []byte(allProfilesFixture), filepath.Join(t.TempDir(), "old.toml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(migration.Profiles(), []string{"external", "first", "second"}) || migration.DefaultProfile() != "second" {
		t.Fatal("profiles or authored default lost")
	}
	candidate, err := migration.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	fields, err := koanftoml.Parser().Unmarshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	profiles := fields["profiles"].(map[string]any)
	first, second := profiles["first"].(map[string]any), profiles["second"].(map[string]any)
	if first["timeout"] != "30s" || second["timeout"] != "45s" || first["editor"] != "first-editor" || second["editor"] != "global-editor" || fields["theme"] != "dracula" || fields["editor"] != "global-editor" {
		t.Fatal("legacy semantics changed")
	}
	if first["credential_id"] == second["credential_id"] {
		t.Fatal("independent profiles share a credential location")
	}
	if profiles["external"].(map[string]any)["api_token_env"] != "JIRA_TOKEN_EXTERNAL" {
		t.Fatal("external reference lost")
	}
}

func TestProfileMigrationAllApplyAndFreshRollback(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		name := "new target"
		if inPlace {
			name = "in place"
		}
		t.Run(name, func(t *testing.T) {
			state := newProfileMigrationTest(t, allProfilesFixture, inPlace)
			originalValues := map[core.CredentialLocation]transactionSnapshot{}
			for key, value := range state.provider.values {
				originalValues[key] = value
			}
			receipt, err := state.transaction.Apply(t.Context(), state.provider.store)
			if err != nil || receipt.Status != "applied" || !receipt.JournalDurable {
				t.Fatal("apply", err)
			}
			if len(receipt.Profiles) != 3 || len(state.provider.backups) != 2 {
				t.Fatal("not all credential resources backed up")
			}
			for _, resource := range state.transaction.record.Resources {
				if resource.State == "external" {
					continue
				}
				got, err := credentials.Load(t.Context(), &migrationCredentialSink{value: state.provider.values[resource.Target].value})
				if err != nil || !got.Complete() {
					t.Fatal("missing bound destination", err)
				}
				if got.Token != originalValues[resource.Source].value || state.provider.values[resource.Source] != originalValues[resource.Source] {
					t.Fatal("old keyring source changed or token mismatched")
				}
			}
			resolved, err := Load(Options{Path: state.targetPath, Profile: "first", Environ: func() []string { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Config.Timeout != 30*time.Second || resolved.Config.DefaultProject != "ONE" {
				t.Fatal("selected profile did not retain runtime values")
			}
			reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := reopened.PreviewRollback(t.Context())
			if err != nil || !preview.CredentialInspectionRequired || !preview.WouldRestoreConfiguration {
				t.Fatal("rollback preview", err)
			}
			receipt, err = reopened.Rollback(t.Context(), state.provider.store)
			if err != nil || receipt.Status != "rolled_back" || !receipt.JournalDurable {
				t.Fatal("rollback", err)
			}
			if inPlace {
				got, err := os.ReadFile(state.sourcePath)
				if err != nil || !bytes.Equal(got, state.original) {
					t.Fatal("exact preimage lost", err)
				}
			} else if _, err := os.Stat(state.targetPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("prior absence not restored")
			}
			for key, value := range originalValues {
				if state.provider.values[key] != value {
					t.Fatal("legacy source not retained")
				}
			}
			if len(state.provider.backups) != 2 {
				t.Fatal("provider backups deleted")
			}
		})
	}
}

func TestProfileMigrationFlatRetainsExistingSlotWithoutProviderReads(t *testing.T) {
	state := newProfileMigrationTest(t, "# flat\nbase_url='https://flat.invalid'\nemail='flat@example.invalid'\ntimeout='0s'\nread_only=false\n", true)
	calls := 0
	stores := func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		calls++
		return nil, errors.New("provider must stay closed")
	}
	receipt, err := state.transaction.Apply(t.Context(), stores)
	if err != nil || calls != 0 {
		t.Fatal("flat conversion opened provider", err)
	}
	resolved, err := Load(Options{Path: state.targetPath, Environ: func() []string { return nil }})
	if err != nil || resolved.Config.CredentialID != "default" || resolved.Config.Timeout != 0 {
		t.Fatal("flat slot/deadline changed", err)
	}
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Rollback(t.Context(), stores); err != nil || calls != 0 {
		t.Fatal("flat rollback opened provider", err)
	}
	got, err := os.ReadFile(state.sourcePath)
	if err != nil || !bytes.Equal(got, state.original) {
		t.Fatal("flat exact preimage lost", err)
	}
}

func TestProfileMigrationCanceledWriteReopensAndRollsBack(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state.provider.onMutation = func(operation string, after bool) error {
		if operation == "apply" && after {
			cancel()
		}
		return nil
	}
	receipt, err := state.transaction.Apply(ctx, state.provider.store)
	if !errors.Is(err, context.Canceled) || receipt.JournalDurable {
		t.Fatal("canceled publication lost unknown durable state", err)
	}
	if _, err := os.Stat(state.targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration published before all credentials")
	}
	state.provider.onMutation = nil
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = reopened.Rollback(t.Context(), state.provider.store)
	if err != nil || receipt.Status != "rolled_back" {
		t.Fatal("fresh rollback failed", err)
	}
	if _, err := os.Stat(state.targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rollback created configuration")
	}
}

func TestProfileMigrationRollbackPreflightsEveryResource(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	receipt, err := state.transaction.Apply(t.Context(), state.provider.store)
	if err != nil {
		t.Fatal(err)
	}
	resource := state.transaction.record.Resources[len(state.transaction.record.Resources)-1]
	changed := state.provider.values[resource.Target]
	changed.value = "unrelated-current-value"
	state.provider.values[resource.Target] = changed
	priorMutations := len(state.provider.mutations)
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Rollback(t.Context(), state.provider.store); !errors.Is(err, core.ErrCredentialChanged) {
		t.Fatal("changed last credential accepted", err)
	}
	if len(state.provider.mutations) != priorMutations {
		t.Fatal("restored early resource before later refusal")
	}
	if _, err := os.Stat(state.targetPath); err != nil {
		t.Fatal("changed credential triggered config restore", err)
	}
}

func TestProfileMigrationResolvesSDKAliasCollisionsBeforeWrites(t *testing.T) {
	fixture := strings.ReplaceAll(allProfilesFixture, "default_project='ONE'", "secret_backend='1password'\nvault='vault-name'\nitem='item-name'")
	fixture = strings.ReplaceAll(fixture, "default_project='TWO'", "secret_backend='1password'\nvault='vault-id'\nitem='item-id'")
	state := newProfileMigrationTest(t, fixture, false)
	canonical := core.CredentialLocation{Backend: core.CredentialBackendOnePassword, Account: "default", Vault: "vault-id", Item: "item-id"}
	state.provider.values[canonical] = transactionSnapshot{location: canonical, value: "private-synthetic-shared", present: true}
	stores := func(ctx context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		if location.Backend == core.CredentialBackendOnePassword {
			location = canonical
		}
		return state.provider.store(ctx, location)
	}
	receipt, err := state.transaction.Apply(t.Context(), stores)
	if !errors.Is(err, core.ErrCredentialRecoveryInvalid) || len(state.provider.mutations) != 0 || receipt.Status != "prepared" {
		t.Fatal("physical alias collision permitted writes", err)
	}
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal("refused apply corrupted journal", err)
	}
	if _, err := reopened.Rollback(t.Context(), nil); err != nil {
		t.Fatal("unused journal rollback failed", err)
	}
}

func TestProfileMigrationUnknownConfigWriteRetainsRecoverablePostimage(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	original := state.transaction.ops.applyConfig
	cause := errors.New("synthetic directory sync failure")
	state.transaction.ops.applyConfig = func(ctx context.Context, recovery *LegacyRecovery) (bool, error) {
		changed, err := original(ctx, recovery)
		if err != nil || !changed {
			return changed, err
		}
		return false, cause
	}
	receipt, err := state.transaction.Apply(t.Context(), state.provider.store)
	if !errors.Is(err, cause) || !receipt.JournalDurable {
		t.Fatal("publication outcome lost", err)
	}
	last := receipt.Effects[len(receipt.Effects)-1]
	if last.Operation != migrationConfigApply || last.Status != "uncertain" || last.Attempted != nil {
		t.Fatal("false-plus-error was advertised as unattempted")
	}
	if _, err := os.Stat(state.targetPath); err != nil {
		t.Fatal("named failure did not follow actual publication", err)
	}
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Rollback(t.Context(), state.provider.store); err != nil {
		t.Fatal("unknown configuration outcome could not recover", err)
	}
	if _, err := os.Stat(state.targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preimage absence not restored")
	}
}

func TestProfileMigrationPartialRollbackReopensAndRetries(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	receipt, err := state.transaction.Apply(t.Context(), state.provider.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state.provider.onMutation = func(operation string, after bool) error {
		if operation == "restore" && after {
			cancel()
		}
		return nil
	}
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = reopened.Rollback(ctx, state.provider.store)
	if !errors.Is(err, context.Canceled) || receipt.JournalDurable {
		t.Fatal("partial restoration did not retain cancellation", err)
	}
	if _, err := os.Stat(state.targetPath); err != nil {
		t.Fatal("configuration restored before all credentials", err)
	}
	state.provider.onMutation = nil
	reopened, err = LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = reopened.Rollback(t.Context(), state.provider.store)
	if err != nil || receipt.Status != "rolled_back" || !receipt.JournalDurable {
		t.Fatal("fresh rollback retry failed", err)
	}
}

func TestProfileMigrationReopenRejectsTamperedProviderBackup(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	receipt, err := state.transaction.Apply(t.Context(), state.provider.store)
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, resource := range state.transaction.record.Resources {
		if resource.State == "present" {
			backup = resource.Backup
			break
		}
	}
	pair := state.provider.backups[backup]
	pair.before.metadata = "tampered owned metadata"
	state.provider.backups[backup] = pair
	writes := len(state.provider.mutations)
	reopened, err := LoadMigrationTransaction(t.Context(), receipt.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Rollback(t.Context(), state.provider.store); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
		t.Fatal("tampered private backup accepted", err)
	}
	if len(state.provider.mutations) != writes {
		t.Fatal("tampered backup permitted a restore")
	}
}

func TestProfileMigrationAbsentCredentialDoesNotFabricateOrOpenDestination(t *testing.T) {
	state := newProfileMigrationTest(t, allProfilesFixture, false)
	var absent profileMigrationResource
	for _, resource := range state.transaction.record.Resources {
		if resource.Profile == "first" {
			absent = resource
			delete(state.provider.values, resource.Source)
		}
	}
	stores := func(ctx context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		if location == absent.Target {
			t.Fatal("absent source opened destination")
		}
		return state.provider.store(ctx, location)
	}
	receipt, err := state.transaction.Apply(t.Context(), stores)
	if err != nil || receipt.Status != "applied" {
		t.Fatal("absent profile blocked other migration", err)
	}
	for _, profile := range receipt.Profiles {
		if profile.Profile == "first" && profile.CredentialState != "absent" {
			t.Fatal("absence not reported")
		}
	}
	if len(state.provider.backups) != 1 {
		t.Fatal("absent source manufactured backup")
	}
}

func TestProfileMigrationPreservesLegacyProfileNames(t *testing.T) {
	for _, name := range []string{"_work", "-work", strings.Repeat("w", 80)} {
		t.Run(name, func(t *testing.T) {
			data := "default_profile='" + name + "'\n[[profiles]]\nname='" + name + "'\nsecret_backend='env'\n"
			migration, err := PrepareProfileMigration(t.Context(), []byte(data), filepath.Join(t.TempDir(), "old.toml"), "")
			if err != nil {
				t.Fatal("previously valid name rejected", err)
			}
			if !slices.Equal(migration.Profiles(), []string{name}) || migration.DefaultProfile() != name {
				t.Fatal("migration renamed authored profile")
			}
			candidate, err := migration.Candidate()
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFile(candidate); err != nil {
				t.Fatal("migrated name cannot be loaded", err)
			}
		})
	}
}
