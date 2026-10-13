package config

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newRollback(deps Deps) *cobra.Command {
	var journal string
	var yes bool
	return command.MustNew(command.Spec[envelope.ConfigRollbackOutput]{
		Op: "config.rollback", Use: "rollback", Short: "Restore migration preimages from a retained journal",
		MutatesLocalState: true,
		Long: "Restore exact configuration and credential preimages from explicit --journal. " +
			"The recorded paths and concrete provider identities select recovery; current global --config " +
			"and environment settings do not retarget it.\n\n" +
			"--dry-run reads only the journal, immutable configuration artifact and configuration snapshots. " +
			"It reports whether real rollback still requires credential inspection, without opening a provider " +
			"or claiming credential postimages were verified.\n\n" +
			"A live rollback requires --yes in headless mode. It checks all expected resource states before " +
			"its first write, restores credentials before configuration, and restores prior absence when applicable. " +
			"Changed resources refuse restoration. Recovery records and provider backups remain retained. " +
			"A failed or canceled restoration can be retried explicitly with a fresh context and the same journal. " +
			"These checks do not provide filesystem/provider compare-and-swap.",
		Example: `$ jira config rollback --journal=recovery/migration.json --dry-run --output=json

$ jira config rollback --journal=recovery/migration.json --yes --no-input --output=json`,
		Flags: func(cmd *cobra.Command) {
			command.AddStringVar(cmd, &journal, "journal", "", "Read this retained migration journal", command.FlagOpts{Terse: "Migration journal", Placeholder: "PATH", Hint: "file"})
			command.AddBoolVar(cmd, &yes, "yes", false, "Confirm restoration without prompting", command.FlagOpts{Terse: "Confirm restoration", Shorthand: "y"})
			command.AddDryRunFlag(cmd)
		},
		Validate:      func() error { return migrationRequired("journal", journal) },
		Human:         renderRollbackHuman,
		RetainOnError: func(out envelope.ConfigRollbackOutput) bool { return out.Receipt != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigRollbackOutput, error) {
			return runRollback(ctx, deps, inv, journal, yes)
		},
	})
}

func runRollback(ctx context.Context, deps Deps, inv command.Invocation, journal string, yes bool) (envelope.ConfigRollbackOutput, error) {
	out := envelope.ConfigRollbackOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Journal: journal}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := migrationHeadless(inv, yes); err != nil {
		return out, err
	}
	transaction, err := cfg.LoadMigrationTransaction(ctx, journal)
	if err != nil {
		return out, &cli.CodedError{Code: cli.CodeConfigRecoveryInvalid, Message: "migration journal or configuration recovery cannot be read and verified", Cause: err}
	}
	receipt := transaction.Receipt()
	out.Journal, out.Receipt = receipt.Journal, &receipt
	preview, err := transaction.PreviewRollback(ctx)
	if err != nil {
		return out, migrationError(err)
	}
	out.WouldRestoreConfiguration, out.CredentialInspectionRequired = preview.WouldRestoreConfiguration, preview.CredentialInspectionRequired
	if inv.DryRun {
		return out, nil
	}
	if err := confirmMigration(ctx, deps, inv, yes, prompt.Confirm{
		Question:    prompt.Question("Restore migration preimages using ", cli.Text(receipt.Journal), "?"),
		Caveat:      "Current credential and configuration postimages must match. Recovery records and provider backups remain retained.",
		Affirmative: "Restore", Negative: "Keep",
	}); err != nil {
		return out, err
	}
	receipt, err = transaction.Rollback(ctx, deps.MigrationStores)
	out.Receipt = &receipt
	out.Restored = err == nil && receipt.Status == "rolled_back" && receipt.JournalDurable
	if err != nil {
		return out, migrationError(err)
	}
	return out, nil
}

func renderRollbackHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigRollbackOutput)
	if !ok {
		return fmt.Errorf("config.rollback: unexpected payload %T", env.Data)
	}
	switch {
	case !env.OK:
		v.Line("rollback refused or incomplete; inspect retained recovery")
	case out.DryRun:
		v.Line("would restore migration preimages using", cli.Text(out.Journal))
		if out.CredentialInspectionRequired {
			v.Line("live rollback still requires credential inspection")
		}
	case out.Restored:
		v.Line("restored migration preimages using", cli.Text(out.Journal))
	default:
		v.Line("rollback incomplete; inspect retained recovery")
	}
	if out.Receipt != nil {
		renderMigrationReceipt(v, *out.Receipt)
	}
	return nil
}
