package issue

import (
	"context"
	"errors"
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

// Visibility flag names, spelled once so registration and the refusal
// below cannot disagree about them.
const (
	flagVisibilityGroup = "visibility-group"
	flagVisibilityRole  = "visibility-role"
)

// newCommentAdd returns `jira issue comment add`.
//
// THE FIRST COMMAND IN THIS TREE THAT WRITES TO JIRA, and the first that
// is both keyed and mutating. Four decisions below are contract for
// every write that follows, so each is argued where it is made rather
// than in a design document a later implementer may not read:
//
//   - the dry-run preview publishes the composed request and never
//     builds a client (runCommentAdd);
//   - a partially-failed write is a partial result, not a command
//     failure, for a STRONGER reason than it is on a read
//     (commentAddFanOut);
//   - there is no --force, because there is nothing destructive to
//     confirm (see the Long text and command.WithForce's doc comment);
//   - the created comment is published unconditionally, because the
//     POST's own response carries it (envelope.IssueCommentAddData).
func newCommentAdd(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var sel command.SelectorFlags
	var body command.BodyFlags
	// mut carries the shared write surface. Bare — no options — which is
	// the correct call for this command and not an unfinished one: it
	// needs the preview every mutation has and none of the opt-in flags
	// (see command.AddMutationFlags' own doc comment).
	var mut command.MutationFlags
	var visGroup, visRole string
	// cmd is captured so Validate can reach the parsed flag set, which
	// alias folding needs and Spec.Validate is not handed — the same
	// capture newWatchersList makes, for the same reason.
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueCommentAddOutput]{
		Op:          "issue.comment.add",
		WritesJira:  true,
		Identity:    true,
		Keyed:       true,
		Passthrough: true,
		Use:         "add ISSUE_KEY...",
		Short:       "Add a comment to an issue",
		Long: "Post a comment to one or more issues.\n\n" +
			"The body comes from exactly one of three routes, and they are " +
			"alternatives rather than fallbacks: `--markdown` for text typed on " +
			"the command line, `--markdown-file` to read it from a file (`-` " +
			"reads standard input), or `--json-input` to send a native ADF " +
			"document. ADF is the lossless route; Markdown is a convenience " +
			"layer converted to ADF on the way. Giving two of them is refused " +
			"rather than merged — a body can only come from one place.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others, and that matters more here than on a " +
			"read — the comments that DID post are real, and the envelope names " +
			"exactly which keys got one so you can retry only the rest instead " +
			"of commenting twice. Only a run where EVERY key failed is reported " +
			"as a failure of the command itself.\n\n" +
			"Total failure, cancellation and timeout retain the authored " +
			"request and all joined per-key outcomes in the full failure " +
			"frame. Keep completed comment ids and inspect failed or uncertain " +
			"targets before retrying; repeating an add creates duplicates.\n\n" +
			"`--dry-run` composes the whole request and stops. It never " +
			"contacts Jira, so it reports the body and visibility that WOULD be " +
			"sent — `data.body` is byte-identical to what a live run posts — but " +
			"it cannot tell you whether the issues exist or whether you may " +
			"comment on them.\n\n" +
			"Explicit keys need no confirmation. A selector-driven write " +
			"confirms its resolved targets; `--force` confirms them up front " +
			"when no one can answer a prompt.\n\n" +
			"The comment Jira creates comes back at " +
			"`data.results[].data.comment`, including its id — which is what " +
			"`comment edit` and `comment delete` take. It costs no extra " +
			"request: Jira's own response to the post carries it.\n\n" +
			command.SelectorHelp + "\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# comment on one issue\n" +
			"$ jira issue comment add KEY-123 --markdown 'Deployed to staging.'\n\n" +
			"# the same comment on several issues\n" +
			"$ jira issue comment add KEY-123 KEY-124 KEY-125 --markdown 'Tracked in the release ticket.'\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue comment add KEY-123 --markdown 'Draft.' --dry-run\n\n" +
			"# body from a file, restricted to one group\n" +
			"$ jira issue comment add KEY-123 --markdown-file notes.md --visibility-group developers\n\n" +
			"# body from standard input\n" +
			"$ cat notes.md | jira issue comment add KEY-123 --markdown-file -\n\n" +
			"# a native ADF document, the lossless route\n" +
			"$ jira issue comment add KEY-123 --json-input comment.json\n\n" +
			"# just the new comment ids, as json\n" +
			"$ jira issue comment add KEY-123 KEY-124 --markdown 'Done.' --output=json --jq '.data.results[] | select(.ok) | {key: .issue.key, id: .data.comment.id}'\n\n" +
			"# every issue a query matches; the confirmation lists them first\n" +
			"$ jira issue comment add --jql 'project = KEY AND sprint = 12' --markdown 'Sprint closed.'\n\n" +
			"# a list of keys you wrote out yourself\n" +
			"$ jira issue comment add --from-file ./keys.txt --markdown 'Done.' --force",
		// Min is 0 because a selector replaces the positionals entirely;
		// SelectorFlags.Validate gives the "name something" refusal, which
		// can name both routes where cobra's arity check could not.
		Args: command.ArgSpec{Min: 0, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			command.AddBodyFlags(c, &body)
			// --force and --validate-remote both arrive WITH the selectors
			// and neither would mean anything without them. A comment add
			// that names its keys still prompts for nothing, so --force has
			// nothing to skip there; a selector-driven one prompts because
			// the caller did not name the issues, and --validate-remote is
			// what lets a preview resolve the query it would otherwise
			// refuse to run.
			command.AddMutationFlags(c, &mut, command.WithForce(), command.WithValidateRemote())
			command.AddSelectorFlags(c, &sel)
			command.AddStringVar(c, &visGroup, flagVisibilityGroup, "",
				"Restrict the comment to members of this group",
				command.FlagOpts{Terse: "Group-only", Placeholder: "name"})
			command.AddStringVar(c, &visRole, flagVisibilityRole, "",
				"Restrict the comment to holders of this project role",
				command.FlagOpts{Terse: "Role-only", Placeholder: "name"})
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			// Alias folding FIRST: --body must have become --markdown
			// before the conflict check looks, or `--body x --json-input f`
			// would slip past a check that only sees the canonical flags.
			if err := body.ResolveAliases(cmd.Flags()); err != nil {
				return err
			}
			if err := body.Conflict(); err != nil {
				return err
			}
			if err := body.RequireBody("a comment"); err != nil {
				return err
			}
			if err := visibilityConflict(visGroup, visRole); err != nil {
				return err
			}
			// Every positional here is a key, so the count goes through
			// as-is. cmd.Flags().Args() is what cobra hands RunE, read one
			// step early — newTransition's capture, for its reason.
			if err := sel.Validate(len(cmd.Flags().Args())); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderCommentAdd,
		RetainOnError: func(out envelope.IssueCommentAddOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCommentAddOutput, error) {
			return runCommentAdd(ctx, deps, inv, commentAddOptions{
				body:           &body,
				visibility:     visibilityFor(visGroup, visRole),
				selector:       sel,
				force:          mut.Force,
				validateRemote: mut.ValidateRemote,
				parallelism:    keyed.Parallelism,
			})
		},
	})
}

