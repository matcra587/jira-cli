// Package adfcmd provides credential-free ADF authoring and review commands.
package adfcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gechr/x/shell"
	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const flagInput = "input"

// New constructs local commands without a client or credential capability.
func New() *cobra.Command {
	group := &cobra.Command{Use: "adf", Short: "Convert and review rich text without contacting Jira", Args: cobra.NoArgs}
	group.AddCommand(newConvert(), newRender())
	return group
}

func newConvert() *cobra.Command {
	var input string
	strict := true
	var bestEffort bool
	return command.MustNew(command.Spec[envelope.ADFConvertOutput]{
		Op: "adf.convert", Use: "convert", Short: "Convert Markdown to a validated native ADF document",
		Passthrough: true,
		Long: "Convert GFM Markdown through the same converter used by issue and comment bodies. " +
			"This local read requires no credentials and makes no Jira request. --input defaults to - for piped stdin; " +
			"a terminal input is refused immediately. Files and stdin share the body size limit.\n\n" +
			"Strict mode is the default: dropped content fails with exit 3 and retains the candidate document and diagnostics. " +
			"--adf-best-effort or --adf-strict=false accepts documented loss. Shape downgrades that retain content still warn. " +
			"Every emitted candidate passes the pinned ADF schema. JSON puts the document at .data.document; compact at .document. " +
			"Diagnostics remain in data.diagnostics in both modes. Extract the document with --jq before passing it to --json-input. " +
			"A Markdown conversion is authoring, not a lossless edit of an existing ADF document.",
		Example: "$ jira adf convert --input notes.md --output=json\n\n" +
			"$ jira adf convert --input notes.md --jq '.data.document'",
		Flags: func(c *cobra.Command) {
			addInput(c, &input, "Markdown")
			command.AddBoolVar(c, &strict, "adf-strict", true, "Refuse conversion that drops authored content", command.FlagOpts{Terse: "Refuse content loss"})
			command.AddBoolVar(c, &bestEffort, "adf-best-effort", false, "Accept reported conversion loss", command.FlagOpts{Terse: "Accept content loss"})
			c.MarkFlagsMutuallyExclusive("adf-strict", "adf-best-effort")
		},
		Human: renderConverted,
		RetainOnError: func(out envelope.ADFConvertOutput) bool {
			return len(out.Document) != 0
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFConvertOutput, error) {
			data, err := readInput(ctx, inv, input)
			if err != nil {
				return envelope.ADFConvertOutput{}, err
			}
			body, err := command.ConvertMarkdown(flagInput, string(data))
			if err != nil {
				return envelope.ADFConvertOutput{}, err
			}
			if err := adf.Validate(body.ADF); err != nil {
				return envelope.ADFConvertOutput{}, fmt.Errorf("adf: converter produced an invalid document: %w", err)
			}
			out := envelope.ADFConvertOutput{Document: body.ADF, Diagnostics: append([]envelope.Warning{}, body.Lossy...)}
			for _, warning := range body.Lossy {
				out.Lossy = out.Lossy || warning.Lossy
			}
			if out.Lossy && strict && !bestEffort {
				err := cli.NewInputError(cli.CodeADFContentLoss, "Markdown conversion drops content; inspect the retained warnings and candidate before accepting loss")
				err.Field = flagInput
				err.Suggestions = []string{"jira adf convert --input " + shell.Quote(input) + " --adf-best-effort --output=json"}
				return out, err
			}
			return out, ctx.Err()
		},
	})
}

func newRender() *cobra.Command {
	var input string
	return command.MustNew(command.Spec[envelope.ADFRenderOutput]{
		Op: "adf.render", Use: "render", Short: "Project native ADF to Markdown with explicit loss diagnostics",
		Long: "Read a native ADF document and return a GFM Markdown review projection without contacting Jira. " +
			"Pass the document itself; issue, fields and body wrappers are not unwrapped. " +
			"Server-originated future nodes are accepted without imposing the older pinned schema. " +
			"Unknown nodes retain a labeled placeholder and their child text.\n\n" +
			"lossy_constructs lists sorted unique node and mark types whose semantics or attributes are approximated. " +
			"Mentions, cards, status, tables, tasks, media and extension attributes cannot survive a Markdown edit round trip. " +
			"Keep the original native document for submissions and editing. JSON carries Markdown at .data.markdown, compact at .markdown. " +
			"Human mode prints a sanitized projection. --input defaults to - and refuses terminal stdin.",
		Example: "$ jira adf render --input document.json --output=json\n\n" +
			"$ jira adf render --input document.json --jq '.data.markdown'",
		Flags: func(c *cobra.Command) { addInput(c, &input, "ADF JSON") },
		Human: renderProjected,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFRenderOutput, error) {
			data, err := readInput(ctx, inv, input)
			if err != nil {
				return envelope.ADFRenderOutput{}, err
			}
			doc, err := adf.ParseDocument(data)
			if err != nil {
				err := cli.NewInputError(cli.CodeFlagValueInvalid, "--input is not a native ADF document: "+err.Error())
				err.Field = flagInput
				return envelope.ADFRenderOutput{}, err
			}
			projection := adf.ToMarkdownLossy(doc)
			return envelope.ADFRenderOutput{
				Markdown: projection.Markdown, Lossy: len(projection.LossyConstructs) != 0,
				LossyConstructs: append([]string{}, projection.LossyConstructs...),
			}, ctx.Err()
		},
	})
}

func addInput(c *cobra.Command, input *string, kind string) {
	command.AddStringVar(c, input, flagInput, command.StdinPath, kind+" file; - reads piped stdin", command.FlagOpts{Terse: "Input file", Placeholder: "file"})
}

func readInput(ctx context.Context, inv command.Invocation, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := command.ReadInputFileContext(ctx, flagInput, path, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if xstrings.IsBlank(string(data)) {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--input is empty; supply Markdown or a native ADF document")
		err.Field = flagInput
		return nil, err
	}
	if !utf8.Valid(data) {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--input must contain valid UTF-8 text")
		err.Field = flagInput
		return nil, err
	}
	return data, nil
}

func renderConverted(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFConvertOutput)
	if !ok {
		return fmt.Errorf("adf: expected ADFConvertOutput, got %T", env.Data)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, out.Document, "", "  "); err != nil {
		return err
	}
	v.Section("ADF candidate")
	for _, line := range strings.Split(formatted.String(), "\n") {
		v.Line(cli.Text(line))
	}
	return nil
}

func renderProjected(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFRenderOutput)
	if !ok {
		return fmt.Errorf("adf: expected ADFRenderOutput, got %T", env.Data)
	}
	v.Section("Markdown projection")
	for _, line := range strings.Split(strings.TrimSuffix(out.Markdown, "\n"), "\n") {
		v.Line(cli.Text(line))
	}
	if out.Lossy {
		v.Fields(output.Field{Label: "Lossy constructs", Value: cli.Text(strings.Join(out.LossyConstructs, ", "))})
	}
	return nil
}
