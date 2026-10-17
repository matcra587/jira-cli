package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
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
	headline := "Verified candidate for"
	switch {
	case out.DryRun:
		headline = "Token is valid for"
	case out.Credential.Saved && out.Settings.Saved:
		headline = "Logged in to"
	case out.Credential.Saved:
		headline = "Credential saved for"
	case out.Credential.Status == "external" && out.Settings.Saved:
		headline = "Verified environment credential for"
	}
	// The displaced credential cannot be recovered from here, so this row
	// is the only moment anyone finds out; v.Fields skips it when there
	// was nothing to replace.
	fields := writeIdentity(v, headline, out.DisplayName, out.Email, out.AccountID, out.BaseURL)
	fields = append(fields, output.Field{Label: "Cloud ID", Value: out.CloudID})
	fields = append(fields, output.Field{Label: "Replaced the stored credential for", Value: out.Replaced})
	fields = append(fields,
		output.Field{Label: "Credential publication", Value: cli.Text(out.Credential.Status)},
		output.Field{Label: "Credential provider", Value: cli.Text(string(out.Credential.Backend))},
		output.Field{Label: "Environment reference", Value: cli.Text(out.Credential.Reference)},
		output.Field{Label: "Settings publication", Value: cli.Text(out.Settings.Status)},
		output.Field{Label: "Configuration", Value: cli.Text(out.Settings.Path)},
	)
	if out.LocatorPublication != nil {
		fields = append(fields,
			output.Field{Label: "Credential reference publication", Value: cli.Text(out.LocatorPublication.Status)},
			output.Field{Label: "Credential reference configuration", Value: cli.Text(out.LocatorPublication.Path)},
		)
	}
	v.Fields(fields...)
	if out.DryRun && out.Settings.Status == "prepared" {
		if out.Settings.WouldChange {
			v.Line("would save verified login settings")
		} else {
			v.Line("would keep verified login settings unchanged")
		}
	}
	return nil
}

