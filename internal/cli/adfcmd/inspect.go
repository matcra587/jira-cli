package adfcmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const nativeInputHelp = "Read the complete native ADF document itself, without issue/fields/body wrappers. --input defaults to - for piped stdin and refuses terminal input. Files and stdin share the body size limit. No credentials, Jira requests or file modifications. Local schema conformance does not establish Jira field acceptance. Invalid JSON or schema content exits 3 and retains diagnostics, including in compact mode. Empty instance_location names the document root; keyword_location identifies the failing schema branch. Unknown server nodes are outside the pin; keep their original native bytes for preservation. "

func newValidate() *cobra.Command {
	var input string
	return command.MustNew(command.Spec[envelope.ADFValidateOutput]{
		Op: "adf.validate", Use: "validate", Short: "Validate a native document against the pinned ADF schema",
		Long:          nativeInputHelp + "JSON carries the report at .data; compact returns the report directly. Validation never converts or repairs the document.",
		Example:       "$ jira adf validate --input document.json --output=json\n\n$ jira adf validate --input document.json --jq '.data.valid'",
		Flags:         func(c *cobra.Command) { addInput(c, &input, "ADF JSON") },
		Human:         renderValidated,
		RetainOnError: func(out envelope.ADFValidateOutput) bool { return out.Diagnostics != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFValidateOutput, error) {
			data, err := readInput(ctx, inv, input)
			if err != nil {
				return envelope.ADFValidateOutput{}, err
			}
			diagnostics, err := adf.ValidateDiagnostics(data)
			out := envelope.ADFValidateOutput{SchemaSHA256: adf.SchemaSHA256, Valid: err == nil, Diagnostics: diagnostics}
			if err != nil {
				return out, inspectionError(err, diagnostics)
			}
			return out, ctx.Err()
		},
	})
}

func newLint() *cobra.Command {
	var input string
	return command.MustNew(command.Spec[envelope.ADFLintOutput]{
		Op: "adf.lint", Use: "lint", Short: "Inspect native schema errors and Markdown projection risks",
		Long:          nativeInputHelp + "Also reports the existing Markdown renderer's sorted unique lossy_constructs. Valid rich documents with projection warnings exit zero; lossy=false describes that projection only, not lossless editing or concurrency. projection_available=false identifies input that could not be analyzed; lossy=false alone is not a clean report. Malformed input may prevent projection analysis. projection_diagnostic explains an unavailable projection even when the schema accepts the document. No automatic fixes are applied.",
		Example:       "$ jira adf lint --input document.json --output=json\n\n$ jira adf lint --input document.json --jq '.data.lossy_constructs'",
		Flags:         func(c *cobra.Command) { addInput(c, &input, "ADF JSON") },
		Human:         renderLinted,
		RetainOnError: func(out envelope.ADFLintOutput) bool { return out.Diagnostics != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFLintOutput, error) {
			data, err := readInput(ctx, inv, input)
			if err != nil {
				return envelope.ADFLintOutput{}, err
			}
			diagnostics, validationErr := adf.ValidateDiagnostics(data)
			out := envelope.ADFLintOutput{SchemaSHA256: adf.SchemaSHA256, Valid: validationErr == nil, Diagnostics: diagnostics, LossyConstructs: []string{}}
			if diagnostics == nil {
				return out, validationErr
			}
			doc, parseErr := adf.ParseDocument(data)
			if parseErr == nil {
				projection := adf.ToMarkdownLossy(doc)
				out.ProjectionAvailable = true
				out.LossyConstructs = projection.LossyConstructs
				out.Lossy = len(out.LossyConstructs) != 0
			}
			if validationErr != nil {
				return out, inspectionError(validationErr, diagnostics)
			}
			if parseErr != nil {
				out.ProjectionDiagnostic = parseErr.Error()
			}
			return out, ctx.Err()
		},
	})
}

// A nil report distinguishes a broken embedded validator from caller input.
func inspectionError(err error, diagnostics []adf.ValidationDiagnostic) error {
	if diagnostics == nil {
		return err
	}
	input := cli.NewInputError(cli.CodeADFInvalid, "--input is not valid against the pinned ADF schema; inspect the retained diagnostics")
	input.Field = flagInput
	return input
}

func newExplain() *cobra.Command {
	rows := adf.SupportMatrix()
	names := adf.SupportIdentities()
	return command.MustNew(command.Spec[envelope.ADFExplainOutput]{
		Op: "adf.explain-node", Use: "explain-node TYPE", Short: "Explain a pinned node or mark and its context variants",
		Long:    "Pass an exact runtime type (paragraph, panel, strong) or schema definition (paragraph_with_no_marks_node). Runtime names return every matching context variant; definition names return one. Bounds summarize structure, not enum values, recursive validation or Jira acceptance. Use adf validate for complete native documents and agent adf-matrix for all accepted identities. Local preservation is not lossless editing.",
		Example: "$ jira adf explain-node paragraph --output=json\n\n$ jira adf explain-node paragraph_with_no_marks_node --jq '.data.entries[0].node_bounds.marks.max'",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "TYPE", Predictor: "adftype"}}},
		Flags:   func(c *cobra.Command) { c.ValidArgs = slices.Clone(names) },
		Human:   renderExplained,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ADFExplainOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.ADFExplainOutput{}, err
			}
			matches := []adf.SupportEntry{}
			for _, row := range rows {
				if row.Name == inv.Args[0] || row.Definition == inv.Args[0] {
					matches = append(matches, row)
				}
			}
			if len(matches) == 0 {
				err := cli.NewInputError(cli.CodeADFTypeUnknown, "unknown ADF node, mark or definition: "+inv.Args[0])
				err.Field = "TYPE"
				err.Suggestions = slices.Clone(names)
				return envelope.ADFExplainOutput{}, err
			}
			return envelope.ADFExplainOutput{SchemaSHA256: adf.SchemaSHA256, Entries: matches}, nil
		},
	})
}

