package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// flagDeleteSubtasks is the canonical spelling; `--cascade` is its
// hidden alias (design §5, from jcli).
const flagDeleteSubtasks = "delete-subtasks"

// flagCascade is the hidden alias. Registered without clib metadata,
// like every other hidden alias in this tree.
const flagCascade = "cascade"

// newDelete returns `jira issue delete`.
//
// THE MOST DESTRUCTIVE COMMAND IN THE TREE. It reuses the confirmation
// `comment delete` established rather than inventing a second one — but
// it asks a HARDER question, and that difference is argued in
// confirmIssueDelete rather than assumed.
func newDelete(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var mut command.MutationFlags
	var sel command.SelectorFlags
	var deleteSubtasks bool
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueDeleteOutput]{
		Op:         "issue.delete",
		WritesJira: true,
		Identity:   true,
		Keyed:      true,
		RetainOnError: func(out envelope.IssueDeleteOutput) bool {
			return len(out.Results) > 0
		},
		Use:   "delete ISSUE_KEY...",
		Short: "Delete issues",
		Long: "Delete one or more issues.\n\n" +
			"**This is the most destructive thing this CLI can do, and it cannot " +
			"be undone.** An issue does not go alone: its comments, its " +
			"attachments, its worklog and its whole history go with it, and Jira " +
			"keeps no trash. Nothing here brings any of it back.\n\n" +
			"It asks before acting, and the question names every issue rather " +
			"than counting them — a list you can read is the only way to notice " +
			"the key you did not mean to type. At a terminal you get a yes/no " +
			"dialog. Everywhere else — a script, a pipeline, an agent — there is " +
			"nothing to prompt, so the command refuses rather than inventing a " +
			"silent default that deletes things: pass `--force` to say yes up " +
			"front.\n\n" +
			"**Subtasks.** Jira REFUSES to delete an issue that has subtasks " +
			"unless you say to take them too, so `--delete-subtasks` is not an " +
			"optimisation — it is the difference between a refusal and a cascade " +
			"that removes several issues you did not name. Without it, an issue " +
			"with subtasks fails with Jira's own message and nothing is deleted.\n\n" +
			"This is a keyed command: one entry per key at `data.results[]`, in " +
			"the order you gave them. A key that fails does not fail the others, " +
			"which matters here more than for any other write: the issues that " +
			"WERE deleted are gone, and the envelope naming exactly which ones is " +
			"the only record of how far the run got. A key that does not exist " +
			"fails as its own entry rather than stopping the run — see the " +
			"`data.results[]` contract. Total failure, cancellation and timeout " +
			"retain the ordered outcomes in the full failure frame. On a live " +
			"run, empty per-key `data` records a validated HTTP 204 " +
			"acknowledgement; without it the command does not claim deletion. " +
			"Inspect uncertain outcomes and subtasks before retrying; deletion " +
			"is never automatically replayed.\n\n" +
			"**Selecting issues instead of naming them.** `--jql`, `--filter` " +
			"and `--from-file` choose the issues for you, and are refused " +
			"alongside named keys — two ways of saying which issues to delete " +
			"is one too many. A selector always resolves BEFORE the " +
			"confirmation, so the question lists the actual keys rather than " +
			"the query: a query is the thing you may have got wrong, and " +
			"re-reading it is not how you would find out. A match larger than " +
			"100 issues is refused outright rather than trimmed, because a run " +
			"that stopped partway would look exactly like one that finished.\n\n" +
			"`--dry-run` lists what would be deleted and stops, asking nothing " +
			"and contacting nobody. It cannot tell you whether the issues exist " +
			"or whether any of them have subtasks — and with `--jql` or " +
			"`--filter` it cannot tell you WHICH issues either, because finding " +
			"out is a read from the site: it says so and lists nothing. Add " +
			"`--validate-remote` to resolve the query and list the issues " +
			"without deleting anything.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# delete one issue, with a confirmation at a terminal\n" +
			"$ jira issue delete KEY-123\n\n" +
			"# from a script or an agent, where nothing can answer a prompt\n" +
			"$ jira issue delete KEY-123 --force\n\n" +
			"# an issue with subtasks needs this, or Jira refuses\n" +
			"$ jira issue delete KEY-123 --delete-subtasks --force\n\n" +
			"# several at once\n" +
			"$ jira issue delete KEY-123 KEY-124 --force\n\n" +
			"# see what would go, without deleting or asking\n" +
			"$ jira issue delete KEY-123 --dry-run\n\n" +
			"# every issue a query matches, listed in the confirmation before anything goes\n" +
			"$ jira issue delete --jql 'project = KEY AND status = Done'\n\n" +
			"# which issues that query would take, resolved but writing nothing\n" +
			"$ jira issue delete --jql 'project = KEY AND status = Done' --dry-run --validate-remote\n\n" +
			"# a list you wrote out yourself\n" +
			"$ jira issue delete --from-file ./keys.txt --force",
		// Min is 0 because a selector replaces the positionals entirely.
		// The "name something" refusal moves to SelectorFlags.Validate,
		// which can say BOTH routes; cobra's arity check could only ever
		// have said the one it knows about.
		Args: command.ArgSpec{Min: 0, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			// --validate-remote arrives WITH the selectors and only makes
			// sense because of them: resolving a query is a live read, so it
			// is what a preview needs permission to do. Without a selector
			// this command still has nothing to validate remotely.
			command.AddMutationFlags(c, &mut, command.WithForce(), command.WithValidateRemote())
			command.AddSelectorFlags(c, &sel)
			command.AddBoolVar(c, &deleteSubtasks, flagDeleteSubtasks, false,
				"Delete each issue's subtasks too; without it Jira refuses an issue that has any",
				command.FlagOpts{Terse: "Take subtasks"})
			// The hidden alias, bound to the SAME variable — a bool has one
			// value worth giving, so both spellings set the same true and
			// there is nothing for a resolver to reconcile (addForceAlias's
			// own reasoning).
			fs := c.Flags()
			// Raw pflag, deliberately: a HIDDEN alias carries no clib
			// metadata because nothing renders it — it is absent from help,
			// the schema and completions, so there is no Terse for a
			// register-and-extend helper to attach. That is the same shape
			// AddBodyFlags gives its own hidden aliases; the ruleguard
			// cannot see the Hidden assignment two lines down, so the
			// exemption is stated here instead.
			fs.BoolVar(&deleteSubtasks, flagCascade, false, "Alias for --"+flagDeleteSubtasks) //nolint:gocritic // hidden alias: no metadata to attach, see above
			fs.Lookup(flagCascade).Hidden = true
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			// cmd.Flags().Args() is what cobra hands RunE, read one step
			// early — newTransition's own capture, for its reason. Every
			// positional here is a key, so the count goes through as-is.
			if err := sel.Validate(len(cmd.Flags().Args())); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human: renderDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueDeleteOutput, error) {
			return runDelete(ctx, deps, inv, deleteOptions{
				selector:       sel,
				deleteSubtasks: deleteSubtasks,
				force:          mut.Force,
				validateRemote: mut.ValidateRemote,
				parallelism:    keyed.Parallelism,
			})
		},
	})
}

