package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newCommentDelete returns `jira issue comment delete`.
//
// THE FIRST COMMAND IN THIS TREE THAT DESTROYS SOMETHING ON THE SERVER,
// so the confirmation shape it takes is contract for the deletes that
// follow (issue delete, link delete, attachment delete). The argument
// for that shape is in confirmCommentDelete, where it is made; what
// matters at this level is that the answer is NOT "prompt, and let a
// script find out the hard way", and NOT "never ask" either.
func newCommentDelete(deps Deps) *cobra.Command {
	// WithForce and nothing else. --validate-remote has nothing to check
	// (there is no value to validate against the site — the id either
	// exists or it does not, which the DELETE itself answers), --verify
	// has nothing to read back (the comment is gone, so a readback is a
	// 404 by design), and --no-readback would suppress a readback this
	// command never makes.
	var mut command.MutationFlags
	// cmd is captured so Validate can ask it isWeb(cmd) — --web is a
	// PERSISTENT flag on the `issue` group, so this command inherits it
	// whether or not it can honor one.
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueCommentDeleteOutput]{
		Op:         "issue.comment.delete",
		WritesJira: true,
		Identity:   true,
		RetainOnError: func(out envelope.IssueCommentDeleteOutput) bool {
			return out.CommentID != ""
		},
		// NOT Keyed, for newCommentEdit's reason: a comment id belongs to
		// the issue it was posted on. NOT Passthrough either, and unlike
		// its edit sibling that is not a judgment call — a 204 carries no
		// tenant data for a boundary to preserve.
		Use:   "delete ISSUE_KEY COMMENT_ID",
		Short: "Delete a comment from an issue",
		Long: "Remove one comment from an issue.\n\n" +
			"**This cannot be undone.** Jira keeps no trash for comments: once " +
			"the delete lands, the text is gone from the API and from the issue " +
			"history alike, and no permission or endpoint brings it back. That is " +
			"why this is the one issue command that asks before acting.\n\n" +
			"It asks only when there is somebody to ask. At a terminal you get a " +
			"yes/no dialog naming the issue and the comment. Everywhere else — a " +
			"script, a pipeline, an agent — there is nothing to prompt, so the " +
			"command refuses rather than inventing a silent default that deletes " +
			"things: pass `--force` to say yes up front. `--force` works at a " +
			"terminal too, and skips the dialog there.\n\n" +
			"This command takes exactly one issue and one comment, and is " +
			"deliberately NOT a `KEY…` command: a comment id belongs to the issue " +
			"it was posted on, so a list of keys sharing one id would either find " +
			"nothing or find unrelated comments that happen to share a number. " +
			"Get the id from `jira issue comment list KEY-123`.\n\n" +
			"`--dry-run` composes the request and stops, deleting nothing and " +
			"asking nothing — there is no destructive step for a preview to " +
			"confirm. It never contacts Jira, so it cannot tell you whether the " +
			"comment exists or whether you may delete it; Jira only lets you " +
			"delete your own comments unless you hold the \"Delete all comments\" " +
			"permission.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"writes to one comment on it.\n\n" +
			"A validated HTTP 204 acknowledgement survives cancellation or " +
			"timeout in the full failure frame. Without that acknowledgement, " +
			"the command does not claim deletion. Inspect the comment before " +
			"retrying an uncertain write; deletion is never automatically replayed.",
		Example: "# delete a comment, with a confirmation at a terminal\n" +
			"$ jira issue comment delete KEY-123 10042\n\n" +
			"# from a script or an agent, where nothing can answer a prompt\n" +
			"$ jira issue comment delete KEY-123 10042 --force\n\n" +
			"# check what would go, without deleting or asking\n" +
			"$ jira issue comment delete KEY-123 10042 --dry-run",
		Args: command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{
			{Name: "ISSUE_KEY", Predictor: "issuekey"},
			{Name: "COMMENT_ID"},
		}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddMutationFlags(c, &mut, command.WithForce())
		},
		Validate: func() error {
			return refuseWebOnSingleWrite(cmd)
		},
		Human: renderCommentDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCommentDeleteOutput, error) {
			return runCommentDelete(ctx, deps, inv, mut.Force)
		},
	})
}

