package dashboard

import (
	"fmt"
	"strings"

	"github.com/gechr/primer/layout"
	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

type previewField struct{ name, value string }

// Short stacked previews reserve space for the description. Full details retain
// the separate labeled fields; the same native values supply both presentations.
func (m *Model) previewMetadata(f jira.IssueFields, compact bool, width int) []string {
	fields := []previewField{}
	if f.Status.Set {
		fields = append(fields, previewField{name: "Status", value: statusValue(f.Status)})
	}
	if f.Assignee.Set {
		fields = append(fields, previewField{name: "Assignee", value: personValue(f.Assignee)})
	}
	if m.enabled("tui_priority") && f.Priority.Set {
		fields = append(fields, previewField{name: "Priority", value: textValue(f.Priority.Value.Name)})
	}
	if m.enabled("tui_issue_type") && f.Issuetype.Set {
		fields = append(fields, previewField{name: "Type", value: textValue(f.Issuetype.Value.Name)})
	}
	if m.enabled("tui_labels") && f.Labels.Set && !f.Labels.Null && len(f.Labels.Value) > 0 {
		fields = append(fields, previewField{name: "Labels", value: cli.Text(strings.Join(f.Labels.Value, ", "))})
	}
	parts := xslices.Map(fields, func(field previewField) string {
		if compact {
			return field.value
		}
		return fmt.Sprintf("%-11s%s", field.name, field.value)
	})
	if compact && len(parts) > 0 {
		return []string{layout.NormalizeLine(strings.Join(parts, dim(" · ")), width)}
	}
	return parts
}
