package issue

import (
	"context"
	"fmt"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// flagUser names the account a watcher verb acts on. Canonical and
// unaliased (design §5) — acli spells it the same way, so there is no
// foreign spelling for a hidden alias to accept.
const (
	flagUser      = "user"
	flagUserQuery = "user-query"
)

const watcherIDValidationHelp = "Explicit --validate-remote checks a literal --user through one read-only exact-account lookup before any issue request. " +
	"The reply must name that account and be active, not deleted. Unknown activity, malformed replies or a different ID refuse. " +
	"Surrounding input whitespace is trimmed for this check. One lookup serves every issue; it does not prove issue existence or watcher permission. " +
	"Bare previews and ordinary literal writes perform no account lookup.\n\n"

const watcherQueryHelp = "Use --user-query to look up an account by name or email instead of giving --user. " +
	"The flags are mutually exclusive. Live execution and --validate-remote resolve one complete, " +
	"unique visible active query match before any issue request. Missing, ambiguous, malformed or " +
	"incomplete searches refuse. A match does not prove exact name/email identity or watcher permissions. " +
	"One lookup is shared by every issue.\n\n" +
	"Bare previews preserve data.user_query and warn that it is unresolved; data.account_id is empty. " +
	"They open no Jira client or cached preferences for this query. Explicit remote previews resolve " +
	"the query without writing or checking issue existence.\n\n"

// Both watcher verbs live in one file, and their executor is shared,
// which is a deliberate departure from the one-command-per-file shape
// the rest of this package takes.
//
// The two differ in exactly three places: which seam method they call,
// the word in their prose, and the operation name on the envelope.
// Everything else — the keyed fan-out, the argv ordering, the readback,
// the partial-failure rule, the preview — is the same code, and writing
// it twice would mean two places to fix the next time the keyed write
// contract moves. That is the "second implementation" failure this
// codebase reliably produces, and the answer to it here is one
// watcherVerb value passed to one executor.
//
// The line NOT crossed: the two are still separate cobra commands with
// separate Specs, separate help, and separate registered Output types.
// Sharing the executor is sharing an implementation; sharing the
// declaration would be sharing a contract, and the schema an agent reads
// has to describe "add" and "remove" as the different acts they are.

// watcherVerb is everything one watcher verb needs that the other does
// not share: what to call, and what to call it.
//
// A value rather than an interface. There are exactly two of these and
// there will never be a third — the endpoint has two directions — so a
// struct of fields is the honest shape, and an interface would invite a
// reader to look for the implementations that do not exist.
type watcherVerb struct {
	// write is the seam call — AddWatcher or RemoveWatcher.
	write func(ctx context.Context, client core.JiraClient, key, accountID string) error
	// bare, gerund and past are the three forms this verb's prose needs:
	// "Would add", "Adding", "watcher added". Three spelled-out fields
	// rather than one word conjugated at the call site, because English
	// does not conjugate reliably enough for a helper to do it — "add"
	// doubles no letter and "remove" drops one — and a verb form invented
	// at render time is a sentence nobody reviewed.
	bare        string
	gerund      string
	past        string
	preposition string
}

// The operation name is deliberately NOT a field here. Each Spec spells
// its own Op as a string literal, because the H1 source guard
// (tests/guardrails/human_view_primitives.go) resolves a command's Op by
// reading that literal out of the Spec and cannot follow an identifier —
// an op it cannot name is a human renderer whose verdict it cannot
// attribute, which it correctly refuses to accept silently. Two literals
// in two Specs beats a shared field that makes the guard blind.
var (
	watcherAdd = watcherVerb{
		write: func(ctx context.Context, client core.JiraClient, key, accountID string) error {
			_, err := client.AddWatcher(ctx, key, accountID)
			return err
		},
		bare:        "add",
		gerund:      "Adding",
		past:        "added",
		preposition: "to",
	}
	watcherRemove = watcherVerb{
		write: func(ctx context.Context, client core.JiraClient, key, accountID string) error {
			_, err := client.RemoveWatcher(ctx, key, accountID)
			return err
		},
		bare:        "remove",
		gerund:      "Removing",
		past:        "removed",
		preposition: "from",
	}
)

// newWatchersAdd returns `jira issue watchers add`.
func newWatchersAdd(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var mut command.MutationFlags
	var user, userQuery string
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWatchersAddOutput]{
		Op:         "issue.watchers.add",
		WritesJira: true,
		Identity:   true,
		Keyed:      true,
		Use:        "add ISSUE_KEY...",
		Short:      "Add a watcher to issues",
		Long: "Make one account watch one or more issues.\n\n" +
			"`--user` takes an Atlassian account id, which is what the API " +
			"accepts and what `jira issue watchers list` prints in its first " +
			"column — that command is how you find the id, and how you check who " +
			"a change is about to start notifying.\n\n" +
			watcherIDValidationHelp +
			watcherQueryHelp +
			"Adding an account that already watches the issue SUCCEEDS rather " +
			"than erroring. The verb names an end state, so re-running it leaves " +
			"you in the state you asked for; it does not tell you which of the " +
			"two happened, because Jira does not say.\n\n" +
			"This is a keyed command: results come back as one entry per key, in " +
			"the order you gave them, at `data.results[]`. A key that fails does " +
			"not fail the others, and that matters more on a write than on a " +
			"read — the issues that DID get the watcher are really watched, and " +
			"the envelope names exactly which so you can retry only the rest. " +
			"Only a run where EVERY key failed is reported as a failure of the " +
			"command itself.\n\n" +
			"Total failure, cancellation and timeout retain ordered per-key " +
			"outcomes and the resolved account in the full failure frame.\n\n" +
			"**Each key costs two requests by default.** Jira answers the write " +
			"itself with an empty 204, so reporting the resulting watcher set " +
			"means reading it back — which this command does, publishing it at " +
			"`data.results[].data.readback`. Pass `--no-readback` to skip that " +
			"second request and halve the traffic; the write still happens, and " +
			"the entry simply carries no readback.\n\n" +
			"A failed readback keeps the acknowledged write successful and " +
			"reports `data.results[].data.readback_error`. Inspect watcher " +
			"state rather than repeating the write to recover that read.\n\n" +
			"Bare `--dry-run` composes the preview and stops. It never contacts Jira, " +
			"so it reports the account that WOULD be added but cannot tell you " +
			"whether the issues exist, whether the account does, or whether it is " +
			"already watching.\n\n" +
			"There is no `--force`. Adding a watcher is reversible with `jira " +
			"issue watchers remove`, so there is no destructive step to confirm.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# start watching one issue, as a given account\n" +
			"$ jira issue watchers add KEY-123 --user 5b10a2844c20165700ede21g\n\n" +
			"# watch it yourself — the watch/unwatch case\n" +
			"$ jira issue watchers add KEY-123 --user @me\n\n" +
			"# the same account on several issues\n" +
			"$ jira issue watchers add KEY-123 KEY-124 KEY-125 --user 5b10a2844c20165700ede21g\n\n" +
			"# resolve a watcher by visible name or email query\n" +
			"$ jira issue watchers add KEY-123 --user-query=ada@example.com\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue watchers add KEY-123 --user 5b10a2844c20165700ede21g --dry-run\n\n" +
			"# skip the readback, for one request per key instead of two\n" +
			"$ jira issue watchers add KEY-123 --user 5b10a2844c20165700ede21g --no-readback\n\n" +
			"# just the keys that took it, as json\n" +
			"$ jira issue watchers add KEY-123 KEY-124 --user 5b10a2844c20165700ede21g --output=json --jq '.data.results[] | select(.ok) | .issue.key'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			addWatcherWriteFlags(c, &keyed, &mut, &user, &userQuery)
		},
		Validate:      func() error { return validateWatcherWrite(cmd, keyed, user, userQuery, mut.ValidateRemote) },
		Human:         renderWatchersAdd,
		RetainOnError: func(out envelope.IssueWatchersAddOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWatchersAddOutput, error) {
			// BEFORE the envelope is built, so data.account_id publishes what
			// was actually sent rather than the sentinel that was typed.
			client, err := resolveWatcherTarget(ctx, deps, inv.DryRun, mut.ValidateRemote, &user, userQuery)
			if err != nil {
				return envelope.IssueWatchersAddOutput{}, err
			}
			out := envelope.IssueWatchersAddOutput{
				MutationState: envelope.MutationState{DryRun: inv.DryRun},
				AccountID:     user,
				UserQuery:     userQuery,
			}
			out.Results, err = runWatcherWrite(ctx, deps, inv, watcherAdd, watcherWriteOptions{
				client:      client,
				accountID:   user,
				noReadback:  mut.NoReadback,
				parallelism: keyed.Parallelism,
			})
			return out, err
		},
	})
}

