package jql

import (
	"context"
	"fmt"
	"slices"

	"github.com/gechr/primer/table"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// Deps preserves credential-free help, schema and static completion.
type Deps struct {
	JiraClient func(ctx context.Context) (core.JiraClient, error)
	// Preferences and CacheFor never access the credential store. CacheFor
	// must isolate the supplied non-secret site/account identity.
	Preferences func() (BuildPreferences, error)
	CacheFor    func(site, account string) (core.Cache, error)
}

func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "jql", Short: "Build queries, validate JQL and discover live metadata", Args: cobra.NoArgs,
		Long:    "Build a query locally from structured filters, validate explicit JQL with Jira or discover the site's visible fields, functions and reserved words. These commands do not change issues.",
		Example: `$ jira jql validate 'project = KEY AND assignee = currentUser()'`,
	}
	cmd.AddCommand(newBuild(deps), newValidate(deps), newReference(deps))
	return cmd
}

func newValidate(deps Deps) *cobra.Command {
	var mode string
	return command.MustNew(command.Spec[envelope.JQLValidateOutput]{
		Op: "jql.validate", Passthrough: true,
		Use: "validate QUERY...", Short: "Validate explicit queries through Jira's parser",
		Long: "Send one or more quoted JQL queries to Jira, preserving their order and duplicates. " +
			"A successful parser call returns per-query valid=false for errors and exits zero; inspect data.queries[].valid. " +
			"Authentication, transport, rate-limit and server failures remain nonzero errors.\n\n" +
			"--mode strict returns errors and omits the syntax tree when validation fails. " +
			"warn can return a syntax tree despite semantic errors. none skips semantic validation; syntax errors still appear. " +
			"Warnings alone do not make valid false. Validity is scoped to the mode and current account; it does not prove matching issues or write permission.\n\n" +
			"submitted_queries retains authored text. queries reports Jira's possibly normalized text; parsed preserves the complete native response, syntax trees and extensions. " +
			"Use -- before a query beginning with a dash.",
		Example: `$ jira jql validate 'status = Done AND assignee = currentUser()'

# Inspect both validity and diagnostics
$ jira jql validate 'project = KEY' 'created >= -7d' --mode warn --output=json

# Syntax errors remain result data
$ jira jql validate 'project = (' --output=json`,
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "QUERY"}}},
		Flags: func(c *cobra.Command) {
			command.AddStringVar(c, &mode, "mode", string(core.JQLValidationStrict), "Validation mode: strict, warn or none", command.FlagOpts{
				Terse: "Validation mode", Placeholder: "mode", Enum: []string{string(core.JQLValidationStrict), string(core.JQLValidationWarn), string(core.JQLValidationNone)}, EnumTerse: []string{"semantic validation", "retain formed syntax trees", "syntax parsing only"}, EnumDefault: string(core.JQLValidationStrict),
			})
		},
		Human: renderValidate,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.JQLValidateOutput, error) {
			return runValidate(ctx, deps, inv.Args, core.JQLValidationMode(mode))
		},
	})
}

func newReference(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.JQLReferenceOutput]{
		Op: "jql.reference", Passthrough: true,
		Use: "reference", Short: "Discover live JQL fields, functions and reserved words",
		Long: "Read the configured site's JQL autocomplete metadata. Human output includes fields, functions and reserved words; machine output retains the complete native reference object. " +
			"Visible metadata depends on the site and account. Missing entries do not prove that a field or function does not exist, and metadata does not prove query validity or matching issues. " +
			"Native boolean-like flags are strings; inspect reference for operators, types, capabilities and future properties.",
		Example: "$ jira jql reference\n\n# Retain complete native metadata\n$ jira jql reference --output=json",
		Human:   renderReference,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.JQLReferenceOutput, error) {
			return runReference(ctx, deps)
		},
	})
}