// newLogin returns `jira auth login`.
func newLogin(deps Deps) *cobra.Command {
	var (
		withToken bool
		site      string
		email     string
		cloud     command.OptionalString
	)

	return command.MustNew(command.Spec[envelope.AuthLoginOutput]{
		Op:                "auth.login",
		MutatesLocalState: true,
		Use:               "login",
		Short:             "Store an API token for the configured account",
		Long: "Store an API token in the selected credential store.\n\n" +
			"The default is the OS keyring. With secret_backend=1password, the direct " +
			"SDK updates only the owned credential field in the selected item. " +
			"A missing item is created as API Credentials with a Jira CLI section and jira-cli tag; existing item layout stays intact. " +
			"The env backend verifies its selected environment token and saves public metadata only. " +
			"Run it without `--with-token`; no token is stored. See `jira agent guide configure-credentials`.\n\n" +
			"The token is read from standard input with `--with-token`. There " +
			"is no `--token` flag, and there will not be one: a flag value is " +
			"visible to every process on the machine through `ps`, and it " +
			"lands in shell history.\n\n" +
			"The token is verified against Jira before it is saved, so a typo " +
			"fails here rather than on your next command. Classic site routing is " +
			"verified first; only a 401/403 refusal can trigger public cloud-ID " +
			"discovery and verification through the fixed Atlassian gateway. " +
			"Discovery sends no token or cookies and follows no redirects. " +
			"Use `--cloud-id=ID` for a known gateway, or `--cloud-id=` to force " +
			"classic routing. If authored `cloud_id` differs from the verified " +
			"scope, live login refuses before saving: repair that setting and " +
			"repeat login. Previews can verify without changing either.\n\n" +
			"The site and account are stored alongside the token, so `--site` " +
			"and `--email` are needed only when nothing is stored yet. On a " +
			"terminal you are asked for whatever is missing, with existing " +
			"values prefilled.\n\n" +
			"Verified login prepares public site/account/cloud, provider selection and independently bound " +
			"account_id metadata before credential publication. Conflicting authored or effective scope is refused " +
			"before storing; repair the selected settings or environment, then repeat login. " +
			"Settings snapshots refuse intervening edits and create missing targets without clobbering.\n\n" +
			"`--dry-run` verifies the token and stores nothing. It can check a proposed scope while " +
			"reporting settings.status=blocked when live publication would conflict. An independent stdin " +
			"preview is also allowed with the env backend.\n\n" +
			"Credential and settings receipts retain actual effects on failure. If the credential was saved " +
			"but settings publication failed, inspect the selected configuration and use `auth whoami --save` " +
			"after repairing its scope. A credential.status=unknown or settings.status=unknown requires inspection " +
			"before retrying; login never automatically replays or restores a failed write.",
		Example: "# first run, everything on one line\n" +
			"$ op read op://Private/Jira/token | jira auth login \\\n" +
			"    --site https://your-site.atlassian.net --email you@example.com --with-token\n\n" +
			"# interactively, when there is a terminal\n" +
			"$ jira auth login",
		Flags: func(c *cobra.Command) {
			command.AddOptionalStringVar(c, &cloud, "cloud-id", "Verify an explicit scoped-token cloud ID; an empty value forces classic routing",
				command.FlagOpts{Terse: "Cloud scope", Placeholder: "cloud-id"})
			command.AddStringVar(c, &site, "site", "", "Jira site root, like https://your-site.atlassian.net",
				command.FlagOpts{Terse: "Jira site", Placeholder: "url"})
			command.AddStringVar(c, &email, "email", "", "Account the API token belongs to",
				command.FlagOpts{Terse: "Account email", Placeholder: "address"})
			command.AddBoolVar(c, &withToken, "with-token", false, "Read the API token from standard input",
				command.FlagOpts{Terse: "Token from stdin"})
			command.AddDryRunFlag(c)
		},
		Human:         renderLoginHuman,
		RetainOnError: func(out envelope.AuthLoginOutput) bool { return out.Verified },
		Warnings:      loginWarnings,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.AuthLoginOutput, error) {
			cloudID, cloudSet := cloud.Value()
			return runLogin(ctx, deps, inv, loginFlags{site: site, email: email, withToken: withToken, cloudID: cloudID, cloudSet: cloudSet})
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
	cloudID   string
	cloudSet  bool
}

// identity is the site and account a token is being stored for.
type identity struct {
	site     string
	email    string
	cloudID  string
	cloudSet bool
	location core.CredentialLocation
	external bool
}

func runLogin(ctx context.Context, deps Deps, inv command.Invocation, flags loginFlags) (envelope.AuthLoginOutput, error) {
	var zero envelope.AuthLoginOutput
	if deps.ClientForSave != nil {
		deps.ClientFor = deps.ClientForSave
	}

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
	if err := ctx.Err(); err != nil {
		return identity{}, "", err
	}
	if flags.cloudSet {
		if err := core.ValidateCloudID(flags.cloudID); err != nil {
			input := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			input.Field = "cloud-id"
			return identity{}, "", input
		}
	}
	var stored SiteInfo
	var err error
	if deps.SiteForLogin != nil {
		stored, err = deps.SiteForLogin(ctx, LoginDefaults{Site: strings.TrimSpace(flags.site) == "", Email: strings.TrimSpace(flags.email) == ""})
	} else {
		stored, err = deps.Site(ctx)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return identity{}, "", contextErr
	}
	// An independently supplied token can be verified without reading the old
	// credential. Retain public defaults, but never bypass invalid settings or
	// cancellation while resolving them.
	if err != nil && cli.MapError(err).Code == cli.CodeAuthStoreUnavailable {
		if deps.SiteForLogin != nil {
			stored, err = deps.SiteForLogin(ctx, LoginDefaults{})
		} else if deps.SiteWithoutStore != nil {
			stored, err = deps.SiteWithoutStore(ctx)
		}
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return identity{}, "", contextErr
	}
	if err != nil {
		return identity{}, "", err
	}
	ident := identity{
		site:    credentials.NormalizeSite(firstNonEmpty(flags.site, stored.BaseURL)),
		email:   firstNonEmpty(flags.email, stored.Email),
		cloudID: strings.TrimSpace(flags.cloudID), cloudSet: flags.cloudSet,
		location: stored.Location,
	}

	// Piped input is an explicit instruction and wins over asking.
	if flags.withToken {
		if stored.Location.Backend == core.CredentialBackendEnvironment && !inv.DryRun {
			input := cli.NewInputError(cli.CodeFlagValueInvalid, "the environment backend cannot store a supplied token; configure JIRA_API_TOKEN or api_token_env and run login without --with-token")
			input.Field = "with-token"
			return identity{}, "", input
		}
		if missing := missingIdentity(ident); missing != "" {
			e := cli.NewInputError(cli.CodeFlagRequired,
				fmt.Sprintf("--%s is required: nothing is stored yet, so `--with-token` has no site to verify against", missing))
			e.Field = missing
			return identity{}, "", e
		}
		token, tokErr := readToken(ctx, inv)
		return ident, token, tokErr
	}
	if stored.Location.Backend == core.CredentialBackendEnvironment {
		if missing := missingIdentity(ident); missing != "" {
			input := cli.NewInputError(cli.CodeFlagRequired, "the environment backend needs public site/account metadata before verification")
			input.Field = missing
			return identity{}, "", input
		}
		if stored.TokenSource != string(envelope.TokenSourceEnv) || strings.TrimSpace(stored.Token) == "" {
			return identity{}, "", cli.NewCodedError(cli.CodeAuthMissing, "the environment backend has no selected token; configure JIRA_API_TOKEN or api_token_env before login")
		}
		ident.external = true
		return ident, stored.Token, nil
	}

	if !inv.Interactive {
		e := cli.NewInputError(cli.CodeFlagRequired,
			"pass the token on standard input with --with-token, for example `op read op://Private/Jira/token | jira auth login --with-token`")
		e.Field = "with-token"
		return identity{}, "", e
	}

	return askForLogin(ctx, ident, identity{site: flags.site, email: flags.email, cloudID: ident.cloudID, cloudSet: ident.cloudSet})
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

	user, cloudID, err := verifyLogin(ctx, deps, ident, token)
	if err != nil {
		return zero, err
	}

	return publishLogin(ctx, deps, ident, token, dryRun, cloudID, user)
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