// newWatchersRemove returns `jira issue watchers remove`.
func newWatchersRemove(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var mut command.MutationFlags
	var user, userQuery string
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWatchersRemoveOutput]{
		Op:         "issue.watchers.remove",
		WritesJira: true,
		Identity:   true,
		Keyed:      true,
		Use:        "remove ISSUE_KEY...",
		Short:      "Remove a watcher from issues",
		Long: "Stop one account watching one or more issues.\n\n" +
			"`--user` takes an Atlassian account id, which is what the API " +
			"accepts and what `jira issue watchers list` prints in its first " +
			"column — that command is how you find the id, and how you check who " +
			"is watching before you change it. It also takes `@me`, which " +
			"resolves to whoever your credentials authenticate as: that is the " +
			"watch/unwatch case, and it is why this CLI has no separate `watch` " +
			"verb. Live resolution reads the current user from Jira. A local " +
			"preview uses matching cached account_id metadata with an unverified " +
			"warning, or leaves @me unresolved. `--validate-remote` reads Jira instead.\n\n" +
			watcherIDValidationHelp +
			watcherQueryHelp +
			"Removing an account that was not watching SUCCEEDS rather than " +
			"erroring. The verb names an end state, so re-running it leaves you " +
			"in the state you asked for.\n\n" +
			"This is not treated as a destructive command and takes no `--force`. " +
			"Nothing is lost that `jira issue watchers add` cannot put back — " +
			"unlike a deleted comment, a removed watcher is a subscription, not " +
			"content.\n\n" +
			"This is a keyed command: results come back as one entry per key, in " +
			"the order you gave them, at `data.results[]`. A key that fails does " +
			"not fail the others, and the envelope names exactly which keys the " +
			"account was removed from so you can retry only the rest. Only a run " +
			"where EVERY key failed is reported as a failure of the command " +
			"itself.\n\n" +
			"Total failure, cancellation and timeout retain ordered per-key " +
			"outcomes and the resolved account in the full failure frame.\n\n" +
			"**Each key costs two requests by default.** Jira answers the write " +
			"itself with an empty 204, so reporting the resulting watcher set " +
			"means reading it back — which this command does, publishing it at " +
			"`data.results[].data.readback`. Pass `--no-readback` to skip that " +
			"second request; the write still happens.\n\n" +
			"A failed readback keeps the acknowledged write successful and " +
			"reports `data.results[].data.readback_error`. Inspect watcher " +
			"state rather than repeating the write to recover that read.\n\n" +
			"Bare `--dry-run` composes the preview and stops. It never contacts Jira, " +
			"so it reports the account that WOULD be removed but cannot tell you " +
			"whether the issues exist or whether the account was watching.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# stop an account watching one issue\n" +
			"$ jira issue watchers remove KEY-123 --user 5b10a2844c20165700ede21g\n\n" +
			"# resolve a watcher by visible name or email query\n" +
			"$ jira issue watchers remove KEY-123 --user-query=ada@example.com\n\n" +
			"# stop watching it yourself\n" +
			"$ jira issue watchers remove KEY-123 --user @me\n\n" +
			"# the same account across several issues\n" +
			"$ jira issue watchers remove KEY-123 KEY-124 KEY-125 --user 5b10a2844c20165700ede21g\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue watchers remove KEY-123 --user 5b10a2844c20165700ede21g --dry-run\n\n" +
			"# skip the readback, for one request per key instead of two\n" +
			"$ jira issue watchers remove KEY-123 --user 5b10a2844c20165700ede21g --no-readback\n\n" +
			"# who is left watching, as json\n" +
			"$ jira issue watchers remove KEY-123 --user 5b10a2844c20165700ede21g --output=json --jq '.data.results[].data.readback.watchers[].display_name'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			addWatcherWriteFlags(c, &keyed, &mut, &user, &userQuery)
		},
		Validate:      func() error { return validateWatcherWrite(cmd, keyed, user, userQuery, mut.ValidateRemote) },
		Human:         renderWatchersRemove,
		RetainOnError: func(out envelope.IssueWatchersRemoveOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWatchersRemoveOutput, error) {
			// BEFORE the envelope is built, so data.account_id publishes what
			// was actually sent rather than the sentinel that was typed.
			client, err := resolveWatcherTarget(ctx, deps, inv.DryRun, mut.ValidateRemote, &user, userQuery)
			if err != nil {
				return envelope.IssueWatchersRemoveOutput{}, err
			}
			out := envelope.IssueWatchersRemoveOutput{
				MutationState: envelope.MutationState{DryRun: inv.DryRun},
				AccountID:     user,
				UserQuery:     userQuery,
			}
			out.Results, err = runWatcherWrite(ctx, deps, inv, watcherRemove, watcherWriteOptions{
				client:      client,
				accountID:   user,
				noReadback:  mut.NoReadback,
				parallelism: keyed.Parallelism,
			})
			return out, err
		},
	})
}

