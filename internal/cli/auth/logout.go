package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newLogout returns `jira auth logout`.
func newLogout(deps Deps) *cobra.Command {
	var assumeYes bool

	return command.MustNew(command.Spec[envelope.AuthLogoutOutput]{
		Op:                "auth.logout",
		MutatesLocalState: true,
		Use:               "logout",
		Short:             "Remove the stored API token",
		Long: "Remove the API token from the selected credential store.\n\n" +
			"For 1Password, remove only the CLI-owned credential field; retain the " +
			"item and all unmanaged fields. Other clients using that field also lose it.\n\n" +
			"This does NOT revoke the token. " +
			"the token keeps working everywhere else until you revoke it at " +
			"id.atlassian.com under Security → API tokens. If the token may " +
			"have leaked, removing it here is not the fix.\n\n" +
			"An environment variable is not touched either — `JIRA_API_TOKEN` " +
			"is set by whatever set it, and this command does not reach into " +
			"your shell.\n\n" +
			"The result reports any nonempty environment credential still available after removal, " +
			"including one exposed by deleting the stored record.\n\n" +
			"It asks before removing anything. `--yes` skips the question, " +
			"and is required when there is no terminal to ask at.",
		Flags: func(c *cobra.Command) {
			command.AddBoolVar(c, &assumeYes, "yes", false, "Do not ask for confirmation",
				command.FlagOpts{Terse: "Skip confirmation", Shorthand: "y"})
			command.AddDryRunFlag(c)
		},
		Human: renderLogoutHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AuthLogoutOutput, error) {
			return runLogout(ctx, deps, inv, assumeYes)
		},
	})
}

func runLogout(ctx context.Context, deps Deps, inv command.Invocation, assumeYes bool) (envelope.AuthLogoutOutput, error) {
	// Resolved once, here, and threaded down. Confirming and removing
	// must name the SAME account — and each resolution opens the
	// credential store, which on a macOS keychain can mean a prompt, so
	// doing it twice asked twice.
	var zero envelope.AuthLogoutOutput

	site, err := deps.Site(ctx)
	if err != nil {
		return zero, err
	}
	account, err := accountToRemove(ctx, deps, site.Email)
	if err != nil {
		return zero, err
	}

	if err := confirmRemoval(ctx, deps, inv, site, account, assumeYes); err != nil {
		if errors.Is(err, errAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled, "logout canceled — the token is still stored")
		}
		return zero, err
	}

	return logout(ctx, deps, site, account, inv.DryRun)
}

// confirmRemoval gates the destructive path.
//
// A preview destroys nothing, so it never asks. Everything else does —
// removing a credential you cannot re-derive is worth one keystroke,
// and the question names the account so it can actually be answered.
//
// A non-interactive caller is never prompted: it must pass --yes, which
// keeps the command scriptable without inventing a silent default that
// deletes things.
func confirmRemoval(ctx context.Context, deps Deps, inv command.Invocation, site SiteInfo, account string, assumeYes bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if inv.DryRun || assumeYes {
		return nil
	}

	if strings.TrimSpace(account) == "" {
		// Nothing to confirm; logout itself reports the missing account.
		return nil
	}

	if !inv.Interactive {
		e := cli.NewInputError(cli.CodeFlagRequired,
			"logout removes a stored credential, so it needs confirmation — pass --yes to confirm without being asked")
		e.Field = "yes"
		return e
	}

	ok, err := confirmLogout(ctx, deps.Confirm, account, site.BaseURL)
	if err != nil {
		return err
	}
	if !ok {
		return errAborted
	}
	return nil
}

// accountToRemove names the account a logout is about: the one in the
// stored RECORD, falling back to configured when nothing readable is
// stored.
//
// The record rather than resolved config, because JIRA_EMAIL outranks
// the keyring: with a work email in the shell and a personal one stored,
// resolved config would have logout name — and confirm — an account it
// did not touch. Both the question and the report read it from here so
// they cannot disagree.
//
// A store that will not open, or a record that will not read, is not
// fatal: logout still has a configured email to name, and its job is to
// leave you logged out either way. Cancellation and deadline errors stop before
// confirmation or deletion rather than falling back to a configured account.
func accountToRemove(ctx context.Context, deps Deps, configured string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	store, err := deps.Credentials()
	var rec credentials.Record
	if err == nil {
		rec, err = credentials.Load(ctx, store)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", err
	}
	if err == nil && rec.Email != "" {
		return rec.Email, nil
	}
	return configured, nil
}

// remainingSource names a credential that will STILL authenticate after
// the stored one is gone, or empty when logout really does log you out.
//
// Removing the keyring record does not remove a token in the
// environment or the config file — those simply become effective. A
// logout that reports success while the next command still works is the
// most misleading outcome this command has, so it is reported.
func remainingSource(ctx context.Context, deps Deps, site SiteInfo) string {
	if site.TokenSource != string(envelope.TokenSourceKeyring) && site.TokenSource != string(envelope.TokenSourceOnePassword) {
		// The effective token already comes from somewhere logout does
		// not touch — the environment, or the config file — so removing
		// the stored record changes nothing about being authenticated.
		// Empty stays empty: no token in effect, nothing to warn about.
		if strings.TrimSpace(site.Token) == "" {
			return ""
		}
		return site.TokenSource
	}
	if deps.SiteWithoutStore == nil {
		// Nothing wired to ask. Silence is the safe answer: claiming
		// nothing remains would be the very over-promise this reports.
		return ""
	}
	// Resolve again with the credential store withheld: whatever answers
	// then is what takes over.
	without, err := deps.SiteWithoutStore(ctx)
	if err != nil || strings.TrimSpace(without.Token) == "" {
		return ""
	}
	return without.TokenSource
}

// logout removes the stored credential.
func logout(ctx context.Context, deps Deps, site SiteInfo, account string, dryRun bool) (envelope.AuthLogoutOutput, error) {
	var zero envelope.AuthLogoutOutput

	if strings.TrimSpace(account) == "" {
		e := cli.NewInputError(cli.CodeConfigInvalid,
			"email is not set, so there is no account to log out of")
		e.Field = "email"
		return zero, e
	}

	if !dryRun {
		store, err := deps.Credentials()
		if err != nil {
			return zero, err
		}
		if err := credentials.Clear(ctx, store); err != nil {
			return zero, err
		}
	}

	// An environment token survives this, and a caller who does not know
	// that will read the next successful command as a failed logout.
	remaining := remainingSource(ctx, deps, site)
	return envelope.AuthLogoutOutput{
		MutationState:    envelope.MutationState{DryRun: dryRun},
		Email:            account,
		EnvTokenRemains:  remaining == string(envelope.TokenSourceEnv),
		RemainingSource:  remaining,
		RevocationNeeded: true,
	}, nil
}

// renderLogoutHuman says what was removed and, when it matters, what was
// not.
func renderLogoutHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AuthLogoutOutput)
	if !ok {
		return fmt.Errorf("auth.logout: unexpected payload %T", env.Data)
	}
	// A preview removed nothing, and saying otherwise is the exact lie
	// --dry-run exists to prevent.
	verb := "removed the stored token for"
	if out.DryRun {
		verb = "would remove the stored token for"
	}
	v.Line(fmt.Sprintf("%s %s", verb, out.Email))
	if out.RemainingSource != "" {
		tense := "is still in effect, so commands will keep working"
		if out.DryRun {
			tense = "would still be in effect, so commands would keep working"
		}
		v.Line(fmt.Sprintf("a token from %s %s", out.RemainingSource, tense))
	}
	v.Line("the token is not revoked — revoke it at id.atlassian.com if it may have leaked")
	return nil
}
