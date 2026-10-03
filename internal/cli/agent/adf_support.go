package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newADFMatrix() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ADFSupportOutput]{
		Op: "agent.adf-matrix", Use: "adf-matrix", Short: "Discover local ADF authoring, projection and native support",
		Long: "Inspect every node and mark definition in the generated pinned ADF catalog without credentials or Jira requests. " +
			"Context variants have separate rows; definition names and structural bounds identify them. " +
			"Bounds are summaries, not a substitute for full-document schema validation.\n\n" +
			"author describes Markdown emission; render describes a dedicated Markdown projection, including approximations. " +
			"terminal_render separately describes the human issue-view renderer. structural projection carries ordinary structure, " +
			"but attributes, marks and whitespace may still require diagnostics. approximate is inherently lossy; placeholder keeps a node label and child content. " +
			"Unrepresented marks are dropped from the projection with loss diagnostics.\n\n" +
			"preserve means untouched native parse/marshal fidelity. validate and submit require a valid document in its schema context; " +
			"they do not establish that a Jira field accepts it. edit remains false until lossless editing is implemented. " +
			"Unknown future types can be preserved but are outside the pinned validator. JSON carries rows at .data.entries; compact at .entries.",
		Example: "$ jira agent adf-matrix --output=json\n\n" +
			"$ jira agent adf-matrix --jq '.data.entries[] | select(.capabilities.author) | .definition'",
		Human: renderADFMatrix,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.ADFSupportOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.ADFSupportOutput{}, err
			}
			return envelope.ADFSupportOutput{
				SchemaSHA256: adf.SchemaSHA256,
				OfficialURL:  "https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/",
				Entries:      adf.SupportMatrix(),
				Unknown:      envelope.ADFUnknownSupport{PreserveNative: true, NodeProjection: "placeholder", MarkProjection: "dropped"},
			}, nil
		},
	})
}

func renderADFMatrix(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFSupportOutput)
	if !ok {
		return fmt.Errorf("agent: expected ADFSupportOutput, got %T", env.Data)
	}
	v.Section("Local ADF support")
	v.Line("Author: Markdown emission. Terminal: issue-view rendering. Projection: Markdown review.")
	fields := make([]output.Field, 0, len(out.Entries))
	for _, row := range out.Entries {
		fields = append(fields, output.Field{
			Label: cli.Text(row.Definition),
			Value: fmt.Sprintf("author=%t terminal=%t projection=%s", row.Capabilities.Author, row.TerminalRender, cli.Text(row.MarkdownProjection)),
		})
	}
	v.Fields(fields...)
	v.Line("Native preservation and schema validation do not provide lossless editing or Jira field acceptance.")
	return nil
}
