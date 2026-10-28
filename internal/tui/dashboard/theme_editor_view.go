package dashboard

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/table"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Keep color roles in fixed rows rather than a command picker that expands
// the selected description. Grid owns ANSI-aware column sizing and shrinking;
// bounded role names keep the value/source columns near the names on wide screens.
func (d *themeEditorDialog) themeRows(width int) string {
	query := accent("/ ") + d.chooser.menu.Query()
	if d.chooser.menu.Query() == "" {
		query += dim("Type to filter")
	}
	valueHeading := "Color"
	if d.sections.ActiveItem() == "Palette" {
		valueHeading = "Value"
	}
	heading := func(text string) string {
		if cli.StyleEnabled() {
			return cli.ResolvedTheme().Bold.Render(text)
		}
		return text
	}
	cells := [][]string{{heading("Role"), heading(valueHeading), heading("Source")}}
	entries := d.chooser.visibleEntries()
	selected, found := d.chooser.menu.Selected()
	for _, entry := range entries {
		source := d.roleSource(d.roles[entry.Name])
		styledSource := dim(source)
		if source == "Custom" {
			styledSource = accent(source)
		}
		name := layout.NormalizeLine(xansi.WcWidth.Truncate(cli.Text(entry.Name), 24, "…"), 24)
		// Selection belongs to the role cell. Wrapping the full row lets a color
		// swatch's SGR reset interrupt reverse video partway through the row.
		if found && entry == selected && cli.StyleEnabled() {
			name = d.roleSelectionStyle(entry.Name).Render(name)
		}
		cells = append(cells, []string{name, entry.Key, styledSource})
	}
	grid := table.NewGrid(cells)
	grid.MaxWidth = max(1, min(52, width-2))
	grid.FlexCols = []int{0, 1, 2}
	aligned, _ := grid.AlignColumns()
	rows := []string{query, "", "  " + aligned[0]}
	for i, entry := range entries {
		row := "  " + aligned[i+1]
		if found && entry == selected {
			d.selectedRow = d.chooserRow + len(rows)
			row = accent("› ") + aligned[i+1]
		}
		rows = append(rows, row)
	}
	if !found {
		rows = append(rows, dim("  No matching roles."))
	}
	return strings.Join(layout.NormalizeLines(rows, width), "\n")
}

func (d *themeEditorDialog) roleSource(role string) string {
	switch role {
	case "base":
		return "Base"
	case "entities":
		if len(d.definition.EntityColors) > 0 {
			return "Custom"
		}
		return "Base"
	}
	section, name, _ := strings.Cut(role, ".")
	overrides := d.definition.Colors
	if section == "syntax" {
		overrides = d.definition.Syntax
	}
	if _, ok := overrides[name]; ok {
		return "Custom"
	}
	return "Base"
}

func (d *themeEditorDialog) roleSelectionStyle(name string) lipgloss.Style {
	if value := d.resolvedColors[d.roles[name]]; value != "" {
		return cli.ColorSelectionStyle(lipgloss.Color(value))
	}
	return cli.ResolvedTheme().Blue.Reverse(true)
}
