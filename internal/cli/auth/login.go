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

// maxTokenBytes bounds what is read from stdin. An API token is a couple
// of hundred bytes; anything approaching this is a redirected file or a
// pipe that never ends, and reading it whole before noticing would be a
// denial of service against the user's own memory.
const maxTokenBytes = 8 << 10

// renderLoginHuman confirms who you now are, in one line.
//
// It names the ACCOUNT Jira answered with rather than the email that was
// typed: the useful confirmation is that the credential resolved to the
// person you expected, which a typed value cannot tell you.
func renderLoginHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AuthLoginOutput)
	if !ok {
		return fmt.Errorf("auth.login: unexpected payload %T", env.Data)
	}
	headline := "Logged in to"
	if out.DryRun {
		headline = "Token is valid for"
	}
	// The displaced credential cannot be recovered from here, so this row
	// is the only moment anyone finds out; v.Fields skips it when there
	// was nothing to replace.
	fields := writeIdentity(v, headline, out.DisplayName, out.Email, out.AccountID, out.BaseURL)
	fields = append(fields, output.Field{Label: "Replaced the stored credential for", Value: out.Replaced})
	v.Fields(fields...)
	return nil
}

// newLogin returns `jira auth login`.
func newLogin(deps Deps) *cobra.Command {
	var (
		withToken bool
		site      string
		email     string
	)

	return command.MustNew(command.Spec[envelope.AuthLoginOutput]{
		Op:                "auth.login",
		MutatesLocalState: true,
		Use:               "login",
		Short:             "Store an API token for the configured account",
		Long: "Store an API token in the selected credential store.\n\n" +
			"The default is the OS keyring. With secret_backend=1password, the direct " +
			"SDK updates only the owned credential field in the selected item. " +
			"The env backend cannot save tokens. See `jira agent guide configure-credentials`.\n\n" +
			"The token is read from standard input with `--with-token`. There " +
			"is no `--token` flag, and there will not be one: a flag value is " +
			"visible to every process on the machine through `ps`, and it " +
			"lands in shell history.\n\n" +
			"The token is verified against Jira before it is saved, so a typo " +
			"fails here rather than on your next command.\n\n" +
			"The site and account are stored alongside the token, so `--site` " +
			"and `--email` are needed only when nothing is stored yet. On a " +
			"terminal you are asked for whatever is missing, with existing " +
			"values prefilled.\n\n" +
			"`--dry-run` verifies the token and stores nothing, which is how " +
			"to check a credential without committing to it.",
		Example: "# first run, everything on one line\n" +
			"$ op read op://Private/Jira/token | jira auth login \\\n" +
			"    --site https://your-site.atlassian.net --email you@example.com --with-token\n\n" +
			"# interactively, when there is a terminal\n" +
			"$ jira auth login",
		Flags: func(c *cobra.Command) {
			command.AddStringVar(c, &site, "site", "", "Jira site root, like https://your-site.atlassian.net",
				command.FlagOpts{Terse: "Jira site", Placeholder: "url"})
			command.AddStringVar(c, &email, "email", "", "Account the API token belongs to",
				command.FlagOpts{Terse: "Account email", Placeholder: "address"})
			command.AddBoolVar(c, &withToken, "with-token", false, "Read the API token from standard input",
				command.FlagOpts{Terse: "Token from stdin"})
			command.AddDryRunFlag(c)
		},
		Human: renderLoginHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AuthLoginOutput, error) {
			return runLogin(ctx, deps, inv, loginFlags{site: site, email: email, withToken: withToken})
		},
	})
}

// loginFlags is what the caller asked for on the command line.
//
// Only the flags this command declares itself. --dry-run is resolved by
// the path and read off the invocation, so copying it in here as well
// would give one fact two carriers that could disagree.
type loginFlags struct {
	site      string
	email     string
	withToken bool
}

// identity is the site and account a token is being stored for.
type identity struct {
	site  string
	email string
}

func runLogin(ctx context.Context, deps Deps, inv command.Invocation, flags loginFlags) (envelope.AuthLoginOutput, error) {
	var zero envelope.AuthLoginOutput

	ident, token, err := gather(ctx, inv, deps, flags)
	switch {
	case errors.Is(err, errAborted):
		// Canceling is a choice. Reporting it as a failure with a hint
		// to try again is noise for something the person just decided
		// not to do — so it exits quietly, still non-zero so a script
		// can tell nothing was stored.
		return zero, cli.NewCodedError(cli.CodeCanceled, "login canceled — nothing was stored")
	case err != nil:
		return zero, err
	}

	return login(ctx, deps, ident, token, inv.DryRun)
}

