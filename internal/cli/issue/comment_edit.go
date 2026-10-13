package issue

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// flagClearVisibility removes a comment's read restriction. This
// command's own flag — `comment add` has no use for it, there being no
// prior restriction on a comment being created.
const flagClearVisibility = "clear-visibility"

// newCommentEdit returns `jira issue comment edit`.
//
// THE FIRST MUTATION IN THIS TREE THAT IS NOT KEYED, and the only thing
// that makes it interesting. Everything else here is `comment add` with
// a different verb: the same body registrar, the same compose-then-stop
// preview, the same visibility pair, the same taxonomy. Two decisions
// are this command's own and are argued where they are made:
//
//   - it takes ONE comment on ONE issue and therefore declares no Keyed
//     and registers no keyed surface (see the Long text below);
//   - a body is ALWAYS required, because the endpoint replaces the
//     comment rather than patching it (runCommentEdit).
func newCommentEdit(deps Deps) *cobra.Command {
	var body command.BodyFlags
	// Bare, no options. --force has nothing to skip (editing prompts for
	// no confirmation — it is reversible by editing again, and Jira keeps
	// the previous body in the issue history), --verify has no extra
	// round trip to gate (the PUT's own response carries the stored
	// comment), and --no-readback would suppress a readback this command
	// never makes separately.
	var mut command.MutationFlags
	var visGroup, visRole string
	var clearVisibility bool
	// cmd is captured so Validate can reach the parsed flag set, which
	// alias folding needs and Spec.Validate is not handed — the same
	// capture newCommentAdd makes, for the same reason.
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueCommentEditOutput]{
		Op:         "issue.comment.edit",
		WritesJira: true,
		Identity:   true,
		// NOT Keyed, and command.New enforces the pairing in both
		// directions — a command declaring Keyed without the shared
		// surface is refused, and so is one binding --parallelism while
		// telling the audit it returns a single result. So this line and
		// the absent AddKeyedFlags call below cannot drift apart.
		Passthrough: true,
		Use:         "edit ISSUE_KEY COMMENT_ID",
		Short:       "Edit a comment on an issue",
		Long: "Replace the body of one comment.\n\n" +
			"This command takes exactly one issue and one comment, and it is " +
			"deliberately NOT a `KEY…` command like its `add` sibling. A comment " +
			"id belongs to the issue it was posted on, so a list of keys sharing " +
			"one comment id would either find nothing or find unrelated comments " +
			"that happen to share a number. Get the id from `jira issue comment " +
			"list KEY-123`, or from what `comment add` printed when it posted.\n\n" +
			"The new body comes from exactly one of the three routes `comment " +
			"add` uses, on the same terms: `--markdown` for text on the command " +
			"line, `--markdown-file` to read it from a file (`-` reads standard " +
			"input), or `--json-input` for a native ADF document. Giving two is " +
			"refused rather than merged.\n\n" +
			"**A body is always required, including when all you meant to change " +
			"was the visibility.** Jira's edit endpoint REPLACES the comment " +
			"rather than patching it, so a request carrying no body would ask it " +
			"to store a comment with none. Pass the text you want the comment to " +
			"end up with — this command will not fetch the current body and send " +
			"it back for you, because a round trip that re-encodes somebody " +
			"else's prose is a good way to change it by accident.\n\n" +
			"Visibility follows the same rule from the other side. Naming " +
			"neither `--visibility-group` nor `--visibility-role` leaves the " +
			"comment's CURRENT restriction exactly as it was — it does not make " +
			"the comment public. Use `--clear-visibility` to actually remove a " +
			"restriction, and note that it cannot be combined with either flag " +
			"that sets one.\n\n" +
			"`--dry-run` composes the whole request and stops. It never contacts " +
			"Jira, so `data.body` is byte-identical to what a live run sends, but " +
			"it cannot tell you whether the comment exists or whether you are " +
			"allowed to edit it — Jira only lets you edit your own comments " +
			"unless you hold the \"Edit all comments\" permission.\n\n" +
			"There is no `--force`. An edit is reversible by editing again, and " +
			"Jira keeps the previous body in the issue's history, so there is no " +
			"destructive step to confirm.\n\n" +
			"**Notifications.** Editing a comment notifies its watchers by " +
			"default, which is Jira's own behavior; `--notify=false` asks it " +
			"not to. Unlike `issue edit`, this endpoint attaches no permission " +
			"requirement to that request.\n\n" +
			"Cancellation and timeout retain any validated comment receipt " +
			"returned by the joined write. An invalid acknowledgement reports " +
			"`comment_response_invalid`; inspect the current comment before " +
			"retrying, because the edit may already have been stored.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"writes to one comment on it.",
		Example: "# replace a comment's text\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Deployed to production, not staging.'\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Draft.' --dry-run\n\n" +
			"# new body from a file\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown-file notes.md\n\n" +
			"# a native ADF document, the lossless route\n" +
			"$ jira issue comment edit KEY-123 10042 --json-input comment.json\n\n" +
			"# restrict an existing comment to one group, giving its text again\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Internal note.' --visibility-group developers\n\n" +
			"# take the restriction back off\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Now public.' --clear-visibility\n\n" +
			"# just the stored comment's updated timestamp, as json\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Fixed.' --output=json --jq '.data.comment.updated'\n\n" +
			"# fix a typo without emailing everyone about it\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Fixed.' --notify=false",
		Args: command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{
			{Name: "ISSUE_KEY", Predictor: "issuekey"},
			{Name: "COMMENT_ID"},
		}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddBodyFlags(c, &body)
			command.AddMutationFlags(c, &mut, command.WithNotify())
			command.AddStringVar(c, &visGroup, flagVisibilityGroup, "",
				"Restrict the comment to members of this group",
				command.FlagOpts{Terse: "Group-only", Placeholder: "name"})
			command.AddStringVar(c, &visRole, flagVisibilityRole, "",
				"Restrict the comment to holders of this project role",
				command.FlagOpts{Terse: "Role-only", Placeholder: "name"})
			command.AddBoolVar(c, &clearVisibility, flagClearVisibility, false,
				"Remove the comment's read restriction",
				command.FlagOpts{Terse: "Unrestrict"})
		},
		Validate: func() error {
			// Alias folding FIRST, for newCommentAdd's reason: --body must
			// have become --markdown before the conflict check looks. The
			// notify alias folds here too, and for the same reason one step
			// on: --skip-notify must have become --notify=false before
			// anything reads mut.Notify.
			if err := body.ResolveAliases(cmd.Flags()); err != nil {
				return err
			}
			if err := mut.ResolveNotify(cmd.Flags()); err != nil {
				return err
			}
			if err := body.Conflict(); err != nil {
				return err
			}
			if err := body.RequireBody("an edited comment"); err != nil {
				return err
			}
			if err := visibilityConflict(visGroup, visRole); err != nil {
				return err
			}
			if err := clearVisibilityConflict(clearVisibility, visGroup, visRole); err != nil {
				return err
			}
			return refuseWebOnSingleWrite(cmd)
		},
		Human:         renderCommentEdit,
		RetainOnError: func(out envelope.IssueCommentEditOutput) bool { return out.Comment != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCommentEditOutput, error) {
			return runCommentEdit(ctx, deps, inv, commentEditOptions{
				body:            &body,
				visibility:      visibilityFor(visGroup, visRole),
				clearVisibility: clearVisibility,
				notify:          mut.Notify,
			})
		},
	})
}

