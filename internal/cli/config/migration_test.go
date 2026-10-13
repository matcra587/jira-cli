package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

const migrationCommandFixture = "default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='person@example.invalid'\n"

func migrationCommandPaths(t *testing.T, extra string) migrationFlags {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "old.toml")
	if err := os.WriteFile(source, []byte(migrationCommandFixture+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.SetFlagLayer(cfg.FlagLayer{Path: filepath.Join(dir, "new.toml")})
	t.Cleanup(func() { cfg.SetFlagLayer(cfg.FlagLayer{}) })
	return migrationFlags{source: source, directory: filepath.Join(dir, "recovery"), service: "jira-cli", yes: true}
}

func TestMigrationCommandPreviewsAndUnmappedPreferencesOpenNothing(t *testing.T) {
	for _, extra := range []string{"", "account_id='cached-account'\n", "[tui]\ntabs=['private-tab-marker']\n", "secret_backend='env'\n", "secret_backend='1password'\nvault='vault-id'\nitem='item-id'\n"} {
		flags := migrationCommandPaths(t, extra)
		calls := 0
		deps := Deps{MigrationStores: func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
			calls++
			return nil, errors.New("unexpected provider")
		}, Confirm: func(context.Context, prompt.Confirm) (bool, error) { calls++; return false, nil }}
		out, err := runMigrate(t.Context(), deps, command.Invocation{DryRun: true}, flags)
		if err != nil || calls != 0 || out.RecoveryPublished || out.Receipt != nil || out.Profile != "work" || !out.DryRun {
			t.Fatal("preview crossed publication/provider/confirmation boundary", err)
		}
		if strings.Contains(extra, "tabs") {
			if out.Ready || len(out.Unmapped) == 0 || strings.Contains(strings.Join(out.Unmapped, " "), "private-tab-marker") {
				t.Fatal("unmapped state was lost or exposed")
			}
		} else if !out.Ready || len(out.Unmapped) != 0 {
			t.Fatal("supported mapping falsely refused publication")
		}
		if _, err := os.Stat(flags.directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("preview created recovery directory")
		}
	}
}

