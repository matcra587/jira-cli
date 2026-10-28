package issue

import (
	"context"
	"fmt"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newEditPrepare(deps Deps) *cobra.Command {
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.IssueEditPrepareOutput]{
		Op: "issue.edit.prepare", Identity: true, Passthrough: true,
		Use: "prepare ISSUE_KEY", Short: "Prepare a bound native description for lossless editing",
		Long: "Read one issue's native description and prepare anchored Markdown.\n\n" +
			"Keep `data.snapshot` with `data.markdown`: the snapshot retains the exact native ADF, " +
			"public site/account/API route and immutable issue ID. `snapshot.description_bytes` retains the exact source as base64; its adjacent native JSON view may compact whitespace. Reopening checks their agreement and uses the byte preimage. An explicit null description stays null; " +
			"an absent description or incomplete native identity is refused. Unknown nodes, attributes, " +
			"marks and root extensions survive in the snapshot. Editable regions may be changed, " +
			"deleted or reordered; protected regions must remain unchanged and present once.\n\n" +
			"This is a read and works in read-only mode. It opens no editor, creates no editing artifacts, " +
			"prompts for nothing and does not write Jira. The snapshot and native updated timestamp " +
			"are observations, never a server-enforced write condition. A later merge still needs " +
			"a server-enforced condition to prevent lost updates. The TUI can review an unconditional native-preserving replacement; a later concurrent writer can still race its final read.\n\n" +
			"JSON returns the complete artifact under `data`; compact returns it directly. " +
			"Use `--jq` to extract one member. `--web` is refused because a browser page cannot " +
			"supply this native editing snapshot.",
		Example: "$ jira issue edit prepare KEY-123 --output=json\n\n" +
			"# Save the complete native editing artifact\n" +
			"$ jira issue edit prepare KEY-123 --output=compact > description-edit.json\n\n" +
			"# Inspect the anchored Markdown\n" +
			"$ jira issue edit prepare KEY-123 --jq '.data.markdown'",
		Args:  command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) { cmd = c },
		Validate: func() error {
			if isWeb(cmd) {
				err := cli.NewInputError(cli.CodeFlagNotApplicable, "--web cannot prepare a native editing snapshot")
				err.Field = flagWeb
				return err
			}
			return nil
		},
		Human: renderIssueEditPrepared,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditPrepareOutput, error) {
			var zero envelope.IssueEditPrepareOutput
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			client, err := deps.JiraClient(ctx)
			if err != nil {
				return zero, err
			}
			reader, ok := client.(core.IssueDescriptionClient)
			if !ok {
				return zero, core.ErrIssueDescriptionUnavailable
			}
			return command.Spin(ctx, "Preparing native description", func(taskCtx context.Context) (envelope.IssueEditPrepareOutput, error) {
				session, _, err := core.PrepareIssueDescription(taskCtx, reader, inv.Args[0])
				if err != nil {
					return zero, err
				}
				snapshot := session.Snapshot()
				regions := xslices.Map(session.Regions(), func(region adf.EditRegion) envelope.ADFEditRegion {
					return envelope.ADFEditRegion{ID: region.ID, NodeType: region.NodeType, Protected: region.Protected}
				})
				if regions == nil {
					regions = []envelope.ADFEditRegion{}
				}
				return envelope.IssueEditPrepareOutput{Issue: envelope.IssueRef{ID: snapshot.IssueID, Key: snapshot.IssueKey}, Snapshot: snapshot, Markdown: session.Markdown(), Regions: regions}, nil
			})
		},
	})
}

func renderIssueEditPrepared(view *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueEditPrepareOutput)
	if !ok {
		return fmt.Errorf("issue.edit.prepare: unexpected payload %T", env.Data)
	}
	view.Fields(output.Field{Label: "Issue", Value: cli.Entity(out.Issue.Key)}, output.Field{Label: "Site", Value: cli.Text(out.Snapshot.Site)}, output.Field{Label: "Account", Value: cli.Text(out.Snapshot.Account)})
	view.Section("Anchored description")
	view.Block(cli.Text(out.Markdown))
	view.Hint("Keep the native snapshot with these anchors; preparation has not changed Jira.")
	return nil
}