func renderValidation(v *output.View, valid bool, diagnostics []adf.ValidationDiagnostic) {
	v.Fields(output.Field{Label: "Schema valid", Value: cli.Text(fmt.Sprint(valid))})
	for _, diagnostic := range diagnostics {
		path := diagnostic.InstanceLocation
		if path == "" {
			path = "/ (document root)"
		}
		v.Fields(output.Field{Label: cli.Text(path), Value: cli.Text(diagnostic.Message)})
		if diagnostic.KeywordLocation != "" {
			v.Fields(output.Field{Label: "Schema keyword", Value: cli.Text(diagnostic.KeywordLocation)})
		}
	}
}

func renderValidated(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFValidateOutput)
	if !ok {
		return fmt.Errorf("adf: expected ADFValidateOutput, got %T", env.Data)
	}
	v.Section("ADF validation")
	renderValidation(v, out.Valid, out.Diagnostics)
	return nil
}

func renderLinted(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFLintOutput)
	if !ok {
		return fmt.Errorf("adf: expected ADFLintOutput, got %T", env.Data)
	}
	v.Section("ADF inspection")
	renderValidation(v, out.Valid, out.Diagnostics)
	v.Fields(output.Field{Label: "Projection available", Value: cli.Text(fmt.Sprint(out.ProjectionAvailable))})
	if out.ProjectionDiagnostic != "" {
		v.Fields(output.Field{Label: "Projection diagnostic", Value: cli.Text(out.ProjectionDiagnostic)})
	}
	v.Fields(output.Field{Label: "Lossy projection", Value: cli.Text(fmt.Sprint(out.Lossy))})
	if out.Lossy {
		v.Fields(output.Field{Label: "Lossy constructs", Value: cli.Text(strings.Join(out.LossyConstructs, ", "))})
	}
	return nil
}

func renderExplained(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ADFExplainOutput)
	if !ok {
		return fmt.Errorf("adf: expected ADFExplainOutput, got %T", env.Data)
	}
	v.Section("ADF definitions")
	for _, row := range out.Entries {
		v.Section(cli.Text(row.Definition))
		v.Fields(output.Field{Label: "Kind", Value: cli.Text(row.Kind)}, output.Field{Label: "Runtime type", Value: cli.Text(row.Name)}, output.Field{Label: "Projection", Value: cli.Text(row.MarkdownProjection)}, output.Field{Label: "Markdown authoring", Value: cli.Text(fmt.Sprint(row.Capabilities.Author))})
		if row.NodeBounds != nil {
			v.Fields(output.Field{Label: "Required keys", Value: cli.Text(strings.Join(row.NodeBounds.Required, ", "))}, output.Field{Label: "Open node", Value: cli.Text(fmt.Sprint(row.NodeBounds.Open))})
			if marks := row.NodeBounds.Marks; marks != nil {
				allowed := "any"
				if marks.Allowed != nil {
					allowed = fmt.Sprint(marks.Allowed)
				}
				v.Fields(output.Field{Label: "Maximum marks", Value: cli.Text(maximumBound(marks.Max))}, output.Field{Label: "Allowed marks", Value: cli.Text(allowed)})
			}
			if attrs := row.NodeBounds.Attrs; attrs != nil {
				v.Fields(output.Field{Label: "Required attributes", Value: cli.Text(strings.Join(attrs.Required, ", "))}, output.Field{Label: "Allowed attributes", Value: cli.Text(strings.Join(attrs.Allowed, ", "))}, output.Field{Label: "Open attributes", Value: cli.Text(fmt.Sprint(attrs.Open))})
			}
			if content := row.NodeBounds.Content; content != nil {
				v.Fields(output.Field{Label: "Child definitions", Value: cli.Text(strings.Join(content.Refs, ", "))}, output.Field{Label: "Minimum children", Value: cli.Text(fmt.Sprint(content.MinItems))}, output.Field{Label: "Maximum children", Value: cli.Text(maximumBound(content.MaxItems))})
			}
		}
		if row.MarkBounds != nil && row.MarkBounds.Attrs != nil {
			v.Fields(output.Field{Label: "Required attributes", Value: cli.Text(strings.Join(row.MarkBounds.Attrs.Required, ", "))}, output.Field{Label: "Allowed attributes", Value: cli.Text(strings.Join(row.MarkBounds.Attrs.Allowed, ", "))}, output.Field{Label: "Open attributes", Value: cli.Text(fmt.Sprint(row.MarkBounds.Attrs.Open))})
		}
	}
	v.Line("Use full-document validation for enums and context; preservation does not establish lossless editing or Jira acceptance.")
	return nil
}

func maximumBound(value int) string {
	if value < 0 {
		return "unbounded"
	}
	return fmt.Sprint(value)
}
