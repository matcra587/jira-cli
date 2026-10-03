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
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// newWatchers returns the `jira issue watchers` command group.
//
// Plural, where `link` and `attachment` are singular. That is the noun
// Jira itself uses for the resource (the endpoint is
// .../issue/{key}/watchers) and the spelling 0.15 shipped, and a rename
// to match its siblings' grammar would break every caller for the sake
// of a consistency no user asked for.
func newWatchers(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watchers",
		Short: "Work with an issue's watchers",
		Long: "Read and change who is watching issues.\n\n" +
			"A watcher is notified when an issue changes. Watching is per " +
			"account, so listing is how you find the account id the other " +
			"watcher verbs take — and how you check who a change is about to " +
			"notify before you make it.\n\n" +
			"All three verbs take a list of issue keys. `add` and `remove` name " +
			"ONE account with `--user` and apply it to every key, because the " +
			"common shape is one person's subscription across several issues " +
			"rather than several people's across one.\n\n" +
			"Both writes describe an END STATE, so they are safe to re-run: " +
			"adding an account that already watches succeeds, and so does " +
			"removing one that was not watching.",
		Example: "# who is watching KEY-123\n" +
			"$ jira issue watchers list KEY-123\n\n" +
			"# start watching a few issues\n" +
			"$ jira issue watchers add KEY-123 KEY-124 --user 5b10a2844c20165700ede21g\n\n" +
			"# stop watching one\n" +
			"$ jira issue watchers remove KEY-123 --user 5b10a2844c20165700ede21g",
	}
	cmd.AddCommand(newWatchersAdd(deps))
	cmd.AddCommand(newWatchersList(deps))
	cmd.AddCommand(newWatchersRemove(deps))
	return cmd
}

// newWatchersList returns `jira issue watchers list`.
//
// On the absent paging flags, which this command shares with its two
// keyed siblings for a DIFFERENT reason than they share with each
// other. Links and attachments have no list endpoint at all, so the
// question never arises. Watchers do — GET .../issue/{key}/watchers is
// a real request — and it simply returns everything: the operation
// declares one parameter (the key), and its response carries no
// startAt, maxResults, total or cursor. 0.15 gave this command `KEY…`
// and `--parallelism` only, which turns out to be right; this is the
// first time anyone checked why.
func newWatchersList(deps Deps) *cobra.Command {
	// keyed carries the shared multi-key surface (--parallelism). The
	// same call marks this command keyed, which command.New cross-checks
	// against the Keyed declaration below — see AddKeyedFlags.
	var keyed command.KeyedFlags
	// cmd is captured so Validate can ask it isWeb(cmd) — --web is a
	// PERSISTENT flag registered on the `issue` group (issue.go), so this
	// command inherits it whether or not it can honor it, and refusing an
	// explicit one is this command's own job (refuseWebOnKeyedRead, which
	// link.go owns and states the reasoning for).
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWatchersListOutput]{
		Op:       "issue.watchers.list",
		Identity: true,
		Keyed:    true,
		Use:      "list ISSUE_KEY...",
		Short:    "List an issue's watchers",
		Long: "List who is watching one or more issues, with the account id each " +
			"one is added or removed by.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others — its entry carries `ok: false` and the " +
			"reason, and the command still succeeds. Only a run where EVERY " +
			"key failed is reported as a failure of the command itself.\n\n" +
			"Total failure, cancellation and timeout retain all joined per-key " +
			"outcomes in the full failure frame, including completed reads.\n\n" +
			"Every watcher is returned, and there is no page size to set. " +
			"Jira's watcher endpoint takes no paging parameters and answers " +
			"with the whole set, so there is no page to be on. Use `--jq` if " +
			"you want fewer of them.\n\n" +
			"Seeing WHO is watching needs the \"View voters and watchers\" " +
			"permission on the project. Without it Jira still reports how " +
			"many — `data.results[].data.watch_count` is populated either " +
			"way — but sends no names, and the command says so rather than " +
			"reporting an empty list.\n\n" +
			"`--web` is refused here. It opens one page and this command reads " +
			"a list of keys, so there is no single thing to open: run `jira " +
			"issue view KEY --web` for whichever issue you want to look at.",
		Example: "# who is watching one issue\n" +
			"$ jira issue watchers list KEY-123\n\n" +
			"# several issues at once\n" +
			"$ jira issue watchers list KEY-123 KEY-124 KEY-125\n\n" +
			"# just the human watchers, as json\n" +
			"$ jira issue watchers list KEY-123 --output=json --jq '.data.results[].data.watchers[] | select(.account_type == \"atlassian\") | {account_id, display_name}'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			return refuseWebOnKeyedRead(cmd)
		},
		Human:         renderWatchersList,
		RetainOnError: func(out envelope.IssueWatchersListOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWatchersListOutput, error) {
			return runWatchersList(ctx, deps, inv.Args, keyed.Parallelism)
		},
	})
}

