package dashboard

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Center the change summary as one block, preserving a shared left edge for
// its headings and literal labels. Center the shorter notices separately.
// Confirm must receive the final width before it positions its button row.
func labelConfirmationText(description string, width int) string {
	rows := layout.WrapLines(description, width)
	group := lipgloss.JoinVertical(lipgloss.Left, rows...)
	summary := "\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center, group) + "\n"
	notes := []string{"Replaces all labels on this issue.", "Changes elsewhere may be overwritten."}
	for _, note := range notes {
		summary += "\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center, dim(strings.Join(layout.WrapLines(note, width), "\n")))
	}
	return summary
}

func labelConfirmationSection(title string, labels []string) string {
	heading := title
	if cli.StyleEnabled() {
		heading = cli.ResolvedTheme().Bold.Render(title)
	}
	heading += " " + dim("(") + accent(strconv.Itoa(len(labels))) + dim("):")
	value := dim("None")
	if len(labels) > 0 {
		value = cli.TextBlock(strings.Join(labels, "\n"))
	}
	return heading + "\n" + lipgloss.NewStyle().PaddingLeft(2).Render(value)
}