// commentEditOptions is what runCommentEdit needs from the flag set,
// gathered into one struct so the handler's signature does not grow a
// parameter per flag — commentAddOptions' own reasoning, and --dry-run is
// absent here for its reason too: it arrives on the Invocation.
type commentEditOptions struct {
	body            *command.BodyFlags
	visibility      *jira.CommentVisibility
	clearVisibility bool
	// notify is --notify, already folded with its hidden alias. It rides
	// the request as a query parameter rather than in the body, which is
	// why it is not part of jira.UpdateCommentRequest.
	notify bool
}

// clearVisibilityConflict refuses --clear-visibility alongside a flag
// that sets one.
//
// The two ask for opposite things about the same wire field, and the
// request has exactly one slot for it — so a CLI accepting both would
// have to silently drop one, and the caller would find out by
// discovering who could read their comment. That is visibilityConflict's
// argument, and it lands harder here: the two failure modes are
// "restricted when you asked for public" and its inverse, and one of
// them leaks.
func clearVisibilityConflict(clear bool, group, role string) error {
	if !clear || (group == "" && role == "") {
		return nil
	}
	set := flagVisibilityGroup
	if group == "" {
		set = flagVisibilityRole
	}
	e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
		"--%s removes the comment's restriction and --%s adds one, and the request carries only one answer — pick one and drop the other",
		flagClearVisibility, set,
	))
	e.Field = flagClearVisibility
	return e
}