// gather resolves the site, account and token from flags, from what is
// stored, and — only on a terminal — by asking.
//
// A non-interactive caller is never prompted. Every agent and every CI
// job lands here, and a command that blocks on input nobody can supply
// is worse than one that fails with the flags to use.
func gather(ctx context.Context, inv command.Invocation, deps Deps, flags loginFlags) (identity, string, error) {
	stored, err := deps.Site(ctx)
	// An independently supplied token can be verified without reading the old
	// credential. Retain public defaults, but never bypass invalid settings or
	// cancellation while resolving them.
	if err != nil && cli.MapError(err).Code == cli.CodeAuthStoreUnavailable && deps.SiteWithoutStore != nil {
		stored, err = deps.SiteWithoutStore(ctx)
	}
	if err != nil {
		return identity{}, "", err
	}
	ident := identity{
		site:  credentials.NormalizeSite(firstNonEmpty(flags.site, stored.BaseURL)),
		email: firstNonEmpty(flags.email, stored.Email),
	}

	// Piped input is an explicit instruction and wins over asking.
	if flags.withToken {
		if missing := missingIdentity(ident); missing != "" {
			e := cli.NewInputError(cli.CodeFlagRequired,
				fmt.Sprintf("--%s is required: nothing is stored yet, so `--with-token` has no site to verify against", missing))
			e.Field = missing
			return identity{}, "", e
		}
		token, tokErr := readToken(ctx, inv)
		return ident, token, tokErr
	}

	if !inv.Interactive {
		e := cli.NewInputError(cli.CodeFlagRequired,
			"pass the token on standard input with --with-token, for example `op read op://Private/Jira/token | jira auth login --with-token`")
		e.Field = "with-token"
		return identity{}, "", e
	}

	return askForLogin(ctx, ident, identity{site: flags.site, email: flags.email})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// login verifies a candidate token and stores it only if Jira accepts.
//
// The order is the whole point. Storing first and checking after would
// leave a broken credential in the keyring on every typo, and the next
// command — not this one — would be the one that failed.
func login(ctx context.Context, deps Deps, ident identity, token string, dryRun bool) (envelope.AuthLoginOutput, error) {
	var zero envelope.AuthLoginOutput

	if missing := missingIdentity(ident); missing != "" {
		e := cli.NewInputError(cli.CodeFlagRequired,
			fmt.Sprintf("--%s is required to verify the token", missing))
		e.Field = missing
		return zero, e
	}

	client, err := deps.ClientFor(ident.site, ident.email, token)
	if err != nil {
		return zero, err
	}
	// The transport already classifies a 401 as an auth failure with its
	// own exit code; wrapping it in a local one would lose that.
	user, err := verify(ctx, client)
	if err != nil {
		return zero, err
	}

	var replaced string

	// A preview still VERIFIES — that is the useful half, and the only
	// way to answer "does this token work?" without committing it. It
	// just does not store.
	if !dryRun {
		store, storeErr := deps.Credentials()
		if storeErr != nil {
			return zero, storeErr
		}
		// There is one record, so logging into a second site REPLACES
		// the first — and the credential it displaces cannot be got back
		// from here. Silently destroying the other tenant's login is the
		// part worth saying out loud.
		//
		// Read from the store already opened for the write, not a second
		// resolution: on a macOS keychain each open can mean a prompt,
		// which is the same fault logout was just fixed for.
		prev, prevErr := credentials.Load(ctx, store)
		if contextErr := ctx.Err(); contextErr != nil {
			return zero, contextErr
		}
		if errors.Is(prevErr, context.Canceled) || errors.Is(prevErr, context.DeadlineExceeded) {
			return zero, prevErr
		}
		if prevErr == nil && prev.Complete() && (prev.Site != ident.site || prev.Email != ident.email) {
			replaced = prev.Email + " on " + prev.Site
		}
		if saveErr := credentials.Save(ctx, store, credentials.Record{
			Site:  ident.site,
			Email: ident.email,
			Token: token,
		}); saveErr != nil {
			return zero, saveErr
		}
	}

	return envelope.AuthLoginOutput{
		MutationState: envelope.MutationState{DryRun: dryRun},
		Replaced:      replaced,
		BaseURL:       ident.site,
		Email:         ident.email,
		AccountID:     user.User.AccountID.Value,
		DisplayName:   user.User.DisplayName.Value,
	}, nil
}

// missingIdentity names the first thing login cannot proceed without,
// as the FLAG that supplies it — this is the one command where the
// answer is something to type on the command line. The name doubles as
// the InputError's field, which is why it comes back bare and the
// callers add the dashes. `auth status` asks the same question of
// resolved configuration and names config keys; see missingSite.
func missingIdentity(ident identity) string {
	switch {
	case ident.site == "":
		return "site"
	case ident.email == "":
		return "email"
	}
	return ""
}

// readToken takes the token from stdin.
//
// Refusing a terminal is deliberate: `jira auth login --with-token` with
// nothing piped would sit waiting for input with no prompt, which reads
// as a hang. Saying so is better than looking broken.
func readToken(ctx context.Context, inv command.Invocation) (string, error) {
	if inv.StdinIsTerminal {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			"--with-token expects the token on standard input; pipe it in, for example `op read op://Private/Jira/token | jira auth login --with-token`")
		e.Field = "with-token"
		return "", e
	}

	raw, err := command.ReadStdinContext(ctx, "with-token", inv.Stdin, maxTokenBytes+1)
	if err != nil {
		return "", fmt.Errorf("read token from standard input: %w", err)
	}
	if len(raw) > maxTokenBytes {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			"the input on standard input is too large to be an API token")
		e.Field = "with-token"
		return "", e
	}
	// Trim: `op read` and most files end with a newline, and a token
	// with a trailing newline fails authentication in a way that looks
	// like a bad credential rather than a bad read.
	token := strings.TrimSpace(string(raw))
	if token == "" {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, "no token arrived on standard input")
		e.Field = "with-token"
		return "", e
	}
	if strings.ContainsAny(token, "\r\n") {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			"the input on standard input spans multiple lines, so it is not a single API token")
		e.Field = "with-token"
		return "", e
	}
	return token, nil
}