// deleteOptions is runDelete's input beyond the invocation — a struct
// rather than a fifth and sixth positional parameter, for the reason
// composeJQLInput is one: a call site should not have to get six
// same-typed values right by position.
type deleteOptions struct {
	selector       command.SelectorFlags
	deleteSubtasks bool
	force          bool
	validateRemote bool
	parallelism    int
}

// runDelete resolves the targets, confirms once, then deletes every key.
//
// **THE ORDER OF THE FIRST TWO STEPS IS THE SAFETY PROPERTY OF THIS
// COMMAND**, and it is stated here because it is the thing a later edit
// is most likely to break invisibly. Resolution comes first, so the
// confirmation names the issues that are actually about to be deleted. A
// confirmation reachable before resolution would be one of two things,
// both wrong: a dialog echoing `project = X` back at somebody who has
// already read it, or — worse — a dialog naming an empty list while the
// fan-out deletes a hundred issues behind it. TestConfirmationCannotBeReached
// BeforeTheSelectorResolves pins it.
//
// **One confirmation for the whole run, not one per key.** A dialog per
// issue would be unanswerable for a list of ten and would turn a partial
// "no" into a half-deleted set — the caller would have to reason about
// which prompts they had already accepted. One question naming every key
// is both easier to answer and the only version with a single, clear
// outcome.
//
// The rest of the ordering is runCommentDelete's: the preview returns
// before the confirmation, and the confirmation runs before any delete is
// sent. What moved is only the resolution, which now precedes all three —
// including the preview, because a preview that cannot say which issues
// it is previewing is not one.
func runDelete(ctx context.Context, deps Deps, inv command.Invocation, opts deleteOptions) (envelope.IssueDeleteOutput, error) {
	var zero envelope.IssueDeleteOutput

	out := envelope.IssueDeleteOutput{
		MutationState:  envelope.MutationState{DryRun: inv.DryRun},
		DeleteSubtasks: opts.deleteSubtasks,
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
		// Keys is empty exactly when the selector went unresolved, so the
		// preview lists nothing and the warning ResolveTargets raised says
		// why. It is not silently an empty run: Resolved=false is in the
		// envelope beside it.
		out.Results = deletePreviewResults(targets.Keys)
		return out, nil
	}

	if err := confirmIssueDelete(deps.Confirm, inv, targets, opts.deleteSubtasks, opts.force); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("delete canceled — %s untouched", joinPlainKeys(targets.Keys)))
		}
		return zero, err
	}

	client, err := newClient()
	if err != nil {
		return zero, err
	}
	results, err := deleteFanOut(ctx, client, targets.Keys, opts.deleteSubtasks, opts.parallelism)
	out.Results = results
	return out, err
}