// addWatcherWriteFlags registers the surface both verbs share.
//
// WithoutReadback and nothing else, which is exactly what
// command.MutationOption's own footprint table predicts for these two:
// --force has nothing destructive to confirm, and --verify would gate a
// readback this command already does by default.
//
// --validate-remote permits shared @me/query lookup and opts literal IDs into
// exact-account validation. No account lookup establishes watcher permissions
// or issue existence. Bare previews and ordinary literal writes remain lazy.
func addWatcherWriteFlags(cmd *cobra.Command, keyed *command.KeyedFlags, mut *command.MutationFlags, user, userQuery *string) {
	command.AddKeyedFlags(cmd, keyed)
	command.AddMutationFlags(cmd, mut, command.WithoutReadback(), command.WithValidateRemote())
	command.AddStringVar(cmd, user, flagUser, "",
		"The Atlassian account id to add or remove, as `watchers list` prints it"+command.MeHelp,
		command.FlagOpts{Terse: "Account id", Placeholder: "account-id"})
	command.AddStringVar(cmd, userQuery, flagUserQuery, "", "Resolve one visible active user by name or email query; conflicts with --user", command.FlagOpts{Terse: "User query", Placeholder: "query"})
}

// validateWatcherWrite runs the refusals both verbs share, before any
// I/O happens.
func validateWatcherWrite(cmd *cobra.Command, keyed command.KeyedFlags, user, userQuery string, validateRemote bool) error {
	if err := keyed.Validate(); err != nil {
		return err
	}
	if cmd.Flags().Changed(flagUser) && cmd.Flags().Changed(flagUserQuery) {
		err := cli.NewInputError(cli.CodeFlagConflict, "--user and --user-query are mutually exclusive")
		err.Field = flagUserQuery
		return err
	}
	if cmd.Flags().Changed(flagUserQuery) {
		if err := command.ValidateAccountLookups([]command.AccountField{{Flag: flagUserQuery, Value: &userQuery, LookupQuery: true}}); err != nil {
			return err
		}
	} else {
		if err := requireUser(user); err != nil {
			return err
		}
		if err := command.ValidateAccountLookups([]command.AccountField{{Flag: flagUser, Value: &user, ValidateRemoteID: validateRemote}}); err != nil {
			return err
		}
	}
	return refuseWebOnKeyedWrite(cmd)
}

