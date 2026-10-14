package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

func newFieldTypes() *cobra.Command {
	return command.MustNew(command.Spec[envelope.FieldTypesOutput]{
		Op: "agent.fieldtypes", Use: "fieldtypes", Short: "Discover native field-value shapes and handling",
		Passthrough: true, // types.example contains Jira's native value vocabulary
		Long: "Inspect local field handling without credentials or Jira requests. " +
			"The rows are illustrative native values for familiar field families, not an exhaustive plugin registry or tenant schema. " +
			"Names are not field IDs or automatic matches for metadata schema.type.\n\n" +
			"issue create/edit/clone --json-input accepts native fields and update values. " +
			"No scalar conversion or type-specific validation runs on this route. " +
			"Unknown types, numeric tokens, explicit nulls, empty arrays and unknown object members are preserved. " +
			"Omitted fields stay omitted. Jira decides whether a value, clearing request or operation is valid. " +
			"A dry run proves composition, not field availability or server acceptance.\n\n" +
			"Use issue input prepare with an authored payload and an explicit native screen metadata file for deliberate scalar lifting and known-shape validation. " +
			"It is local authoring, preserves unknown plugins and native values, and does not change the mutation JSON route.\n\n" +
			"Read metadata with jira api get: /rest/api/3/field lists field IDs and schemas; " +
			"/rest/api/3/issue/KEY-123/editmeta describes an issue's editable fields and operations; " +
			"/rest/api/3/issue/createmeta/PROJECT/issuetypes/TYPE_ID lists paginated create fields. " +
			"These reads require authentication. Use the actual project, issue type and field context to obtain allowed values. " +
			"issue create --generate-json --project KEY_OR_ID --type NAME_OR_ID collects all context-specific create metadata pages and a native placeholder skeleton. " +
			"issue edit ISSUE_KEY... --generate-json keeps each issue's editable field metadata and skeleton separate. " +
			"These authenticated reads work in read-only mode. Fill wanted values and remove unused optional fields or operations before submitting the extracted payload. " +
			"JSON carries rows at .data.types; compact at .types. An example is a field value, not a whole request payload.",
		Example: "$ jira agent fieldtypes --output=json\n\n" +
			"$ jira agent fieldtypes --jq '.data.types[] | select(.name == \"user\") | .example'",
		Human: renderFieldTypes,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.FieldTypesOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.FieldTypesOutput{}, err
			}
			return envelope.FieldTypesOutput{
				Types: jira.FieldTypeExamples(),
				Handling: envelope.FieldTypeHandling{
					InputFlag: command.FlagJSONInput, Encoding: "native-json", PreserveValues: true,
					UnknownTypes: "native-json", NullValues: "explicit-null", EmptyArrays: "empty-array",
				},
				Metadata: []envelope.FieldMetadataRoute{
					{Purpose: "field IDs and schemas", Path: "/rest/api/3/field"},
					{Purpose: "editable fields, allowed values and operations", Path: "/rest/api/3/issue/KEY-123/editmeta"},
					{Purpose: "paginated create fields for a project and issue type", Path: "/rest/api/3/issue/createmeta/PROJECT/issuetypes/TYPE_ID"},
				},
				OfficialURL: "https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/",
			}, nil
		},
	})
}

func renderFieldTypes(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.FieldTypesOutput)
	if !ok {
		return fmt.Errorf("agent: expected FieldTypesOutput, got %T", env.Data)
	}
	v.Section("Native field values")
	v.Line("Use --json-input. Jira validates field types and allowed values; the CLI preserves native values.")
	fields := make([]output.Field, 0, len(out.Types))
	for _, row := range out.Types {
		fields = append(fields, output.Field{Label: cli.Text(row.Name), Value: cli.Text(row.NativeShape)})
	}
	v.Fields(fields...)
	v.Line("Examples need real field IDs and permitted values. Unknown types use the same native route.")
	return nil
}