func runValidate(ctx context.Context, deps Deps, queries []string, mode core.JQLValidationMode) (envelope.JQLValidateOutput, error) {
	if err := mode.Validate(); err != nil {
		input := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
		input.Field = "mode"
		return envelope.JQLValidateOutput{}, input
	}
	if err := core.ValidateJQLQueries(queries, mode); err != nil {
		input := cli.NewInputError(cli.CodeArgValueInvalid, err.Error())
		input.Field = "QUERY"
		return envelope.JQLValidateOutput{}, input
	}
	if err := ctx.Err(); err != nil {
		return envelope.JQLValidateOutput{}, err
	}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return envelope.JQLValidateOutput{}, err
	}
	read, err := command.Spin(ctx, "Validating JQL", func(ctx context.Context) (core.JQLParseRead, error) { return client.ParseJQL(ctx, queries, mode) })
	if err != nil {
		return envelope.JQLValidateOutput{}, err
	}
	if read.Parsed == nil || !read.Parsed.Queries.Set || read.Parsed.Queries.Null || len(read.Parsed.Queries.Value) != len(queries) {
		return envelope.JQLValidateOutput{}, cli.NewCodedError(cli.CodeJQLResponseInvalid, "parser returned no complete result list")
	}
	for _, query := range read.Parsed.Queries.Value {
		if !query.Query.Set || query.Query.Null {
			return envelope.JQLValidateOutput{}, cli.NewCodedError(cli.CodeJQLResponseInvalid, "parser result lacks its query text")
		}
	}
	entries := xslices.Map(read.Parsed.Queries.Value, func(query jira.ParsedJqlQuery) envelope.JQLValidateEntry {
		return envelope.JQLValidateEntry{Query: query.Query.Value, Valid: len(query.Errors.Value) == 0, Errors: query.Errors, Warnings: query.Warnings}
	})
	return envelope.JQLValidateOutput{Mode: string(mode), SubmittedQueries: slices.Clone(queries), Queries: entries, Parsed: *read.Parsed}, nil
}

func runReference(ctx context.Context, deps Deps) (envelope.JQLReferenceOutput, error) {
	if err := ctx.Err(); err != nil {
		return envelope.JQLReferenceOutput{}, err
	}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return envelope.JQLReferenceOutput{}, err
	}
	read, err := command.Spin(ctx, "Reading JQL reference", client.GetJQLReference)
	if err != nil {
		return envelope.JQLReferenceOutput{}, err
	}
	if read.Reference == nil {
		return envelope.JQLReferenceOutput{}, cli.NewCodedError(cli.CodeJQLResponseInvalid, "reference read returned no metadata object")
	}
	fields := xslices.Map(read.Reference.VisibleFieldNames.Value, func(field jira.FieldReferenceData) envelope.JQLReferenceField {
		return envelope.JQLReferenceField{Value: field.Value, DisplayName: field.DisplayName, CustomFieldID: field.Cfid}
	})
	functions := xslices.Map(read.Reference.VisibleFunctionNames.Value, func(function jira.FunctionReferenceData) envelope.JQLReferenceFunction {
		return envelope.JQLReferenceFunction{Value: function.Value, DisplayName: function.DisplayName}
	})
	return envelope.JQLReferenceOutput{Fields: fields, Functions: functions, ReservedWords: read.Reference.JqlReservedWords, Reference: *read.Reference}, nil
}

func renderValidate(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.JQLValidateOutput)
	if !ok {
		return fmt.Errorf("jql: expected JQLValidateOutput, got %T", env.Data)
	}
	v.Line(cli.Text("Validation mode: " + out.Mode))
	for _, query := range out.Queries {
		state := "no errors"
		if !query.Valid {
			state = "errors"
		}
		v.Line(cli.Text(state + ": " + query.Query))
		for _, message := range query.Errors.Value {
			v.Line(cli.Text("  error: " + message))
		}
		for _, message := range query.Warnings.Value {
			v.Line(cli.Text("  warning: " + message))
		}
	}
	v.Hint("Query diagnostics are result data. No errors does not prove matching issues; none skips semantic validation.")
	return nil
}

func renderReference(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.JQLReferenceOutput)
	if !ok {
		return fmt.Errorf("jql: expected JQLReferenceOutput, got %T", env.Data)
	}
	v.Line(cli.Text(fmt.Sprintf("%d visible fields", len(out.Fields))))
	if len(out.Fields) > 0 {
		output.Table(v, cli.ResolvedTheme(), []table.Column[envelope.JQLReferenceField]{
			{Name: "value", Header: "FIELD", Render: func(f envelope.JQLReferenceField, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(f.Value.Value)))
			}},
			{Name: "display_name", Header: "NAME", Flex: true, Render: func(f envelope.JQLReferenceField, _ *table.RenderContext) table.Cell {
				return table.StyledCell(cli.OrDash(cli.Entity(f.DisplayName.Value)), cli.Text(f.DisplayName.Value))
			}},
			{Name: "custom_field_id", Header: "CUSTOM FIELD", Render: func(f envelope.JQLReferenceField, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(f.CustomFieldID.Value)))
			}},
		}, out.Fields)
	}
	v.Line(cli.Text(fmt.Sprintf("%d visible functions", len(out.Functions))))
	for _, function := range out.Functions {
		v.Line(cli.Text(function.Value.Value + " — " + function.DisplayName.Value))
	}
	v.Line(cli.Text("Reserved words:"))
	for _, word := range out.ReservedWords.Value {
		v.Line(cli.Text(word))
	}
	v.Hint("Metadata visibility depends on the site and account; missing entries do not prove absence.")
	return nil
}