// requireUser refuses an invocation naming no account.
//
// Refused HERE rather than through cobra's MarkFlagRequired, so the
// failure carries this CLI's own taxonomy code and field rather than
// pflag's prose — the same reason every other refusal in this package is
// a cli.InputError. It is also refused before any client is built, which
// is what Spec.Validate is for: a request that cannot succeed never
// reaches Jira and never pays for a connection.
func requireUser(user string) error {
	if user != "" {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
		"no account named — say whose watch to change with --%s, taking the id from `jira issue watchers list KEY`",
		flagUser,
	))
	e.Field = flagUser
	return e
}

// resolveWatcherUser settles @me and explicit literal-ID preflight on --user
// before anything is composed or sent.
//
// A named step rather than an inline call, for two reasons. Both verbs
// need it identically, so naming it keeps them from drifting. And it is
// the seam the tests drive: every other test in this package exercises
// the executor directly rather than through cobra, so the resolution has
// to be reachable the same way.
//
// The POLICY is command.ResolveAccounts' — live resolves, a bare preview
// warns and leaves the sentinel, --validate-remote resolves in a preview.
// It is shared with `--assignee` and `--reporter` rather than restated
// here, which is the whole point of the shared resolver: `@me` means one
// thing across this CLI, and design §6 drops `issue watch`/`unwatch` on
// the strength of that.
func resolveWatcherUser(
	ctx context.Context,
	deps Deps,
	dryRun, validateRemote bool,
	user *string,
) (core.JiraClient, error) {
	return command.ResolveAccounts(ctx, deps.JiraClient, deps.Preferences, dryRun, validateRemote,
		[]command.AccountField{{Flag: flagUser, Value: user, ValidateRemoteID: true}})
}

