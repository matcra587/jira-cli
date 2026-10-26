package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type migrationFlags struct {
	source, profile, directory, service string
	yes, deferTUI                       bool
}

func newMigrate(deps Deps) *cobra.Command {
	var flags migrationFlags
	return command.MustNew(command.Spec[envelope.ConfigMigrateOutput]{
		Op: "config.migrate", Use: "migrate", Short: "Convert configuration into named profiles with recovery",
		MutatesLocalState: true,
		Long: "Convert every legacy profile, or a flat configuration, into a named-profile document. " +
			"--source selects the explicit input; --config selects the destination. A flat input becomes the default profile. " +
			"--source-profile explicitly selects a legacy subset. Global --profile selects runtime settings and does not filter migration. " +
			"The source default and all supported preferences are retained.\n\n" +
			"Migration does not warm board caches. Before using a retained default_board with structured issue filters, " +
			"run issue list --board NAME --refresh --as-jql for that profile. Explicit --jql and --saved override default_board.\n\n" +
			"--dry-run reads local files only and creates nothing. It does not inspect credentials. " +
			"Unmapped preferences block conversion; --defer-tui retains recognized TUI settings only in exact recovery bytes. " +
			"Legacy environment token references are preserved without reading their values.\n\n" +
			"A live conversion requires an unused --recovery-dir and --yes in headless mode. Protected configuration preimages " +
			"and a durable per-resource journal precede provider writes. All provider destinations are resolved before writes; " +
			"aliases to the same destination refuse conversion. Credentials are backed up in their provider and the final " +
			"configuration is published last. Old keyring entries remain intact. Flat input retains its current credential slot. " +
			"No Jira authentication is attempted. Supply --legacy-keyring-service for an old custom keyring namespace.\n\n" +
			"Keep recovery records after success or failure. Interrupted apply is never replayed: use config rollback with " +
			"the recorded journal. Writes may complete before an error or cancellation. Recovery checks are not compare-and-swap.",
		Example: `$ jira config migrate --source=old.toml --config=settings.toml --dry-run --output=json

$ jira config migrate --source=old.toml --config=settings.toml --recovery-dir=recovery --yes --no-input`,
		Flags: func(cmd *cobra.Command) {
			command.AddStringVar(cmd, &flags.source, "source", "", "Read the explicit legacy configuration file", command.FlagOpts{Terse: "Legacy source", Placeholder: "PATH", Hint: "file", Forward: true})
			command.AddStringVar(cmd, &flags.profile, "source-profile", "", "Convert only this legacy profile; omission preserves all profiles", command.FlagOpts{Terse: "Legacy profile", Placeholder: "NAME", Complete: "predictor=legacyprofile"})
			command.AddStringVar(cmd, &flags.directory, "recovery-dir", "", "Publish protected recovery in this unused explicit directory", command.FlagOpts{Terse: "Recovery directory", Placeholder: "DIR", Hint: "dir"})
			command.AddStringVar(cmd, &flags.service, "legacy-keyring-service", "jira-cli", "Read the historical keyring service namespace", command.FlagOpts{Terse: "Historical keyring service", Placeholder: "NAME"})
			command.AddBoolVar(cmd, &flags.yes, "yes", false, "Confirm conversion without prompting", command.FlagOpts{Terse: "Confirm conversion", Shorthand: "y"})
			command.AddBoolVar(cmd, &flags.deferTUI, "defer-tui", false, "Retain recognized TUI settings in recovery for later migration", command.FlagOpts{Terse: "Defer TUI settings"})
			command.AddDryRunFlag(cmd)
		},
		Validate:      func() error { return migrationRequired("source", flags.source) },
		Human:         renderMigrateHuman,
		RetainOnError: func(out envelope.ConfigMigrateOutput) bool { return out.Profile != "" },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigMigrateOutput, error) {
			return runMigrate(ctx, deps, inv, flags)
		},
	})
}

func runMigrate(ctx context.Context, deps Deps, inv command.Invocation, flags migrationFlags) (envelope.ConfigMigrateOutput, error) {
	out := envelope.ConfigMigrateOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !inv.DryRun {
		if err := migrationRequired("recovery-dir", flags.directory); err != nil {
			return out, err
		}
		if err := migrationHeadless(inv, flags.yes); err != nil {
			return out, err
		}
	}
	source, err := cfg.ReadFile(ctx, flags.source)
	if err != nil {
		return out, migrationError(err)
	}
	target, err := cfg.OpenFile(ctx, "")
	if err != nil {
		return out, migrationError(err)
	}
	options := cfg.LegacyMigrationOptions{DeferTUI: flags.deferTUI}
	migration, err := cfg.PrepareProfileMigration(ctx, source.Contents(), source.Path(), flags.profile, options)
	if err != nil {
		return out, migrationError(err)
	}
	out.SourcePath, out.Path, out.Profile = source.Path(), target.Path(), migration.DefaultProfile()
	out.Profiles = migration.Profiles()
	out.SourceSHA256 = migration.SourceSHA256()
	out.Mapped, out.Unmapped, out.Deferred = migration.Mapped(), migration.Unmapped(), migration.Deferred()
	if out.Deferred == nil {
		out.Deferred = []string{}
	}
	if out.Mapped == nil {
		out.Mapped = []string{}
	}
	if out.Unmapped == nil {
		out.Unmapped = []string{}
	}
	out.Ready = len(out.Unmapped) == 0
	if !out.Ready {
		if inv.DryRun {
			return out, nil
		}
		return out, migrationError(cfg.ErrLegacyPreferencesUnmapped)
	}
	plan, err := cfg.PrepareProfileTransaction(ctx, source, target, flags.profile, flags.service, options)
	if err != nil {
		return out, migrationError(err)
	}
	out.SourceCredential, out.TargetCredential = plan.CredentialLocations()
	out.Backend = out.SourceCredential.Backend
	if flags.directory != "" {
		out.Recovery, err = plan.RecoveryPaths(ctx, flags.directory)
		if err != nil {
			return out, migrationError(err)
		}
	}
	if inv.DryRun {
		return out, nil
	}
	if err := confirmMigration(ctx, deps, inv, flags.yes, prompt.Confirm{
		Question:    prompt.Question("Convert profiles ", cli.Text(strings.Join(out.Profiles, ", ")), " from ", cli.Text(out.SourcePath), " into ", cli.Text(out.Path), "?"),
		Caveat:      "Credential state may change. Configuration recovery and provider backups are retained; no Jira authentication is attempted.",
		Affirmative: "Convert", Negative: "Keep",
	}); err != nil {
		return out, err
	}
	transaction, published, err := plan.Create(ctx, flags.directory)
	out.RecoveryPublished = published
	if err != nil {
		return out, migrationError(err)
	}
	receipt, err := transaction.Apply(ctx, deps.MigrationStores)
	out.Receipt = &receipt
	if err != nil {
		return out, migrationError(err)
	}
	return out, nil
}