// runWatchersList reads every key's watchers and assembles the keyed
// payload.
//
// Pulled out of the command.Spec closure so it is directly testable
// against a fake core.JiraClient, matching runAttachmentList's own
// split. Like that function and unlike runLinkList, it returns no site:
// nothing in this table is a browse target — an account id has no issue
// page — and the section headers are the keys the caller typed.
func runWatchersList(ctx context.Context, deps Deps, keys []string, parallelism int) (envelope.IssueWatchersListOutput, error) {
	var zero envelope.IssueWatchersListOutput
	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}

	outcomes := make([]watchersOutcome, len(keys))
	// ONE spinner around the whole fan-out, not one per key: a spinner
	// owns the terminal's live region, and several drawing at once
	// interleave into corruption. The label counts keys rather than
	// naming them, because it must not change per key while the
	// animation is up.
	_, err = command.Spin(ctx, watchersSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure — that is what
		// makes a bad key a partial result rather than a whole-command
		// one — so the closure returns no error and each outcome is
		// recorded in place instead. Parallel writes outcomes[i] from the
		// goroutine handling key i, which needs no lock: x/sync.Parallel
		// hands each call a distinct index, and the slice was sized up
		// front. Order therefore comes from the ARGV, never from which
		// read finished first, which is the ordering guarantee
		// envelope.KeyedResults publishes.
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = readWatchers(ctx, client, keys[i])
		})
		return struct{}{}, nil
	})
	// The published verdicts and the raw errors behind them, split apart
	// for command.KeyedFailure — which owns the partial-vs-total rule for
	// every keyed command, not just this one.
	results := make([]envelope.KeyedResult[envelope.IssueWatchersListData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	out := envelope.IssueWatchersListOutput{}
	out.Results = results
	if err != nil {
		// Spin joins each reader before returning. Preserve its completed
		// rows and failed targets while cancellation describes the run.
		return out, err
	}
	return out, command.KeyedFailure(results, raw)
}

// watchersOutcome is one key's result plus the raw error behind a
// failure — the same pairing attachmentOutcome makes, and for the
// reason its doc comment gives: the envelope's KeyError is the right
// shape to PUBLISH per key and the wrong thing to fail the command
// with, so the original error travels alongside for
// command.KeyedFailure to hand back untouched.
type watchersOutcome struct {
	result envelope.KeyedResult[envelope.IssueWatchersListData]
	// err is nil exactly when result.OK.
	err error
}

// watchersSpinnerLabel is the fan-out's one spinner label. It says how
// many issues are being read rather than which, because the label is
// fixed for the life of the animation while the reads are not.
func watchersSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Loading watchers for " + keys[0]
	}
	return fmt.Sprintf("Loading watchers for %d issues", len(keys))
}