// resolveWatcherTarget selects explicit query intent before entering the shared
// account policy. Local unresolved queries carry no fabricated account ID.
func resolveWatcherTarget(ctx context.Context, deps Deps, dryRun, validateRemote bool, user *string, query string) (core.JiraClient, error) {
	if query == "" {
		return resolveWatcherUser(ctx, deps, dryRun, validateRemote, user)
	}
	resolved := query
	client, err := command.ResolveAccounts(ctx, deps.JiraClient, deps.Preferences, dryRun, validateRemote,
		[]command.AccountField{{Flag: flagUserQuery, Value: &resolved, LookupQuery: true}})
	if err == nil && (!dryRun || validateRemote) {
		*user = resolved
	}
	return client, err
}

// watcherWriteOptions is what runWatcherWrite needs from the flag set,
// gathered into one struct so the executor's signature does not grow a
// parameter per flag — commentAddOptions' own reasoning. --dry-run is
// absent for its reason too: it arrives on the Invocation.
type watcherWriteOptions struct {
	// client is the one ResolveAccounts built for @me, a query or exact-ID preflight,
	// or nil when the run needed no lookup.
	client      core.JiraClient
	accountID   string
	noReadback  bool
	parallelism int
}

// runWatcherWrite applies one verb to every key and assembles the keyed
// payload, or previews it.
//
// **The executor builds no client for a preview**, which is runCommentAdd's property
// unchanged and for its reason: a dry run returns before
// deps.JiraClient() is ever called, so it cannot reach the network
// because it never obtains the thing that could. Explicit remote preflight
// may already have supplied a client; this executor still performs no reads
// or writes for a preview.
//
// It returns the results rather than a whole Output because the two
// verbs' Output types differ (see envelope.IssueWatchersAddOutput for
// why they are two types), while everything down here is identical. The
// caller wraps.
func runWatcherWrite(
	ctx context.Context,
	deps Deps,
	inv command.Invocation,
	verb watcherVerb,
	opts watcherWriteOptions,
) ([]envelope.KeyedResult[envelope.IssueWatchersWriteData], error) {
	if inv.DryRun {
		return watcherPreviewResults(inv.Args), nil
	}
	// client may already exist: resolving @me or a query built one, and
	// deps.JiraClient is a constructor rather than a singleton accessor, so
	// calling it again would open a second and pay twice for credential
	// resolution.
	client := opts.client
	if client == nil {
		var err error
		if client, err = deps.JiraClient(ctx); err != nil {
			return nil, err
		}
	}
	return watcherWriteFanOut(ctx, client, inv.Args, verb, opts)
}

