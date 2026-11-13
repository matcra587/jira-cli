package root

import (
	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

// apiEndpointPredictor is bound to the method in command metadata rather than
// inferring it from shell argv. The bundled index needs no settings or cache.
func apiEndpointPredictor(method string) func([]string) []string {
	return func([]string) []string {
		operations := jira.APIOperations(method, "")
		operations = xslices.UniqueFunc(operations, func(value jira.APIOperation) string { return value.Path })
		return xslices.Map(operations, func(value jira.APIOperation) string {
			return cli.Text(value.Path) + "\t" + cli.Text(value.Summary)
		})
	}
}