func migrationRequired(flag, value string) error {
	if strings.TrimSpace(value) != "" {
		return nil
	}
	err := cli.NewInputError(cli.CodeFlagRequired, "--"+flag+" is required")
	err.Field = flag
	return err
}

func migrationHeadless(inv command.Invocation, yes bool) error {
	if inv.DryRun || yes || inv.Interactive && !inv.NoInput {
		return nil
	}
	err := cli.NewInputError(cli.CodeFlagRequired, "migration and rollback change stored state; pass --yes to confirm without prompting")
	err.Field = "yes"
	return err
}

func confirmMigration(ctx context.Context, deps Deps, inv command.Invocation, yes bool, question prompt.Confirm) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if inv.DryRun || yes {
		return nil
	}
	if err := migrationHeadless(inv, yes); err != nil {
		return err
	}
	ask := deps.Confirm
	if ask == nil {
		ask = prompt.Run
	}
	answer, err := ask(ctx, question)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return &cli.CodedError{Code: cli.CodeCanceled, Message: "migration canceled; no state was changed", Cause: err}
		}
		return &cli.CodedError{Code: cli.CodeConfigMigrationFailed, Message: "migration confirmation failed; no state was changed", Cause: err}
	}
	if !answer {
		return cli.NewCodedError(cli.CodeCanceled, "migration canceled; no state was changed")
	}
	return nil
}

func migrationError(err error) error {
	code, message := cli.CodeConfigMigrationFailed, "migration or rollback did not complete; inspect retained recovery before retrying"
	switch {
	case errors.Is(err, cfg.ErrLegacyPreferencesUnmapped):
		code, message = cli.CodeConfigMigrationUnmapped, "selected legacy preferences require runtime mappings before conversion"
	case errors.Is(err, cfg.ErrFileChanged), errors.Is(err, core.ErrCredentialChanged), errors.Is(err, core.ErrCredentialBackupExists):
		code, message = cli.CodeConfigMigrationChanged, "migration resources changed; expected preimages or postimages no longer match"
	case errors.Is(err, cfg.ErrLegacyRecovery), errors.Is(err, core.ErrCredentialRecoveryInvalid), errors.Is(err, core.ErrCredentialRecoveryUnsupported):
		code, message = cli.CodeConfigRecoveryInvalid, "migration input or recovery state cannot be safely applied"
	case errors.Is(err, cfg.ErrLegacyConfig), errors.Is(err, cfg.ErrFileInvalid):
		code, message = cli.CodeConfigInvalid, "selected configuration is invalid for migration"
	}
	return &cli.CodedError{Code: code, Message: message, Cause: err}
}

func renderMigrateHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigMigrateOutput)
	if !ok {
		return fmt.Errorf("config.migrate: unexpected payload %T", env.Data)
	}
	switch {
	case !env.OK:
		v.Line("conversion refused or incomplete; inspect retained recovery")
	case out.DryRun && !out.Ready:
		v.Line("would refuse conversion; unmapped preferences:", strings.Join(out.Unmapped, ", "))
	case out.DryRun:
		v.Line("would convert profiles", cli.Text(strings.Join(out.Profiles, ", ")), "into", cli.Text(out.Path))
	case out.Receipt != nil && out.Receipt.Status == "applied" && out.Receipt.JournalDurable:
		v.Line("converted profiles", cli.Text(strings.Join(out.Profiles, ", ")), "into", cli.Text(out.Path))
	default:
		v.Line("conversion incomplete; inspect retained recovery")
	}
	if len(out.Deferred) != 0 {
		v.Line("TUI settings deferred; retained only in original source/recovery:", strings.Join(out.Deferred, ", "))
	}
	if out.RecoveryPublished {
		v.Line("configuration recovery:", cli.Text(out.Recovery.Configuration))
	}
	if out.Receipt != nil {
		renderMigrationReceipt(v, *out.Receipt)
	}
	return nil
}

func renderMigrationReceipt(v *output.View, receipt core.MigrationReceipt) {
	v.Line("journal:", cli.Text(receipt.Journal))
	for _, effect := range receipt.Effects {
		if effect.Profile != "" {
			v.Line(cli.Text(effect.Profile), cli.Text(effect.Operation), cli.Text(effect.Status))
		} else {
			v.Line(cli.Text(effect.Operation), cli.Text(effect.Status))
		}
	}
	if !receipt.JournalDurable {
		v.Line("latest outcome is not durably recorded; reopen the journal before rollback")
	}
}