// confirmIssueDelete gates the destructive path.
//
// **The policy is confirmDestructive's, and it deliberately is not made
// stricter here.** The tempting move is a second gate for this command
// — a typed confirmation, a count the caller must repeat — on the
// grounds that an issue is worth more than a comment. Rejected, for two
// reasons. A caller who has met `--force` on the other deletes would
// meet a different contract on the one where the stakes are highest,
// which is exactly where surprise is least affordable; and a stricter
// prompt is only a real defense against a person, while every
// destructive invocation this CLI is built for comes from a script that
// would pass whatever token the gate demanded.
//
// What DOES change is the question. It names every issue rather than
// counting them, because a list is the only form in which somebody
// notices the key they did not mean to type — and it says out loud when
// subtasks are coming too, since those are issues the caller never
// named.
func confirmIssueDelete(ask confirmFunc, inv command.Invocation, targets command.Targets, deleteSubtasks, force bool) error {
	subject := joinPlainKeys(targets.Keys)
	caveat := "Jira keeps no trash — the comments, attachments and history go too, and this cannot be undone."
	if deleteSubtasks {
		caveat = "Their SUBTASKS will be deleted as well. " + caveat
	}
	risk := "deleting " + subject + " cannot be undone, so it needs confirmation"
	if targets.Summary != nil {
		// A selector-driven delete says so in BOTH sentences, because the
		// two readers are different: the caveat is read by somebody about
		// to answer, and the risk clause is all a non-interactive caller
		// gets. Neither should have to infer that these keys came from a
		// query rather than from their own command line.
		caveat = fmt.Sprintf("--%s chose these, you did not name them. ", targets.Summary.Kind) + caveat
		risk = fmt.Sprintf("deleting %s, which --%s chose rather than you naming them, cannot be undone, so it needs confirmation",
			subject, targets.Summary.Kind)
	}
	return confirmDestructive(ask, inv, force, destructiveQuestion{
		Subject:     subject,
		Risk:        risk,
		Question:    []string{"Delete ", subject, "?"},
		Caveat:      caveat,
		Affirmative: "Delete",
		Negative:    "Keep",
	})
}

// joinPlainKeys renders a key list for a dialog and for an error — plain
// text, no sinks, because both destinations style their own content and
// a pre-styled span would strand its reset inside them (axis H5).
func joinPlainKeys(keys []string) string {
	switch len(keys) {
	case 0:
		return ""
	case 1:
		return keys[0]
	}
	return fmt.Sprintf("%d issues (%s)", len(keys), joinKeys(keys))
}

// deletePreviewResults builds the per-key entries for a dry run —
// previewResults' shape, for its reason.
func deletePreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueDeleteData] {
	results := make([]envelope.KeyedResult[envelope.IssueDeleteData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueDeleteData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueDeleteData{},
		}
	}
	return results
}

