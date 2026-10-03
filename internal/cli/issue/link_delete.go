package issue

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newLinkDelete returns `jira issue link delete`.
//
// **It takes a link id and NO issue key**, which departs from the layout
// design's `KEY LINK_ID` and from every other single-target verb in this
// tree. The reason is the endpoint: DELETE /rest/api/3/issueLink/{linkId}
// declares exactly one parameter, because a link id is unique across the
// site rather than scoped to an issue. A key positional here would be an
// argument the command reads and cannot send — so a caller who passed
// the wrong one would watch the link disappear anyway, having been given
// every reason to believe the key was checked. This CLI already refuses
// that shape for flags (command.WithForce's own doc comment: a flag that
// does nothing is worse than an absent one), and a positional is worse
// still, being harder to leave out.
//
// The cost is real and worth naming: `link list` prints ids per issue, so
// a caller who copies the wrong row deletes the wrong link with nothing
// to catch them. Verifying would mean a GET of the link before the
// DELETE, which is a second round trip to protect against a copy-paste
// slip — and one this command deliberately does not make, because the
// same slip is possible with the key present.
func newLinkDelete(deps Deps) *cobra.Command {
	// Bare, no options. --force has nothing to skip and the reasoning is
	// worth contrasting with `comment delete`, which does take one: a
	// deleted comment is unrecoverable prose, while a deleted link is a
	// relationship `jira issue link create` puts back in one command. The
	// undo is what decides whether a confirmation earns its keystroke.
	var mut command.MutationFlags
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueLinkDeleteOutput]{
		Op:         "issue.link.delete",
		WritesJira: true,
		// NOT Identity, and this is the only issue-domain command that
		// declares none: there is no issue to name (see
		// envelope.IssueLinkDeleteOutput). Declaring it would promise a
		// data.issue slot this operation cannot fill.
		Use:   "delete LINK_ID",
		Short: "Delete a link between issues",
		Long: "Remove one link between two issues.\n\n" +
			"**It takes the link's id and no issue key.** A link id is unique " +
			"across the whole site, and Jira's delete endpoint takes nothing " +
			"else — so asking you for a key as well would be asking for " +
			"something this command cannot check or send. Get the id from `jira " +
			"issue link list KEY-123`, which prints one per link.\n\n" +
			"Both issues stop seeing the link: a link belongs to the pair, not " +
			"to whichever end you happened to look at.\n\n" +
			"There is no `--force`. A link is a relationship rather than " +
			"content, and `jira issue link create` puts an unwanted deletion " +
			"back in one command — unlike `jira issue comment delete`, which " +
			"destroys prose and therefore does ask.\n\n" +
			"`--dry-run` composes the request and stops. It never contacts " +
			"Jira, so it cannot tell you whether the link exists or which " +
			"issues it joins.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"is not scoped to an issue at all.",
		Example: "# remove a link by id\n" +
			"$ jira issue link delete 10042\n\n" +
			"# find the id first\n" +
			"$ jira issue link list KEY-123 --output=json --jq '.data.results[].data.links[] | {id, relation, key: .issue.key}'\n\n" +
			"# check what would go, without deleting\n" +
			"$ jira issue link delete 10042 --dry-run",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "LINK_ID"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddMutationFlags(c, &mut)
		},
		Validate: func() error {
			return refuseWebOnLinkDelete(cmd)
		},
		Human: renderLinkDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueLinkDeleteOutput, error) {
			return runLinkDelete(ctx, deps, inv)
		},
	})
}

// refuseWebOnLinkDelete refuses --web on a command with no issue.
//
// A fourth sibling of the refusals in link.go, comment_add.go and
// comment_edit.go, and the only one whose reason is not about arity: the
// others have too many subjects or a subject a browse page does not
// show, while this one has no issue at all. There is nothing for --web
// to open, not even in principle.
func refuseWebOnLinkDelete(cmd *cobra.Command) error {
	if !isWeb(cmd) {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--web opens an issue page, and this command takes a link id rather than an issue — run `jira issue link list KEY` to see the link in context")
	e.Field = flagWeb
	return e
}

// runLinkDelete previews or removes one link.
//
// The preview builds no client, which is runCommentAdd's property
// unchanged and for its reason: a dry run returns before
// deps.JiraClient() is ever called, so it cannot reach the network
// because it never obtains the thing that could.
func runLinkDelete(ctx context.Context, deps Deps, inv command.Invocation) (envelope.IssueLinkDeleteOutput, error) {
	var zero envelope.IssueLinkDeleteOutput

	linkID := inv.Args[0]
	out := envelope.IssueLinkDeleteOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		LinkID:        linkID,
	}
	if inv.DryRun {
		return out, nil
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	if _, err := command.Spin(ctx, "Deleting link "+linkID, func(ctx context.Context) (core.LinkDeleteWrite, error) {
		return client.DeleteIssueLink(ctx, linkID)
	}); err != nil {
		return zero, err
	}
	return out, nil
}

// renderLinkDelete writes the human view: one line, in the tense of
// whichever path ran. Preview tense is structural (axis H2) — the two
// sentences are built in separate branches with no shared fragment.
func renderLinkDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueLinkDeleteOutput)
	if !ok {
		return fmt.Errorf("issue.link.delete: human renderer received %T, want envelope.IssueLinkDeleteOutput", env.Data)
	}
	// cli.Entity for the id, cli.Text for the prose, as SIBLING spans on
	// one line rather than nested (axis H5).
	id := cli.Entity(out.LinkID)
	if out.DryRun {
		v.Line(cli.Text("Would delete link ") + id + cli.Text("."))
		v.Hint("Nothing was deleted. Drop --dry-run to apply it.")
		return nil
	}
	v.Line(cli.Text("Deleted link ") + id + cli.Text("."))
	return nil
}
