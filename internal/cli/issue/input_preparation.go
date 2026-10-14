package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

func newInput() *cobra.Command {
	group := &cobra.Command{Use: "input", Short: "Prepare native issue input without submitting changes", Args: cobra.NoArgs}
	group.AddCommand(newInputPrepare())
	return group
}

func newInputPrepare() *cobra.Command {
	var input, metadata string
	var create bool
	var cmd *cobra.Command
	cmd = command.MustNew(command.Spec[envelope.IssueInputPreparationOutput]{
		Op: "issue.input.prepare", Use: "prepare", Short: "Prepare scalar field values using an explicit metadata snapshot",
		Passthrough: true,
		Long: "Prepare a whole fields/update payload locally, without credentials, Jira requests or file writes. " +
			"--metadata names one native {fields: {...}} screen snapshot; --input names the authored payload and defaults to piped stdin. " +
			"Each file or stream is limited to 1 MiB. Only one input may read stdin. Use --create for the structural project/issuetype screen exemptions.\n\n" +
			"Known select labels become value objects, user IDs become accountId objects, group/version names become name objects, " +
			"and project/parent keys become key objects. Numeric-looking strings never imply IDs. Already-native objects retain their extensions. " +
			"Numeric strings in known number fields become JSON number tokens without float conversion or spelling changes. " +
			"The legacy fields.custom_fields wrapper is flattened with collision refusal. Unknown plugins, native update values, explicit nulls " +
			"and unchanged native numeric/string spelling are preserved. Known malformed shapes refuse the entire preparation; no field is dropped.\n\n" +
			"Textarea Markdown uses the shared ADF converter. Dropped content refuses; use adf convert to review and explicitly accept loss " +
			"before supplying its native document. Existing native ADF, including future content, is preserved.\n\n" +
			"fields reports encoded, validated or unverified handling. Validation checks local known shapes and snapshot field/operation membership, " +
			"not requiredness, allowed option values, permissions, freshness, write success or server concurrency protection. " +
			"Preserved native ADF is unverified: its preservation parser does not validate known or future node schemas. " +
			"This explicit authoring command does not change create/edit/clone --json-input. " +
			"Extract .data.payload in JSON or .payload in compact mode before submitting it.",
		Example: "$ jira issue edit KEY-123 --generate-json --jq '.data.results[0].data.metadata' > editmeta.json\n\n" +
			"$ jira issue input prepare --input authored.json --metadata editmeta.json --jq '.data.payload' > prepared.json\n\n" +
			"$ jira issue create --generate-json --project KEY --type Story --jq '{fields:.data.fields}' > createmeta.json\n\n" +
			"$ jira issue input prepare --create --input authored.json --metadata createmeta.json --output=json",
		Flags: func(c *cobra.Command) {
			command.AddStringVar(c, &input, "input", command.StdinPath, "Authored fields/update JSON file; - reads piped stdin", command.FlagOpts{Terse: "Authored input", Placeholder: "file", Hint: "file"})
			command.AddStringVar(c, &metadata, "metadata", "", "Explicit native screen metadata file; - reads piped stdin", command.FlagOpts{Terse: "Metadata snapshot", Placeholder: "file", Hint: "file"})
			command.AddBoolVar(c, &create, "create", false, "Allow structural project/issuetype fields outside the create screen", command.FlagOpts{Terse: "Create context"})
		},
		Validate: func() error {
			for _, fact := range []struct{ flag, value string }{{"input", input}, {"metadata", metadata}} {
				if strings.TrimSpace(fact.value) == "" {
					err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+fact.flag+" requires a file path or -")
					err.Field = fact.flag
					return err
				}
			}
			if input == command.StdinPath && metadata == command.StdinPath {
				return cli.NewInputError(cli.CodeFlagConflict, "--input and --metadata cannot both read stdin")
			}
			if cmd.Flags().Changed(flagWeb) {
				err := cli.NewInputError(cli.CodeFlagNotApplicable, "--web does not open local input preparation")
				err.Field = flagWeb
				return err
			}
			return nil
		},
		Human: renderInputPreparation,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueInputPreparationOutput, error) {
			return runInputPreparation(ctx, inv, input, metadata, create)
		},
	})
	return cmd
}

func runInputPreparation(ctx context.Context, inv command.Invocation, input, metadata string, create bool) (envelope.IssueInputPreparationOutput, error) {
	metadataBytes, err := command.ReadInputFileContext(ctx, "metadata", metadata, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return envelope.IssueInputPreparationOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return envelope.IssueInputPreparationOutput{}, err
	}
	screen, err := core.DecodeEditIssueFields(metadataBytes)
	if err != nil {
		return envelope.IssueInputPreparationOutput{}, preparationInputError("metadata", err)
	}
	inputBytes, err := command.ReadInputFileContext(ctx, "input", input, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return envelope.IssueInputPreparationOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return envelope.IssueInputPreparationOutput{}, err
	}
	var request jira.IssueWriteRequest
	if err := json.Unmarshal(inputBytes, &request); err != nil {
		return envelope.IssueInputPreparationOutput{}, preparationInputError("input", err)
	}
	prepared, err := core.PrepareIssueInput(ctx, request, screen.Fields, create)
	if err != nil {
		var valueError *core.IssueInputPreparationError
		if errors.As(err, &valueError) {
			err = preparationInputError("input", err)
		} else if errors.Is(err, core.ErrIssueMetadataInvalid) {
			err = preparationInputError("metadata", err)
		}
		return envelope.IssueInputPreparationOutput{}, err
	}
	payload, err := jira.MarshalNoEscape(prepared.Payload)
	if err != nil {
		return envelope.IssueInputPreparationOutput{}, err
	}
	out := envelope.IssueInputPreparationOutput{
		Payload: payload, Fields: []envelope.IssueInputPreparedField{}, Diagnostics: []envelope.IssueInputPreparationDiagnostic{},
	}
	for _, field := range prepared.Fields {
		out.Fields = append(out.Fields, envelope.IssueInputPreparedField{Field: field.Field, Type: field.Type, Handling: field.Handling})
	}
	for _, warning := range prepared.Warnings {
		out.Diagnostics = append(out.Diagnostics, envelope.IssueInputPreparationDiagnostic{
			Type: warning.Type, Message: warning.Message, Field: warning.Field, Path: warning.Path,
			NodeType: warning.NodeType, MarkType: warning.MarkType, Lossy: warning.Lossy,
		})
	}
	return out, ctx.Err()
}

func preparationInputError(field string, err error) error {
	inputErr := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+field+": "+err.Error())
	inputErr.Field = field
	return inputErr
}

func renderInputPreparation(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueInputPreparationOutput)
	if !ok {
		return fmt.Errorf("issue input: expected IssueInputPreparationOutput, got %T", env.Data)
	}
	v.Section("Prepared issue input")
	v.Line(cli.Text(string(out.Payload)))
	for _, field := range out.Fields {
		v.Line(cli.Text(field.Field), cli.Text(field.Handling), cli.Text(field.Type))
	}
	for _, diagnostic := range out.Diagnostics {
		v.Line(cli.Text(diagnostic.Field), cli.Text(diagnostic.Path), cli.Text(diagnostic.Message))
	}
	v.Hint("Local snapshot checks do not establish Jira permissions or accepted values.")
	return nil
}
