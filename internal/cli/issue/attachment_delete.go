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

// newAttachmentDelete returns `jira issue attachment delete`.
//
// The SECOND destructive command in this tree, and it deliberately
// invents nothing: the confirmation is `issue comment delete`'s, reached
// through the same shared gate (confirmDestructive), so the two behave
// identically and a caller who learned one knows the other.
//
// It takes an ATTACHMENT ID and no issue key, for `link delete`'s
// reason: DELETE /rest/api/3/attachment/{id} declares one parameter.
func newAttachmentDelete(deps Deps) *cobra.Command {
	// WithForce and nothing else — the same footprint `comment delete`
	// takes, for the same reason: there is nothing to validate remotely,
	// nothing to read back, and a confirmation to skip.
	var mut command.MutationFlags
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueAttachmentDeleteOutput]{
		Op:            "issue.attachment.delete",
		WritesJira:    true,
		RetainOnError: func(out envelope.IssueAttachmentDeleteOutput) bool { return out.AttachmentID != "" },
		// NOT Identity, like `link delete` and for its reason: an
		// attachment id is site-wide, so this operation names no issue and
		// declaring Identity would promise a data.issue it cannot fill.
		Use:   "delete ATTACHMENT_ID",
		Short: "Delete an attachment",
		Long: "Remove one attachment from the issue it is on.\n\n" +
			"**This cannot be undone.** Jira keeps no trash for attachments: " +
			"once the delete lands the file is gone from the API, and no " +
			"permission or endpoint brings it back. That is why this command " +
			"asks before acting, exactly as `jira issue comment delete` does.\n\n" +
			"It asks only when there is somebody to ask. At a terminal you get a " +
			"yes/no dialog naming the attachment. Everywhere else — a script, a " +
			"pipeline, an agent — there is nothing to prompt, so the command " +
			"refuses rather than inventing a silent default that deletes things: " +
			"pass `--force` to say yes up front.\n\n" +
			"It takes the attachment's id and no issue key. An attachment id is " +
			"unique across the whole site and Jira's delete endpoint takes " +
			"nothing else. Get the id from `jira issue attachment list " +
			"KEY-123`.\n\n" +
			"To keep a copy first, `jira issue attachment download` writes it to " +
			"disk — and doing that BEFORE this is the only undo there is.\n\n" +
			"`--dry-run` composes the request and stops, deleting nothing and " +
			"asking nothing. It never contacts Jira, so it cannot tell you " +
			"whether the attachment exists or what it is called.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"is not scoped to an issue at all.\n\n" +
			"A validated HTTP 204 acknowledgement survives cancellation or " +
			"timeout in the full failure frame. Without that acknowledgement, " +
			"the command does not claim deletion. Inspect the attachment before " +
			"retrying an uncertain write; deletion is never automatically replayed.",
		Example: "# delete an attachment, with a confirmation at a terminal\n" +
			"$ jira issue attachment delete 10042\n\n" +
			"# from a script or an agent, where nothing can answer a prompt\n" +
			"$ jira issue attachment delete 10042 --force\n\n" +
			"# keep a copy first\n" +
			"$ jira issue attachment download 10042 && jira issue attachment delete 10042 --force\n\n" +
			"# check what would go, without deleting or asking\n" +
			"$ jira issue attachment delete 10042 --dry-run",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ATTACHMENT_ID"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddMutationFlags(c, &mut, command.WithForce())
		},
		Validate: func() error {
			return refuseWebOnLinkDelete(cmd)
		},
		Human: renderAttachmentDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueAttachmentDeleteOutput, error) {
			return runAttachmentDelete(ctx, deps, inv, mut.Force)
		},
	})
}

// runAttachmentDelete confirms, then deletes.
//
// The ordering is runCommentDelete's, unchanged and for its reasons: the
// preview returns before both the confirmation and the client, and the
// confirmation runs before the client is built so a caller about to say
// no is not asked for their keychain password first.
func runAttachmentDelete(ctx context.Context, deps Deps, inv command.Invocation, force bool) (envelope.IssueAttachmentDeleteOutput, error) {
	var zero envelope.IssueAttachmentDeleteOutput

	id := inv.Args[0]
	out := envelope.IssueAttachmentDeleteOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		AttachmentID:  id,
	}
	if inv.DryRun {
		return out, nil
	}

	if err := confirmAttachmentDelete(ctx, deps.Confirm, inv, id, force); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("delete canceled — attachment %s is untouched", id))
		}
		return zero, err
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	write, err := command.Spin(ctx, "Deleting attachment "+id, func(ctx context.Context) (core.AttachmentDeleteWrite, error) {
		return client.DeleteAttachment(ctx, id)
	})
	if write.StatusCode == http.StatusNoContent {
		return out, err
	}
	if err != nil {
		return zero, err
	}
	return zero, fmt.Errorf("%w: attachment deletion has no HTTP 204 acknowledgement", core.ErrAttachmentResponseInvalid)
}

// confirmAttachmentDelete gates the destructive path.
//
// It reuses `comment delete`'s gate wholesale rather than restating the
// argument: confirmDestructive owns when to ask, when to refuse and what
// code the refusal carries, and the whole reason it was extracted is
// that a second destructive command must not get a second answer to
// those questions.
//
// What is NOT shared is the question itself. This dialog names an
// attachment where that one names a comment, and its caveat says the
// thing that is specifically true here — that downloading first is the
// only way to keep a copy, which is advice `comment delete` has no
// equivalent of.
func confirmAttachmentDelete(ctx context.Context, ask confirmFunc, inv command.Invocation, id string, force bool) error {
	return confirmDestructive(ctx, ask, inv, force, destructiveQuestion{
		Subject:     "attachment " + id,
		Risk:        "deleting attachment " + id + " cannot be undone, so it needs confirmation",
		Question:    []string{"Delete attachment ", id, "?"},
		Caveat:      "Jira keeps no trash for attachments — this cannot be undone. Download it first if you might want it back.",
		Affirmative: "Delete",
		Negative:    "Keep",
	})
}

// renderAttachmentDelete writes the human view: one line, in the tense
// of whichever path ran (axis H2 — the two sentences share no fragment).
func renderAttachmentDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueAttachmentDeleteOutput)
	if !ok {
		return fmt.Errorf("issue.attachment.delete: human renderer received %T, want envelope.IssueAttachmentDeleteOutput", env.Data)
	}
	// cli.Entity for the id, cli.Text for the prose, as SIBLING spans on
	// one line rather than nested (axis H5).
	id := cli.Entity(out.AttachmentID)
	if out.DryRun {
		v.Line(cli.Text("Would delete attachment ") + id + cli.Text("."))
		v.Hint("Nothing was deleted. Drop --dry-run to apply it.")
		return nil
	}
	v.Line(cli.Text("Deleted attachment ") + id + cli.Text("."))
	return nil
}