// runCommentDelete confirms, then deletes.
//
// **The preview builds no client and asks no question.** It returns
// before deps.JiraClient() is called, which is what "local-only, never
// contacts Jira" has to mean in code rather than in a doc comment
// (runCommentAdd's own property), and before the confirmation, which
// follows from the same fact: a preview destroys nothing, so there is
// nothing to confirm and a dialog would be theater.
//
// **The confirmation runs before the client is built**, not after.
// Building one can open the credential store, which on a macOS keychain
// is itself a prompt — so a caller who is about to say no should not be
// asked for their keychain password first.
func runCommentDelete(ctx context.Context, deps Deps, inv command.Invocation, force bool) (envelope.IssueCommentDeleteOutput, error) {
	var zero envelope.IssueCommentDeleteOutput

	key, commentID := inv.Args[0], inv.Args[1]
	out := envelope.IssueCommentDeleteOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		CommentID:     commentID,
	}
	out.Issue.Key = key

	if inv.DryRun {
		return out, nil
	}

	if err := confirmCommentDelete(ctx, deps.Confirm, inv, key, commentID, force); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("delete canceled — comment %s on %s is untouched", commentID, key))
		}
		return zero, err
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	write, err := command.Spin(ctx, "Deleting comment "+commentID+" on "+key, func(ctx context.Context) (core.CommentDeleteWrite, error) {
		return client.DeleteComment(ctx, key, commentID)
	})
	if write.StatusCode == http.StatusNoContent {
		return out, err
	}
	if err != nil {
		return zero, err
	}
	return zero, fmt.Errorf("%w: comment deletion has no HTTP 204 acknowledgement", core.ErrCommentResponseInvalid)
}

// confirmCommentDelete gates the destructive path.
//
// The POLICY — when to ask, when to refuse, what the refusal carries —
// lives in confirmDestructive (confirm.go), shared with `issue
// attachment delete`. What stays here is this command's own question,
// because a dialog that cannot name what it is about to destroy is not
// worth drawing, and "comment" and "attachment" are not interchangeable
// words to a person about to answer one.
func confirmCommentDelete(ctx context.Context, ask confirmFunc, inv command.Invocation, key, commentID string, force bool) error {
	return confirmDestructive(ctx, ask, inv, force, destructiveQuestion{
		Subject:  fmt.Sprintf("comment %s on %s", commentID, key),
		Risk:     fmt.Sprintf("deleting comment %s on %s cannot be undone, so it needs confirmation", commentID, key),
		Question: []string{"Delete comment ", commentID, " on ", key, "?"},
		// The values are the issue and the id, and the caveat says the
		// thing the values cannot: that there is no way back. "Are you
		// sure?" is not a question anyone can answer well; "this cannot be
		// undone" is.
		Caveat:      "Jira keeps no trash for comments — this cannot be undone.",
		Affirmative: "Delete",
		Negative:    "Keep",
	})
}

// renderCommentDelete writes the human view: one line, in the tense of
// whichever path ran.
//
// Preview tense is structural here as everywhere else (axis H2) — the
// two sentences are built in separate branches with no shared fragment,
// so an edit to the live wording cannot leak a past-tense claim into the
// preview.
func renderCommentDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCommentDeleteOutput)
	if !ok {
		return fmt.Errorf("issue.comment.delete: human renderer received %T, want envelope.IssueCommentDeleteOutput", env.Data)
	}
	// cli.Entity for the identities, cli.Text for the connectives, as
	// SIBLING spans on one line rather than nested (axis H5).
	id, key := cli.Entity(out.CommentID), cli.Entity(out.Issue.Key)
	if out.DryRun {
		v.Line(cli.Text("Would delete comment ") + id + cli.Text(" on ") + key + cli.Text("."))
		v.Hint("Nothing was deleted. Drop --dry-run to apply it.")
		return nil
	}
	v.Line(cli.Text("Deleted comment ") + id + cli.Text(" on ") + key + cli.Text("."))
	return nil
}