// readWatchers is one key's whole outcome: its watchers, or why it has
// none to report. It returns a result rather than an error precisely
// because a failure here is data — the entry the caller reads at
// data.results[] — and not the command's own failure.
func readWatchers(ctx context.Context, client core.JiraClient, key string) watchersOutcome {
	read, err := client.GetWatchers(ctx, key)
	switch {
	case err != nil:
		return failedWatchersResult(key, err)
	case read.Watchers == nil:
		// GetWatchers' own contract (internal/adapters/jira/client.go) is
		// a non-nil Watchers whenever it returns a nil error. Failing
		// loudly rather than trusting that silently mirrors
		// readAttachments' own guard: a zero-valued payload would read as
		// an issue nobody watches rather than the failure it actually is.
		return failedWatchersResult(key, fmt.Errorf("issue.watchers.list: empty watchers response for %q", key))
	}
	data := envelope.IssueWatchersListData{
		Watchers:   watcherViews(val(read.Watchers.Watchers)),
		WatchCount: read.Watchers.WatchCount.Value,
		IsWatching: read.Watchers.IsWatching.Value,
	}
	// The identity carries only the key, and this is the one place a
	// SUCCESSFUL keyed result here differs from its siblings'. Link and
	// attachment reads ride GetIssue, whose response names the issue, so
	// they fill id and self from it. The watchers endpoint answers with
	// the watcher set alone — no issue id, no issue self, and its own
	// `self` is the URL of the watcher LIST, not of the issue — so
	// neither is resolved. IssueRef omits them rather than emitting them
	// empty, which its own doc comment gives as the meaning of their
	// absence: not known. Deriving them from the key would mean a second
	// GET per key to publish two fields nobody asked for.
	return watchersOutcome{result: envelope.KeyedResult[envelope.IssueWatchersListData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// failedWatchersResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use (cli.MapError),
// so a caller branches on one vocabulary of codes wherever a failure
// appears.
func failedWatchersResult(key string, err error) watchersOutcome {
	class := cli.MapError(err)
	return watchersOutcome{
		result: envelope.KeyedResult[envelope.IssueWatchersListData]{
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

// watcherViews narrows one issue's wire watchers to their CLI-shaped
// view — a straight field-by-field narrowing, as attachmentViews is,
// the only judgment in it being which wire fields the contract
// publishes at all (see envelope.WatcherView, which drops the
// privacy-gated emailAddress and the avatar URL set).
func watcherViews(watchers []jira.UserDetails) []envelope.WatcherView {
	// Never nil: an issue nobody watches publishes an empty array, so a
	// caller iterating data.results[].data.watchers needs no null check.
	out := make([]envelope.WatcherView, 0, len(watchers))
	for _, w := range watchers {
		out = append(out, envelope.WatcherView{
			AccountID:   w.AccountID.Value,
			DisplayName: w.DisplayName.Value,
			Active:      w.Active.Value,
			AccountType: w.AccountType.Value,
		})
	}
	return out
}

// renderWatchersList writes the human view: one section per key, in the
// order the caller gave them, exactly as renderAttachmentList does and
// for the same reason — a keyed read's whole shape is per-key, and a
// single merged table would lose which issue each watcher belongs to.
func renderWatchersList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWatchersListOutput)
	if !ok {
		return fmt.Errorf("issue.watchers.list: human renderer received %T, want envelope.IssueWatchersListOutput", env.Data)
	}
	for i, r := range out.Results {
		if i > 0 {
			v.Line()
		}
		// cli.Text, not a styled sink: Section hands its title to clog's
		// Divider, which bolds whatever it is given, and a pre-styled
		// title would strand its own reset mid-span and drop the bold
		// from everything after it (axis H5 — see cli.EntityAs's own doc
		// comment for the byte-verified version of that bug).
		v.Section(cli.Text(r.Issue.Key))
		renderWatchersResult(v, r)
	}
	return nil
}

// renderWatchersResult draws one key's section body: its failure, its
// emptiness, or its watchers.
//
// The empty case splits in two, which is what this renderer has that
// its siblings do not. An empty array can mean nobody is watching or
// that the caller may not be told who is, and those deserve different
// sentences — the count Jira sends either way is what tells them apart
// (see envelope.IssueWatchersListData). Reporting "No watchers." to
// someone whose permissions hid seven of them would be the command
// stating a falsehood it had the evidence to avoid.
func renderWatchersResult(v *output.View, r envelope.KeyedResult[envelope.IssueWatchersListData]) {
	if !r.OK {
		// The message is Jira-authored in the common case (its own 404
		// prose rides through the taxonomy), so it goes through cli.Text
		// like every other Jira string. The hint is CLI-authored and
		// static, and rides the message path so the flags it names render
		// in clog's inline-code style — the same split renderLinkResult
		// and renderAttachmentResult already make.
		v.Line(cli.Text(r.Err.Message))
		v.Hint(r.Err.Hint)
		return
	}
	if len(r.Data.Watchers) == 0 {
		if r.Data.WatchCount > 0 {
			v.Line(cli.Text(fmt.Sprintf("%d watching, but this account cannot see who.", r.Data.WatchCount)))
			v.Hint("Seeing watcher names needs the \"View voters and watchers\" permission on the project.")
			return
		}
		v.Line(cli.Text("No watchers."))
		return
	}
	output.Table(v, cli.ResolvedTheme(), issueWatchersColumns(), r.Data.Watchers)
}