// deleteFanOut deletes every key and assembles the keyed payload.
//
// **A key that does not exist fails as ITS OWN ENTRY**, and the run
// continues. That is the keyed rule applied unchanged, and it is worth
// defending here because the opposite instinct is strong: refusing the
// whole run on a bad key feels safer for a destructive command.
//
// It is not. The keys are independent — deleting KEY-1 has nothing to do
// with whether KEY-2 exists — so a total refusal would make a caller
// delete the good ones in a second run anyway, having learned nothing
// except which key was wrong. And it would be a refusal the CLI cannot
// actually deliver: knowing a key is bad means asking the site, which is
// a read this command does not otherwise make and which would be racy
// against the delete regardless.
//
// What DOES protect the caller from a mistyped key is the confirmation,
// which names every key BEFORE anything is sent — a wrong key is visible
// there, when it is still free.
func deleteFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	deleteSubtasks bool,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueDeleteData], error) {
	outcomes := make([]deleteOutcome, len(keys))
	_, err := command.Spin(ctx, deleteSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = deleteOne(ctx, client, keys[i], deleteSubtasks)
		})
		return struct{}{}, nil
	})
	// Spin joins every worker before assembly, so completed acknowledgements
	// and failures remain attributable even when the command was interrupted.
	results := make([]envelope.KeyedResult[envelope.IssueDeleteData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// deleteOutcome is one key's result plus the raw error behind a failure
// — commentAddOutcome's pairing, for its reason.
type deleteOutcome struct {
	result envelope.KeyedResult[envelope.IssueDeleteData]
	// err is nil exactly when result.OK.
	err error
}

// deleteOne removes one issue.
func deleteOne(ctx context.Context, client core.JiraClient, key string, deleteSubtasks bool) deleteOutcome {
	write, err := client.DeleteIssue(ctx, key, deleteSubtasks)
	var data *envelope.IssueDeleteData
	if write.StatusCode == http.StatusNoContent {
		data = &envelope.IssueDeleteData{}
	} else if err == nil {
		err = core.ErrIssueDeleteResponseInvalid
	}
	if err != nil {
		class := cli.MapError(err)
		return deleteOutcome{
			result: envelope.KeyedResult[envelope.IssueDeleteData]{
				Issue: envelope.IssueRef{Key: key},
				OK:    false,
				Data:  data,
				Err: &envelope.KeyError{
					Code:    class.Code,
					Message: class.Message,
					Hint:    class.Hint,
				},
			},
			err: err,
		}
	}
	return deleteOutcome{result: envelope.KeyedResult[envelope.IssueDeleteData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  data,
	}}
}

// deleteSpinnerLabel is the fan-out's one spinner label. Present tense,
// and correctly so: runDelete returns before the fan-out on a dry run.
func deleteSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Deleting " + keys[0]
	}
	return fmt.Sprintf("Deleting %d issues", len(keys))
}

// renderDelete writes the human view. Preview tense is structural (axis
// H2): the branches share no sentence.
func renderDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueDeleteOutput)
	if !ok {
		return fmt.Errorf("issue.delete: human renderer received %T, want envelope.IssueDeleteOutput", env.Data)
	}
	if out.DryRun {
		renderDeletePreview(v, out)
		return nil
	}
	renderDeleteResults(v, out)
	return nil
}

// renderDeletePreview draws the dry-run view, listing every key rather
// than counting them — the same reason the confirmation does.
func renderDeletePreview(v *output.View, out envelope.IssueDeleteOutput) {
	if renderUnresolvedSelector(v, out.Selector, "delete") {
		return
	}
	keys := weblinkKeys(out.Results)
	v.Line(fmt.Sprintf("Would delete %s.", joinKeys(keys)))
	if out.Selector != nil {
		v.Line(cli.Text(fmt.Sprintf("--%s chose them; they were not named.", out.Selector.Kind)))
	}
	if out.DeleteSubtasks {
		v.Line(cli.Text("Would delete their subtasks too."))
	}
	v.Hint("Nothing was deleted. Drop --dry-run to apply it.")
}

// renderDeleteResults draws the live view: one line per key.
func renderDeleteResults(v *output.View, out envelope.IssueDeleteOutput) {
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if r.OK || r.Data != nil {
			v.Line(key + cli.Text(": deleted."))
		}
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
		}
	}
}
