package dashboard

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Section headings are presentation only. Primer owns filtering and cursor
// movement over real themes, so arrows never land on headers or empty states.
func (p *pickerDialog) themeContent(width int) string {
	query := accent("/ ") + p.menu.Query()
	if p.menu.Query() == "" {
		query += dim("Type to filter")
	}
	rows := []string{query, ""}
	selected, found := p.menu.Selected()
	entries := visiblePaletteEntries(p.menu, len(p.choices))
	for _, custom := range []bool{false, true} {
		group := []string{}
		selectedIndex := -1
		for _, entry := range entries {
			value := ""
			for _, choice := range p.choices {
				if choice.label == entry.Name {
					value = choice.value
					break
				}
			}
			if strings.HasPrefix(value, "custom:") != custom {
				continue
			}
			name := xansi.WcWidth.Truncate(cli.Text(entry.Name), max(1, width-3), "…")
			if found && entry == selected {
				selectedIndex = len(group)
				group = append(group, commandStyles().Selected.Render(name))
			} else {
				group = append(group, "  "+name)
			}
		}
		if len(group) == 0 && (p.menu.Query() != "" || !custom) {
			continue
		}
		heading := "Built-in themes"
		if custom {
			heading = "Your themes"
		}
		rows = append(rows, accent(heading))
		if selectedIndex >= 0 {
			p.themeCursorRow = len(rows) + selectedIndex
		}
		if len(group) == 0 {
			group = append(group, dim("  No custom themes yet. Ctrl+N creates one."))
		}
		rows = append(rows, group...)
		rows = append(rows, "")
	}
	if !found {
		rows = append(rows, dim("No themes match."))
	}
	if p.notice != "" {
		rows = append([]string{cli.Text(p.notice), ""}, rows...)
		p.themeCursorRow += 2
	}
	return strings.Join(layout.NormalizeLines(rows, width), "\n")
}

func (p *pickerDialog) themeFooter() string {
	if p.loading {
		return "Loading themes…"
	}
	hints := []key.Hint{{Key: "↑↓", Desc: "preview"}, {Key: "enter", Desc: "keep"}, {Key: "esc", Desc: "restore"}}
	if p.manageThemes {
		hints = append(hints, key.Hint{Key: "ctrl+n", Desc: "new"}, key.Hint{Key: "ctrl+e", Desc: "edit"}, key.Hint{Key: "ctrl+d", Desc: "duplicate"}, key.Hint{Key: "ctrl+i", Desc: "import"}, key.Hint{Key: "ctrl+r", Desc: "rename"}, key.Hint{Key: "ctrl+x", Desc: "export"})
	}
	return renderDialogHints(p.hintWidth, hints)
}
