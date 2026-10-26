package command

import (
	"fmt"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Issue expansion names come from the pinned platform issue/search schemas.
// Only list --detail selects a preset; issue view expansions are opt-in.
const (
	IssueListExpansions  = "renderedFields,names,schema,transitions,operations,changelog"
	IssueExpansionValues = "renderedFields,names,schema,transitions,operations,editmeta,changelog,versionedRepresentations"
)

// ValidateIssueExpansions rejects unsupported names before client resolution.
// An empty slice explicitly disables expansions when its flag was supplied.
func ValidateIssueExpansions(values []string) error {
	if len(values) == 0 {
		return nil
	}
	allowed := strings.Split(IssueExpansionValues, ",")
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf("--expand value %q is unsupported; use %s", value, strings.ReplaceAll(IssueExpansionValues, ",", ", ")))
			e.Field = "expand"
			return e
		}
	}
	return nil
}