// refuseWebOnSingleWrite refuses --web on a single-target mutation.
//
// A third sibling of refuseWebOnKeyedRead (link.go) and
// refuseWebOnKeyedWrite (comment_add.go), and it exists because their
// shared sentence is the one thing that is NOT true here: those refuse
// the flag because the command has N subjects and --web opens one page.
// This command has exactly one. The reason it is refused anyway is
// different and worth stating rather than borrowing — a browse page
// shows an issue, and what this command changes is one comment on it, so
// opening the page would neither confirm the write nor be the thing the
// caller asked about. issue.go's own doc comment sets the rule this
// follows: a subcommand under this group that cannot honor --web must
// refuse an explicit one rather than accept and ignore it.
//
// CodeFlagNotApplicable, like its siblings: the flag is real and spelled
// right, so the fix is to drop it.
func refuseWebOnSingleWrite(cmd *cobra.Command) error {
	if !isWeb(cmd) {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--web opens an issue page, and this command writes to one comment on it — run `jira issue view KEY --web` to look at the issue itself")
	e.Field = flagWeb
	return e
}

// runCommentEdit composes the request and either previews it or sends
// it.
//
// **The preview builds no client**, which is runCommentAdd's property
// adopted unchanged and for its reason: a dry run returns before
// deps.JiraClient() is ever called, so the preview cannot reach the
// network because it never obtains the thing that could.
//
// **The body is resolved before the branch**, so the preview reports the
// same bytes a live run sends rather than a reconstruction of them. With
// one key there is no fan-out for compose-once to protect, but the
// preview-fidelity half of that argument is exactly as load-bearing.
//
// **Nothing here fetches the comment first.** The command sends the body
// it was given and lets Jira decide whether the id exists; a read-then-
// write would cost a round trip to produce a worse error than the 404
// the PUT already returns, and would tempt a future edit into
// re-encoding a body it merely read.
func runCommentEdit(ctx context.Context, deps Deps, inv command.Invocation, opts commentEditOptions) (envelope.IssueCommentEditOutput, error) {
	var zero envelope.IssueCommentEditOutput

	key, commentID := inv.Args[0], inv.Args[1]
	body, err := opts.body.Resolve(ctx, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}
	req := jira.UpdateCommentRequest{
		Body:            body.ADF,
		Visibility:      opts.visibility,
		ClearVisibility: opts.clearVisibility,
	}

	out := envelope.IssueCommentEditOutput{
		MutationState:     envelope.MutationState{DryRun: inv.DryRun},
		Notify:            opts.notify,
		CommentID:         commentID,
		Body:              body.ADF,
		BodyRoute:         body.Route,
		Visibility:        visibilityView(opts.visibility),
		VisibilityCleared: opts.clearVisibility,
	}
	out.Issue.Key = key

	if inv.DryRun {
		return out, nil
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	write, err := command.Spin(ctx, "Editing comment "+commentID+" on "+key, func(ctx context.Context) (core.CommentWrite, error) {
		return client.UpdateComment(ctx, key, commentID, req, opts.notify)
	})
	if receiptErr := core.ValidateCommentReceipt(write.Comment, commentID); receiptErr == nil {
		view := commentViews([]jira.Comment{*write.Comment})[0]
		out.Comment = &view
	} else if err == nil {
		return zero, receiptErr
	}
	// A joined producer may carry a valid acknowledgement after cancellation.
	// Keep that receipt without changing the command's interruption error.
	return out, err
}

// renderCommentEdit writes the human view.
//
// Preview tense is structural, not a phrasing habit (axis H2): the
// preview and the live view are separate functions with no shared
// sentence, so a future edit to the live wording cannot leak a
// past-tense claim into the preview — renderCommentAdd's own split, for
// its reason.
func renderCommentEdit(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCommentEditOutput)
	if !ok {
		return fmt.Errorf("issue.comment.edit: human renderer received %T, want envelope.IssueCommentEditOutput", env.Data)
	}
	if out.DryRun {
		renderCommentEditPreview(v, out)
		return nil
	}
	renderCommentEditResult(v, out)
	return nil
}

// renderCommentEditPreview draws the dry-run view. Every verb is
// conditional, and each visibility line appears only when that change
// was actually asked for — saying "would leave the restriction alone"
// on every run would bury the one line that reports a real change.
func renderCommentEditPreview(v *output.View, out envelope.IssueCommentEditOutput) {
	// cli.Entity for the identities and cli.Text for the prose, as
	// SIBLING spans concatenated into one Line argument rather than
	// nested — each carries its own reset and the next opens a fresh
	// style, which is the shape axis H5 requires.
	v.Line(cli.Text("Would replace comment ") + cli.Entity(out.CommentID) +
		cli.Text(" on ") + cli.Entity(out.Issue.Key) + cli.Text("."))
	if out.Visibility != nil {
		v.Line(cli.Text(fmt.Sprintf("Would restrict it to the %q %s.", out.Visibility.Value, out.Visibility.Type)))
	}
	if out.VisibilityCleared {
		v.Line(cli.Text("Would remove its read restriction."))
	}
	v.Hint("Nothing was sent. Drop --dry-run to apply it.")
}

// renderCommentEditResult draws the live view. It reports the comment's
// stored `updated` timestamp rather than merely saying "done", because
// that is the one fact in the response a caller cannot already know —
// everything else they typed.
func renderCommentEditResult(v *output.View, out envelope.IssueCommentEditOutput) {
	v.Line(cli.Entity(out.CommentID) + cli.Text(" on ") + cli.Entity(out.Issue.Key) + cli.Text(": comment edited."))
	if out.VisibilityCleared {
		v.Line(cli.Text("Read restriction removed."))
	}
	if out.Visibility != nil {
		v.Line(cli.Text(fmt.Sprintf("Restricted to the %q %s.", out.Visibility.Value, out.Visibility.Type)))
	}
	if out.Comment != nil && out.Comment.Updated != "" {
		// cli.Text before cli.Time, because cli.Time's own unparsable-input
		// fallback returns its argument AS GIVEN — a malformed `updated`
		// must not reach the terminal unsanitized through that path. The
		// same pairing renderCommentList makes for a comment header.
		v.Line(cli.Text("Updated ") + cli.Time(cli.Text(out.Comment.Updated)) + cli.Text("."))
	}
}