// commentAddOptions is what runCommentAdd needs from the flag set,
// gathered into one struct so the handler's signature does not grow a
// parameter per flag.
//
// --dry-run is deliberately absent: it arrives on the Invocation, read
// from the flag the construction path registered itself. Copying it in
// here would give one fact two carriers that could disagree, which is
// the exact bug Invocation.DryRun's own doc comment records.
type commentAddOptions struct {
	body       *command.BodyFlags
	visibility *jira.CommentVisibility
	// selector, force and validateRemote are the bulk-selector surface.
	// force is only ever consulted for a selector-driven run: naming keys
	// prompts for nothing, so there is no prompt for it to skip.
	selector       command.SelectorFlags
	force          bool
	validateRemote bool
	parallelism    int
}

// visibilityFor builds the wire visibility from whichever flag was
// given, or nil for an unrestricted comment. The two flags are already
// known to be mutually exclusive — visibilityConflict refused the pair
// during Validate — so checking group first is a choice of order, not a
// precedence rule that could hide a conflict.
func visibilityFor(group, role string) *jira.CommentVisibility {
	switch {
	case group != "":
		return &jira.CommentVisibility{Type: "group", Value: group}
	case role != "":
		return &jira.CommentVisibility{Type: "role", Value: role}
	}
	return nil
}