func TestMigrationCommandHeadlessAndCanceledConfirmationsHaveNoEffects(t *testing.T) {
	flags := migrationCommandPaths(t, "")
	flags.yes = false
	calls := 0
	deps := Deps{Confirm: func(context.Context, prompt.Confirm) (bool, error) { calls++; return true, nil }}
	for _, inv := range []command.Invocation{{}, {NoInput: true}, {NoInput: true, Interactive: true}} {
		if _, err := runMigrate(t.Context(), deps, inv, flags); cli.MapError(err).Code != cli.CodeFlagRequired {
			t.Fatal("headless conversion did not require explicit yes")
		}
	}
	if calls != 0 {
		t.Fatal("headless run prompted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	deps.Confirm = func(context.Context, prompt.Confirm) (bool, error) { cancel(); return true, nil }
	if _, err := runMigrate(ctx, deps, command.Invocation{Interactive: true}, flags); !errors.Is(err, context.Canceled) {
		t.Fatal("expired answer started conversion")
	}
	if _, err := os.Stat(flags.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused confirmation published recovery")
	}
	confirmationContext, cancelConfirmation := context.WithCancel(t.Context())
	defer cancelConfirmation()
	deps.Confirm = func(context.Context, prompt.Confirm) (bool, error) { cancelConfirmation(); return true, nil }
	if err := confirmMigration(confirmationContext, deps, command.Invocation{Interactive: true}, false, prompt.Confirm{Question: "Synthetic confirmation?"}); !errors.Is(err, context.Canceled) {
		t.Fatal("confirmation owner accepted an answer after cancellation")
	}
}

func TestMigrationCommandProviderFailureRetainsPublishedReceiptAndPrivateCause(t *testing.T) {
	flags := migrationCommandPaths(t, "")
	cause := errors.New("private-provider-test-material")
	out, err := runMigrate(t.Context(), Deps{MigrationStores: func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		return nil, cause
	}}, command.Invocation{}, flags)
	if !errors.Is(err, cause) || cli.MapError(err).Code != cli.CodeConfigMigrationFailed || strings.Contains(err.Error(), cause.Error()) || !out.RecoveryPublished || out.Receipt == nil || out.Receipt.Status != "prepared" {
		t.Fatal("provider failure lost actual receipt, classification or privacy")
	}
	source, err := os.ReadFile(flags.source)
	if err != nil || !bytes.Equal(source, []byte(migrationCommandFixture)) {
		t.Fatal("provider failure changed source")
	}
	cmd := newMigrate(Deps{MigrationStores: func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		return nil, cause
	}})
	flags.directory = filepath.Join(filepath.Dir(flags.source), "other-recovery")
	if err := cmd.ParseFlags([]string{"--source", flags.source, "--recovery-dir", flags.directory, "--yes"}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(t.Context())
	err = cmd.RunE(cmd, nil)
	var retained *cli.ResultError
	if !errors.As(err, &retained) || retained.Data == nil || retained.Op != "config.migrate" {
		t.Fatal("construction path discarded recovery evidence")
	}
}

func TestMigrationCommandRecoveryCollisionDoesNotPublishAndRollbackPreviewOpensNoStore(t *testing.T) {
	flags := migrationCommandPaths(t, "")
	source, err := cfg.OpenFile(t.Context(), flags.source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := cfg.OpenFile(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := cfg.PrepareLegacyTransaction(t.Context(), source, target, "", "")
	if err != nil {
		t.Fatal(err)
	}
	transaction, _, err := plan.Create(t.Context(), flags.directory)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runMigrate(t.Context(), Deps{}, command.Invocation{DryRun: true}, flags); err == nil || out.RecoveryPublished {
		t.Fatal("preview accepted occupied recovery")
	}
	calls := 0
	deps := Deps{MigrationStores: func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		calls++
		return nil, errors.New("unexpected store")
	}}
	out, err := runRollback(t.Context(), deps, command.Invocation{DryRun: true}, transaction.Receipt().Journal, false)
	if err != nil || calls != 0 || out.Restored || out.CredentialInspectionRequired || out.WouldRestoreConfiguration || out.Receipt == nil {
		t.Fatal("rollback preview fabricated restored state or opened provider", err)
	}
	if out, err := runRollback(t.Context(), deps, command.Invocation{}, transaction.Receipt().Journal, true); err != nil || !out.Restored || calls != 0 {
		t.Fatal("unused transaction rollback did not finish without credentials", err)
	}
}

func TestMigrationErrorDiagnosisPrecedesProviderFallbackAndPreservesCancellation(t *testing.T) {
	for _, pair := range []struct {
		cause error
		code  string
	}{{core.ErrCredentialChanged, cli.CodeConfigMigrationChanged}, {core.ErrCredentialRecoveryInvalid, cli.CodeConfigRecoveryInvalid}, {errors.New("private-native-failure"), cli.CodeConfigMigrationFailed}, {context.Canceled, cli.CodeCanceled}} {
		provider := &core.CredentialStoreError{Backend: core.CredentialBackendKeyring, Operation: "restore", Cause: pair.cause, Attempted: true}
		err := migrationError(provider)
		mapped := cli.MapError(err)
		if mapped.Code != pair.code || !errors.Is(err, pair.cause) || strings.Contains(mapped.Message, "private-native-failure") || strings.Contains(mapped.Hint, "environment credentials") {
			t.Fatal("provider fallback hid recovery diagnosis or private material", mapped)
		}
	}
}

func TestMigrationConfirmationDistinguishesAbortionAndFailure(t *testing.T) {
	for _, pair := range []struct {
		cause error
		code  string
	}{{huh.ErrUserAborted, cli.CodeCanceled}, {errors.New("synthetic prompt failure"), cli.CodeConfigMigrationFailed}} {
		flags := migrationCommandPaths(t, "")
		flags.yes = false
		deps := Deps{Confirm: func(context.Context, prompt.Confirm) (bool, error) { return false, pair.cause }}
		_, err := runMigrate(t.Context(), deps, command.Invocation{Interactive: true}, flags)
		if cli.MapError(err).Code != pair.code || !errors.Is(err, pair.cause) {
			t.Fatal("confirmation lost distinct failure or cause")
		}
		if _, err := os.Stat(flags.directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed confirmation published recovery")
		}
	}
}

func TestMigrationCommandConvertsAllProfilesAndExplicitSubset(t *testing.T) {
	for _, selected := range []string{"", "work"} {
		name := "all profiles"
		if selected != "" {
			name = "explicit source profile"
		}
		t.Run(name, func(t *testing.T) {
			flags := migrationCommandPaths(t, "secret_backend='env'\n[[profiles]]\nname='personal'\nbase_url='https://personal.invalid'\nemail='personal@example.invalid'\nsecret_backend='env'\n")
			flags.profile = selected
			calls := 0
			stores := func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
				calls++
				return nil, errors.New("unexpected provider")
			}
			out, err := runMigrate(t.Context(), Deps{MigrationStores: stores}, command.Invocation{}, flags)
			want := 2
			if selected != "" {
				want = 1
			}
			if err != nil || len(out.Profiles) != want || out.Receipt == nil || out.Receipt.Status != "applied" || calls != 0 {
				t.Fatal("profiles not converted", err)
			}
			for _, profile := range out.Profiles {
				resolved, err := cfg.Load(cfg.Options{Path: out.Path, Profile: profile, Environ: func() []string { return nil }})
				if err != nil || resolved.Config.SecretBackend != core.CredentialBackendEnvironment {
					t.Fatal("converted profile unavailable", err)
				}
			}
			rollback, err := runRollback(t.Context(), Deps{MigrationStores: stores}, command.Invocation{}, out.Receipt.Journal, true)
			if err != nil || !rollback.Restored || calls != 0 {
				t.Fatal("all-profile rollback", err)
			}
		})
	}
}
