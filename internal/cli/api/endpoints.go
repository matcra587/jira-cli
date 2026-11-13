package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// Methods is the raw API command vocabulary; catalog coverage may be narrower.
func Methods() []string {
	return []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
}

func newEndpoints() *cobra.Command {
	var method, query string
	return command.MustNew(command.Spec[envelope.APIEndpointsOutput]{
		Op: "api.endpoints", Use: "endpoints", Short: "Find documented API operations offline",
		Long: "Search the bundled platform and Agile API catalog without credentials, configuration, network requests or cache warmup. " +
			"The catalog is generated from this release's pinned OpenAPI documents; source versions and digests are returned with every result. " +
			"Filter by HTTP method or a case-insensitive substring of the path, operation ID or summary. Results are sorted by path and method.\n\n" +
			"Paths are templates: replace brace-delimited parameters with actual values before making a request. " +
			"A documented operation does not establish site availability, permissions or write authorization. " +
			"The catalog supplies suggestions only; jira api METHOD ENDPOINT still accepts literal paths absent from it. " +
			"Only explicitly documented methods are listed, so a supported method can have no catalog entries. " +
			"Shell endpoint completion reads the same bundled catalog, filtered by the selected method. " +
			"Upgrading the CLI updates the bundled catalog; clearing tenant metadata does not change it.",
		Example: `$ jira api endpoints --method=get --query=issue --output=json

# List documented Agile board operations
$ jira api endpoints --query=/rest/agile/1.0/board --output=compact

# Extract matching path templates
$ jira api endpoints --method=get --query=attachment --jq '.data.operations[].path'`,
		Flags: func(cmd *cobra.Command) {
			values := []string{"all"}
			for _, value := range Methods() {
				values = append(values, strings.ToLower(value))
			}
			command.AddStringVar(cmd, &method, "method", "all", "Select a documented HTTP method", command.FlagOpts{
				Terse: "HTTP method", Placeholder: "method", Enum: values, EnumDefault: "all",
			})
			command.AddStringVar(cmd, &query, "query", "", "Search paths, operation IDs and summaries", command.FlagOpts{
				Terse: "Search text", Placeholder: "text",
			})
		},
		Validate: func() error {
			if !strings.EqualFold(method, "all") && !slices.Contains(Methods(), strings.ToUpper(method)) {
				err := cli.NewInputError(cli.CodeFlagValueInvalid, "method must be all, get, head, options, post, put, patch or delete")
				err.Field = "method"
				return err
			}
			return nil
		},
		Human: renderEndpoints,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.APIEndpointsOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.APIEndpointsOutput{}, err
			}
			selected := method
			if strings.EqualFold(selected, "all") {
				selected = ""
			}
			return envelope.APIEndpointsOutput{
				CatalogVersion: jira.APICatalogVersion, Sources: jira.APICatalogSources(),
				Operations: jira.APIOperations(selected, query),
			}, nil
		},
	})
}

func renderEndpoints(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.APIEndpointsOutput)
	if !ok {
		return fmt.Errorf("api.endpoints: unexpected payload %T", env.Data)
	}
	v.Line(strconv.Itoa(len(out.Operations)), "documented API operations from the bundled catalog")
	for _, operation := range out.Operations {
		v.Line(cli.Text(operation.Method), cli.Text(operation.Path))
		v.Line(cli.Text(operation.Summary))
		if operation.Deprecated {
			v.Line("Deprecated in the pinned specification.")
		}
	}
	v.Line("Path templates require actual parameter values. Jira decides availability and permissions.")
	return nil
}