// visibilityConflict refuses both visibility flags at once.
//
// Jira's visibility object holds ONE {type, value} pair, so there is no
// payload that means "this group and that role" — a CLI accepting both
// would have to silently drop one, and the caller would find out by
// discovering who could read their comment.
func visibilityConflict(group, role string) error {
	if group == "" || role == "" {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
		"--%s and --%s both restrict who can read the comment, and Jira allows only one restriction — pick one and drop the other",
		flagVisibilityGroup, flagVisibilityRole,
	))
	e.Field = flagVisibilityGroup
	return e
}

// refuseWebOnKeyedWrite refuses --web on a keyed mutation.
//
// A sibling of refuseWebOnKeyedRead (link.go) rather than a reuse of it,
// because the prose differs where it matters: that one says the command
// "reads a list of keys", and saying so on a command that WRITES to them
// would misdescribe what --web was refused on. The reasoning is
// otherwise identical — --web opens one page, and this command has N
// subjects — and it stays CodeFlagNotApplicable for the same reason: the
// flag is real and spelled right, so the fix is to drop it.
func refuseWebOnKeyedWrite(cmd *cobra.Command) error {
	if !isWeb(cmd) {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--web opens one page, and this command writes to a list of keys — run `jira issue view KEY --web` for the issue you want to look at")
	e.Field = flagWeb
	return e
}

// runCommentAdd composes the request once and either previews it or
// posts it to every key.
//
// **Compose once, send N times.** The body and visibility are resolved
// before any key is touched, so all N issues get a byte-identical
// payload and a per-key failure can never be a per-key difference in
// what was sent. It is also what makes the preview trustworthy: the
// SAME composed request is what a live run posts, not a reconstruction
// of it built down a separate branch.
//
// **The preview builds no client.** A dry run returns before
// deps.JiraClient() is ever called, which is what "local-only, never
// contacts Jira" has to mean in code rather than in a doc comment: the
// preview cannot reach the network because it never obtains the thing
// that could. It also means `--dry-run` works with no credentials
// configured at all — a caller can check what a command would send
// before they have logged in. `auth login`'s preview does the opposite
// (it verifies the token, because verifying IS the useful half there);
// the difference is that nothing about a composed comment payload is
// knowable only from the server.
func runCommentAdd(ctx context.Context, deps Deps, inv command.Invocation, opts commentAddOptions) (envelope.IssueCommentAddOutput, error) {
	var zero envelope.IssueCommentAddOutput

	body, err := opts.body.Resolve(ctx, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}
	req := jira.AddCommentRequest{Body: body.ADF, Visibility: opts.visibility}

	out := envelope.IssueCommentAddOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		Body:          body.ADF,
		BodyRoute:     body.Route,
		Visibility:    visibilityView(opts.visibility),
	}

	newClient := oneClient(deps.JiraClient)
	_, targets, err := resolveTargets(ctx, newClient, inv, selectorInputs{
		selector:       opts.selector,
		validateRemote: opts.validateRemote,
	})
	if err != nil {
		return zero, err
	}
	out.Selector = targets.Summary

	if inv.DryRun {
		// Empty exactly when the selector went unresolved; the warning
		// ResolveTargets raised says why, and Resolved=false is in the
		// envelope beside it.
		out.Results = previewResults(targets.Keys)
		return out, nil
	}

	// Resolution first, then the question — the ordering rule that makes
	// the prompt able to name the issues rather than echo the query.
	if err := confirmSelected(deps.Confirm, inv, opts.force,
		selectedWrite{Gerund: "commenting on", Imperative: "Comment"}, targets); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("comment canceled — %s untouched", joinPlainKeys(targets.Keys)))
		}
		return zero, err
	}

	client, err := newClient()
	if err != nil {
		return zero, err
	}
	out.Results, err = commentAddFanOut(ctx, client, targets.Keys, req, opts.parallelism)
	return out, err
}

// previewResults builds the per-key entries for a dry run: one per key,
// in argv order, each successful and each carrying an empty operation
// object.
//
// Every key is ok, and the envelope's own doc comment states precisely
// what that claims — the payload for this key composed — rather than
// letting a reader infer that Jira would accept it. Keeping the entry
// shape identical to a live run's is the point: a caller can diff a
// preview against a real result without the two disagreeing about their
// structure.
func previewResults(keys []string) []envelope.KeyedResult[envelope.IssueCommentAddData] {
	results := make([]envelope.KeyedResult[envelope.IssueCommentAddData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueCommentAddData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			// A pointer to an empty struct, not nil: the contract says a
			// successful key carries `data`, and a preview's key succeeded.
			// nil would serialize the entry as neither a success nor a
			// failure.
			Data: &envelope.IssueCommentAddData{},
		}
	}
	return results
}

