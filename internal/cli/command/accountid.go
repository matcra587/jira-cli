package command

import (
	"context"
	"fmt"
	"strings"

	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// MeSentinel is the value every account-id flag accepts to mean "whoever
// these credentials authenticate as".
//
// One spelling, shared, because the alternative is each command inventing
// its own: the design's §6 drops `issue watch`/`issue unwatch` on the
// grounds that `watchers add --user @me` covers them, which only holds if
// `@me` means the same thing on `--user`, `--assignee` and `--reporter`
// alike.
//
// The `@` prefix is what keeps it unambiguous. An Atlassian account id is
// an opaque alphanumeric string (`5b10a2844c20165700ede21g`), and no real
// one begins with `@`, so the sentinel cannot collide with a value a
// caller meant literally.
const MeSentinel = "@me"

// AccountField is one account-id flag: the name to blame in a message, and
// a pointer to the value so a resolver can rewrite it in place.
//
// A pointer rather than a returned copy because resolution has to reach
// the SAME variable the flag bound and the payload composer will read.
// Handing back a new string would leave every caller responsible for
// writing it to the right field, which is exactly the per-command
// bookkeeping this shared resolver exists to remove.
type AccountField struct {
	Flag  string
	Value *string
}

// AccountFields returns this bundle's account-id flags, whether or not
// they were given — MeFields below is what narrows to the ones needing
// work.
//
// `issue watchers add/remove` is the other caller this API is shaped for:
// its `--user` is a single flag rather than a bundle, so it builds a
// one-element slice by hand and calls exactly the same three functions.
// That is the whole reason resolution takes []AccountField rather than an
// *IssueFieldFlags.
func (f *IssueFieldFlags) AccountFields() []AccountField {
	return []AccountField{
		{Flag: FlagAssignee, Value: &f.Assignee},
		{Flag: FlagReporter, Value: &f.Reporter},
	}
}

// MeFields narrows to the fields actually holding the sentinel, preserving
// order so a message naming several reads the same on every run.
func MeFields(fields []AccountField) []AccountField {
	return xslices.Filter(fields, func(a AccountField) bool { return *a.Value == MeSentinel })
}

// ResolveMe rewrites every @me among fields to the authenticated account
// id, with ONE call to the site however many fields hold the sentinel.
//
// One call is the point: `--assignee @me --reporter @me` is a reasonable
// invocation and must not cost two identical round trips. It is also why
// this takes the whole slice rather than being a per-value helper a caller
// loops over — a shape that made the duplicate request the default.
//
// It is only ever reached when a sentinel is present (callers gate on
// MeFields), so it never spends a request on a command that did not ask
// for one. A bare `jira issue edit KEY --summary x` must not pay for a
// /myself lookup it has no use for.
func ResolveMe(ctx context.Context, client core.JiraClient, fields []AccountField) error {
	pending := MeFields(fields)
	if len(pending) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	read, err := Spin(ctx, "Resolving "+MeSentinel, func(ctx context.Context) (core.UserRead, error) {
		return client.CurrentUser(ctx)
	})
	if err != nil {
		return fmt.Errorf("resolving %s: %w", MeSentinel, err)
	}
	if err := core.RequireCurrentUserIdentity(read.User); err != nil {
		// CurrentUser's contract is a non-nil User alongside a nil error,
		// and an account with no id is not a thing Jira has. Failing loudly
		// rather than trusting it silently matters more here than in most
		// guards: an empty id would compose `{"accountId": ""}`, which Jira
		// accepts on some fields as "unassigned" — so a silent miss would
		// not error, it would quietly assign the issue to nobody.
		return fmt.Errorf("resolving %s: the site returned no account id for the current user: %w", MeSentinel, err)
	}
	for _, field := range pending {
		*field.Value = read.User.AccountID.Value
	}
	return nil
}

// ResolveAccounts settles every `@me` for one run and decides — once, for
// every command that takes an account id — what a run that cannot resolve
// it does instead.
//
// The three branches mirror `issue transition`'s screen-state read, which
// is deliberate: that command already split "local preview" from "preview
// that may read the site", and a second shape for the same question would
// leave callers guessing which commands pay for a round trip.
//
//   - No sentinel anywhere: nothing happens and no client is built, so a
//     command that never mentions @me pays nothing.
//   - Local preview (--dry-run without --validate-remote): public preferences
//     may supply a matching cached ID with an unverified warning. Otherwise
//     @me stays and an unresolved warning says so. No client is constructed.
//   - Live run, or a preview with --validate-remote: resolved for real.
//
// **It takes the client CONSTRUCTOR, not a client**, which is what keeps
// the first branch honest: a caller that handed over an already-built
// client would have paid for credential resolution before this function
// could decide the run needs none. It returns the client if it built one,
// so the caller does not construct a second — deps.JiraClient is a
// constructor rather than a singleton accessor.
//
// This is the whole policy, not just its pieces. It lives here rather than
// in one command because the second adopter proved the point: `watchers
// add/remove` needed the identical three branches, and had this returned
// only the primitives, that command would have grown its own copy of the
// decision and the two could have drifted.
func ResolveAccounts(
	ctx context.Context,
	newClient func(ctx context.Context) (core.JiraClient, error),
	preferences func() (core.IssuePreferences, error),
	dryRun, validateRemote bool,
	fields []AccountField,
) (core.JiraClient, error) {
	pending := MeFields(fields)
	if len(pending) == 0 {
		// No account lookup needs a client. Live callers construct theirs
		// only after payload validation and any confirmation have passed.
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if dryRun && !validateRemote {
		if preferences != nil {
			local, err := preferences()
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			if err != nil {
				return nil, err
			}
			if local.CachedAccountID != "" {
				if strings.TrimSpace(local.CachedAccountID) == "" {
					return nil, cli.NewCodedError(cli.CodeConfigInvalid, "cached account_id must be empty or nonblank")
				}
				for _, field := range pending {
					*field.Value = local.CachedAccountID
				}
				cli.AddWarning(envelope.Warning{
					Type:    "account_id_cached",
					Message: "This preview uses a configured cached account_id for @me; it is unverified. A live run or --validate-remote reads the current account from Jira.",
				})
				return nil, nil
			}
		}
		// Warned even though nothing failed: a preview holding an
		// unresolved sentinel is showing a payload that differs from the
		// live one. cli.AddWarning is the route a Spec.Run handler has;
		// its own doc comment records why.
		cli.AddWarning(MeUnresolvedWarning(fields))
		return nil, nil
	}

	client, err := newClient(ctx)
	if err != nil {
		return nil, err
	}
	if err := ResolveMe(ctx, client, fields); err != nil {
		return nil, err
	}
	return client, nil
}

// MeUnresolvedWarning describes a local preview without a matching configured
// cached identity. It retains @me rather than inventing an authenticated account.
// Live and --validate-remote resolution always read and validate Jira's reply.
func MeUnresolvedWarning(fields []AccountField) envelope.Warning {
	pending := MeFields(fields)
	if len(pending) == 0 {
		return envelope.Warning{}
	}
	flags := xslices.Map(pending, func(a AccountField) string { return "--" + a.Flag })
	return envelope.Warning{
		Type: "account_id_unresolved",
		Message: fmt.Sprintf(
			"%s left as %s in this preview: resolving it reads the current user from the site, which --dry-run never does. A live run resolves it; add --%s to resolve it here.",
			joinFlags(flags), MeSentinel, FlagValidateRemote,
		),
		// Nothing was dropped — the value is present and will resolve when
		// sent — so this is a heads-up rather than a loss. Lossy is
		// reserved for a warning that says data went missing.
		Lossy: false,
	}
}

// RefuseMeWithoutResolution is the guard for a path that composed a
// payload still holding the sentinel and is about to SEND it.
//
// It should be unreachable: every live path resolves first. It exists
// because the failure it catches is silent and expensive — Jira would
// accept `{"accountId": "@me"}` as an ordinary bad value and answer 400,
// or worse accept it as a literal username on some field, and the caller
// would be left debugging a CLI sentinel that leaked to the server.
func RefuseMeWithoutResolution(fields []AccountField) error {
	pending := MeFields(fields)
	if len(pending) == 0 {
		return nil
	}
	return fmt.Errorf(
		"command: %s still holds %s on a live path; it must be resolved before the request is composed",
		pending[0].Flag, MeSentinel,
	)
}

// joinFlags renders a flag list for one sentence of prose.
func joinFlags(flags []string) string {
	switch len(flags) {
	case 0:
		return ""
	case 1:
		return flags[0]
	}
	return joinAllButLast(flags) + " and " + flags[len(flags)-1]
}

func joinAllButLast(flags []string) string {
	out := ""
	for i, f := range flags[:len(flags)-1] {
		if i > 0 {
			out += ", "
		}
		out += f
	}
	return out
}

// MeHelp is the sentence every account-id flag's help ends with, so the
// sentinel is discoverable from `--help` and from the agent schema rather
// than only from documentation. Spelled once for the reason the flag names
// are: two flags describing the same sentinel differently is how one of
// them comes to be believed.
var MeHelp = fmt.Sprintf(", or %s for yourself; local previews may use a matching cached account_id (unverified)", MeSentinel)