// watcherPreviewResults builds the per-key entries for a dry run: one
// per key, in argv order, each successful and each carrying an empty
// operation object.
//
// Every key is ok, and what that claims is exactly what previewResults
// (comment_add.go) documents — the payload for this key composed —
// rather than any statement about what Jira would do with it. The entry
// carries a pointer to an empty struct rather than nil for that
// function's reason: the contract says a successful key carries `data`,
// and nil would serialize the entry as neither a success nor a failure.
// Its Readback is absent, honestly: a preview read nothing back because
// it wrote nothing.
func watcherPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueWatchersWriteData] {
	results := make([]envelope.KeyedResult[envelope.IssueWatchersWriteData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueWatchersWriteData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueWatchersWriteData{},
		}
	}
	return results
}

// watcherWriteFanOut applies the verb to every key and assembles the
// keyed payload.
//
// The keyed partial-failure policy is commentAddFanOut's, unchanged and
// for its reasons: on a WRITE the keys that succeeded are real and
// cannot be taken back, so the only safe way to retry is to know which
// they were — which is what data.results[] is. A run where some keys
// took the change is `ok: true`, exit 0, with the failures inline; only
// a run where every key failed is the command's own failure.
//
// Read-only mode needs no check here either, for that function's reason:
// the transport refuses each write before the wire, so every key fails
// with read_only_blocked and KeyedFailure returns the original error.
func watcherWriteFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	verb watcherVerb,
	opts watcherWriteOptions,
) ([]envelope.KeyedResult[envelope.IssueWatchersWriteData], error) {
	outcomes := make([]watcherWriteOutcome, len(keys))
	// ONE spinner around the whole fan-out, not one per key: a spinner
	// owns the terminal's live region and several drawing at once
	// interleave into corruption. The label counts keys rather than
	// naming them, because it must not change per key while the animation
	// is up.
	_, err := command.Spin(ctx, watcherWriteSpinnerLabel(verb, keys), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure — that is what
		// makes a bad key a partial result rather than a whole-command one
		// — so the closure returns no error and each outcome is recorded
		// in place. Parallel writes outcomes[i] from the goroutine handling
		// key i, which needs no lock: x/sync.Parallel hands each call a
		// distinct index and the slice was sized up front. Order therefore
		// comes from the ARGV, never from which write finished first.
		xsync.Parallel(opts.parallelism, len(keys), func(i int) {
			outcomes[i] = writeOneWatcher(ctx, client, keys[i], verb, opts)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueWatchersWriteData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		// Spin joins producers before returning; keep their acknowledged
		// outcomes while cancellation or timeout describes the whole run.
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// watcherWriteOutcome is one key's result plus the raw error behind a
// failure — the same pairing commentAddOutcome makes, and for the reason
// its doc comment gives: the envelope's KeyError is the right shape to
// PUBLISH per key and the wrong thing to fail the command with, so the
// original error travels alongside for command.KeyedFailure to hand back
// untouched.
type watcherWriteOutcome struct {
	result envelope.KeyedResult[envelope.IssueWatchersWriteData]
	// err is nil exactly when result.OK.
	err error
}

// writeOneWatcher applies the verb to one key and reads the resulting
// watcher set back, unless asked not to.
//
// **A failed readback does not fail the key.** The write is the thing
// the caller asked for and it has already landed; a second GET that
// falls over afterwards means the CLI cannot DESCRIBE the outcome, not
// that the outcome did not happen. Reporting the key as failed would
// invite a retry of a write that already succeeded — the same reasoning
// transitionOne applies to a comment that could not be posted after a
// transition landed, and the same conclusion. The entry is successful
// with no readback and an explicit readback_error. A deliberate
// --no-readback has neither field.
func writeOneWatcher(
	ctx context.Context,
	client core.JiraClient,
	key string,
	verb watcherVerb,
	opts watcherWriteOptions,
) watcherWriteOutcome {
	if err := verb.write(ctx, client, key, opts.accountID); err != nil {
		return failedWatcherWriteResult(key, err)
	}
	data := envelope.IssueWatchersWriteData{}
	if !opts.noReadback {
		data.Readback, data.ReadbackError = readWatchersBack(ctx, client, key)
	}
	return watcherWriteOutcome{result: envelope.KeyedResult[envelope.IssueWatchersWriteData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// readWatchersBack publishes either the observed watcher set or the
// read's classified error. The acknowledged write remains successful.
// Reusing the list owner preserves its nil-response refusal and projection.
func readWatchersBack(ctx context.Context, client core.JiraClient, key string) (*envelope.IssueWatchersListData, *envelope.KeyError) {
	result := readWatchers(ctx, client, key).result
	return result.Data, result.Err
}

// failedWatcherWriteResult records one key's failure, resolved through
// the SAME taxonomy the envelope's own errors[] entries use
// (cli.MapError), so a caller branches on one vocabulary of codes
// wherever a failure appears.
func failedWatcherWriteResult(key string, err error) watcherWriteOutcome {
	class := cli.MapError(err)
	return watcherWriteOutcome{
		result: envelope.KeyedResult[envelope.IssueWatchersWriteData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    false,
			Err: &envelope.KeyError{
				Code:    class.Code,
				Message: class.Message,
				Hint:    class.Hint,
			},
		},
		err: err,
	}
}

// watcherWriteSpinnerLabel is the fan-out's one spinner label.
//
// Present tense, and correctly so: this function is only reached on the
// live path, because runWatcherWrite returns before the fan-out on a dry
// run. A preview draws no spinner at all, which is the cleanest possible
// answer to the preview-tense rule (axis H2) — there is no animation to
// phrase.
func watcherWriteSpinnerLabel(verb watcherVerb, keys []string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("%s a watcher %s %s", verb.gerund, verb.preposition, keys[0])
	}
	return fmt.Sprintf("%s a watcher %s %d issues", verb.gerund, verb.preposition, len(keys))
}

// renderWatchersAdd and renderWatchersRemove are the two verbs' human
// renderers. Each unwraps its own Output type — that is the one thing a
// renderer cannot share, since the type assertion IS the type — and
// hands the shared body the results plus its verb.
func renderWatchersAdd(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWatchersAddOutput)
	if !ok {
		return fmt.Errorf("issue.watchers.add: human renderer received %T, want envelope.IssueWatchersAddOutput", env.Data)
	}
	renderWatcherWrite(v, watcherAdd, out.DryRun, out.AccountID, out.UserQuery, out.Results)
	return nil
}

func renderWatchersRemove(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWatchersRemoveOutput)
	if !ok {
		return fmt.Errorf("issue.watchers.remove: human renderer received %T, want envelope.IssueWatchersRemoveOutput", env.Data)
	}
	renderWatcherWrite(v, watcherRemove, out.DryRun, out.AccountID, out.UserQuery, out.Results)
	return nil
}

// renderWatcherWrite draws whichever view the run produced.
//
// Preview tense is structural, not a phrasing habit (axis H2): the two
// branches are separate functions with no shared sentence between them,
// so a future edit to the live wording cannot leak a past-tense claim
// into the preview — renderCommentAdd's own split, for its reason.
func renderWatcherWrite(
	v *output.View,
	verb watcherVerb,
	dryRun bool,
	accountID, query string,
	results []envelope.KeyedResult[envelope.IssueWatchersWriteData],
) {
	if dryRun {
		renderWatcherWritePreview(v, verb, accountID, query, results)
		return
	}
	renderWatcherWriteResults(v, verb, results)
}

// renderWatcherWritePreview draws the dry-run view. Every verb is
// conditional, and the account id is named because it is the one value
// in the request a caller might have pasted wrong.
func renderWatcherWritePreview(
	v *output.View,
	verb watcherVerb,
	accountID, query string,
	results []envelope.KeyedResult[envelope.IssueWatchersWriteData],
) {
	// Each key is styled on its own — cli.Entity, one Render call per key
	// — and the plain connectives assembled around them, as SIBLING spans
	// on one line rather than nested (axis H5). Passing them to Line is
	// safe because Line writes bytes and styles nothing, unlike Section.
	keys := make([]string, 0, len(results))
	for _, r := range results {
		keys = append(keys, cli.Entity(r.Issue.Key))
	}
	// verb.bare, not the gerund or the past form: "Would adding" and
	// "Would added" are both wrong, which is why the infinitive is a
	// spelled-out field rather than something derived at render time.
	if accountID == "" && query != "" {
		v.Line(fmt.Sprintf("Would %s the account matching %s as a watcher %s %s after resolution.",
			verb.bare, cli.Text(fmt.Sprintf("%q", query)), verb.preposition, joinKeys(keys)))
	} else {
		v.Line(fmt.Sprintf("Would %s %s as a watcher %s %s.",
			verb.bare, cli.Entity(accountID), verb.preposition, joinKeys(keys)))
	}
	v.Hint("Nothing was sent. Drop --dry-run to apply it.")
}

// renderWatcherWriteResults draws the live view: one line per key, in
// argv order, saying whether that key took the change and why not when
// it did not.
//
// A failed key's own message is Jira-authored in the common case, so it
// goes through cli.Text like every other Jira string, while the hint is
// CLI-authored and rides the message path — the same split
// renderCommentAddResults makes.
//
// The readback is reported as a COUNT rather than a table. A table per
// key would bury the thing this command is reporting (the change) under
// a list the caller can get from `watchers list`, and the count is the
// part that confirms the write did what they meant. It is omitted
// entirely when there is none, which --no-readback makes the common
// case.
//
// The account id is deliberately NOT repeated per line: it is one value
// for the whole run, the caller typed it, and it already rides the
// envelope at data.account_id.
func renderWatcherWriteResults(
	v *output.View,
	verb watcherVerb,
	results []envelope.KeyedResult[envelope.IssueWatchersWriteData],
) {
	for _, r := range results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": watcher "+verb.past+"."))
		if r.Data.Readback != nil {
			// The same sink renderWatchersResult (watchers.go) uses for the
			// same count, rather than cli.Number: WatchCount is an int64 and
			// cli.Number takes an int, so reaching for it would mean a
			// narrowing conversion for a decoration. One idiom for one
			// number, in both places that print it.
			v.Line(cli.Text(fmt.Sprintf("  %d now watching.", r.Data.Readback.WatchCount)))
		}
		if r.Data.ReadbackError != nil {
			v.Line(cli.Text("  Readback failed: " + r.Data.ReadbackError.Message))
			v.Hint(r.Data.ReadbackError.Hint)
		}
	}
}