// commentAddFanOut posts the comment to every key and assembles the
// keyed payload.
//
// **The keyed partial-failure policy transfers from reads unchanged, and
// the argument for it is STRONGER here.** On a read, half-failing costs
// nothing: the caller re-runs and pays for some duplicate GETs. On a
// write, the comments that already posted are real and cannot be taken
// back, so the ONLY safe way to retry is to know exactly which keys
// succeeded — which is what data.results[] is. Failing the whole command
// would discard that list and leave a caller with two bad options:
// re-run everything and post duplicates, or go and look. So a run where
// some keys posted is `ok: true`, exit 0, with the failures inline in
// their own entries, exactly as command.KeyedFailure decides for a read.
//
// The exit code is the part worth being explicit about, because it is
// the part that can surprise: `jira issue comment add A B C && echo ok`
// prints ok even when C failed. That is deliberate. `ok` and the exit
// code report whether the COMMAND ran, and it did — it posted what it
// could and said what it could not. A caller who needs per-key certainty
// reads the per-key field that exists for it
// (`--jq '.data.results[] | select(.ok == false)'`), and a caller who needs
// all-or-nothing does not want this command at all, because Jira offers
// no transaction across issues and neither can a CLI over it.
//
// Read-only mode needs no check here. The transport refuses each POST
// before it reaches the wire, so every key fails with read_only_blocked,
// no key succeeds, and KeyedFailure returns the original error with its
// own code and exit — the same answer a single key would have produced.
func commentAddFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	req jira.AddCommentRequest,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueCommentAddData], error) {
	outcomes := make([]commentAddOutcome, len(keys))
	// ONE spinner around the whole fan-out, not one per key: a spinner
	// owns the terminal's live region and several drawing at once
	// interleave into corruption. The label counts keys rather than
	// naming them, because it must not change per key while the
	// animation is up.
	_, err := command.Spin(ctx, commentAddSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure — that is what
		// makes a bad key a partial result rather than a whole-command
		// one — so the closure returns no error and each outcome is
		// recorded in place. Parallel writes outcomes[i] from the
		// goroutine handling key i, which needs no lock: x/sync.Parallel
		// hands each call a distinct index and the slice was sized up
		// front. Order therefore comes from the ARGV, never from which
		// post finished first.
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = postComment(ctx, client, keys[i], req)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueCommentAddData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		// Spin joins each producer. Preserve created identities and
		// unfinished targets while interruption describes the whole run.
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// commentAddOutcome is one key's result plus the raw error behind a
// failure — the same pairing watchersOutcome makes, and for the reason
// its doc comment gives: the envelope's KeyError is the right shape to
// PUBLISH per key and the wrong thing to fail the command with, so the
// original error travels alongside for command.KeyedFailure to hand back
// untouched.
type commentAddOutcome struct {
	result envelope.KeyedResult[envelope.IssueCommentAddData]
	// err is nil exactly when result.OK.
	err error
}

// commentAddSpinnerLabel is the fan-out's one spinner label.
//
// Present tense, and correctly so: this function is only reached on the
// live path, because runCommentAdd returns before the fan-out on a dry
// run. A preview draws no spinner at all, which is the cleanest possible
// answer to the preview-tense rule (axis H2) — there is no animation to
// phrase.
func commentAddSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Adding a comment to " + keys[0]
	}
	return fmt.Sprintf("Adding a comment to %d issues", len(keys))
}

// postComment posts one key's comment. It returns a result rather than
// an error precisely because a failure here is data — the entry the
// caller reads at data.results[] — and not the command's own failure.
func postComment(ctx context.Context, client core.JiraClient, key string, req jira.AddCommentRequest) commentAddOutcome {
	write, err := client.AddComment(ctx, key, req)
	if err != nil {
		return failedCommentAddResult(key, err)
	}
	if err := core.ValidateCommentReceipt(write.Comment, ""); err != nil {
		return failedCommentAddResult(key, err)
	}
	view := commentViews([]jira.Comment{*write.Comment})[0]
	return commentAddOutcome{result: envelope.KeyedResult[envelope.IssueCommentAddData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &envelope.IssueCommentAddData{Comment: &view},
	}}
}

// failedCommentAddResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use (cli.MapError),
// so a caller branches on one vocabulary of codes wherever a failure
// appears.
func failedCommentAddResult(key string, err error) commentAddOutcome {
	class := cli.MapError(err)
	return commentAddOutcome{
		result: envelope.KeyedResult[envelope.IssueCommentAddData]{
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

// visibilityView narrows the wire visibility to its CLI-shaped view, or
// nil when the comment carries no restriction.
func visibilityView(v *jira.CommentVisibility) *envelope.CommentVisibilityView {
	if v == nil {
		return nil
	}
	return &envelope.CommentVisibilityView{Type: v.Type, Value: v.Value}
}

// renderCommentAdd writes the human view: one line per key saying what
// happened to it, and on a preview the body that would be sent.
//
// **Preview tense is structural here, not a phrasing habit** (axis H2).
// The dry-run branch and the live branch are separate functions with no
// shared sentence between them, so a future edit to the live wording
// cannot leak a past-tense claim into the preview. The preview also
// shows the composed body, because "what would be sent" is the entire
// question a preview is run to answer, and a caller who cannot see the
// body has been told only that the command would do something.
func renderCommentAdd(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCommentAddOutput)
	if !ok {
		return fmt.Errorf("issue.comment.add: human renderer received %T, want envelope.IssueCommentAddOutput", env.Data)
	}
	if out.DryRun {
		renderCommentAddPreview(v, out)
		return nil
	}
	renderCommentAddResults(v, out)
	return nil
}

// renderCommentAddPreview draws the dry-run view. Every verb is
// conditional, and the visibility line is present only when a
// restriction was composed — a preview that said "visible to everyone"
// would be stating a Jira default as though the caller had chosen it.
func renderCommentAddPreview(v *output.View, out envelope.IssueCommentAddOutput) {
	if renderUnresolvedSelector(v, out.Selector, "comment on") {
		return
	}
	// Each key is styled on its own — cli.Entity, one Render call per
	// key — and the plain connectives are assembled around them. They are
	// SIBLING spans on one line, never nested: each carries its own reset
	// and the next opens a fresh style, which is the shape H5 requires.
	// Passing them to Line is safe because Line writes bytes and styles
	// nothing (unlike Section, which hands its title to clog's Divider —
	// that is where a pre-styled string would strand a reset mid-span).
	keys := make([]string, 0, len(out.Results))
	for _, r := range out.Results {
		keys = append(keys, cli.Entity(r.Issue.Key))
	}
	v.Line(fmt.Sprintf("Would add a comment to %s.", joinKeys(keys)))
	if out.Visibility != nil {
		v.Line(cli.Text(fmt.Sprintf("Would restrict it to the %q %s.", out.Visibility.Value, out.Visibility.Type)))
	}
	v.Hint("Nothing was sent. Drop --dry-run to post it.")
}

// renderCommentAddResults draws the live view: one line per key, in argv
// order, saying whether that key got the comment and why not when it did
// not. A failed key's own message is Jira-authored in the common case,
// so it goes through cli.Text like every other Jira string, while the
// hint is CLI-authored and rides the message path — the same split
// renderWatchersResult makes.
func renderCommentAddResults(v *output.View, out envelope.IssueCommentAddOutput) {
	for _, r := range out.Results {
		// cli.Entity for the key and cli.Text for the rest, as two
		// sibling spans concatenated into ONE Line argument — not
		// EntityAs over the whole sentence, which would tint the outcome
		// wording with the key's identity color and make "comment added"
		// and a failure message read as different-colored versions of the
		// same thing. cli.Text is what sanitizes the Jira-authored
		// message before it can reach a terminal (axis H4).
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": comment added."))
	}
}

// joinKeys renders a key list for one sentence of prose: "KEY-1",
// "KEY-1 and KEY-2", "KEY-1, KEY-2 and KEY-3". Prose, not a table, so it
// takes the serial forms a person would write rather than a bare
// comma-join.
func joinKeys(keys []string) string {
	switch len(keys) {
	case 0:
		return ""
	case 1:
		return keys[0]
	case 2:
		return keys[0] + " and " + keys[1]
	}
	head := keys[:len(keys)-1]
	out := ""
	for i, k := range head {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out + " and " + keys[len(keys)-1]
}
