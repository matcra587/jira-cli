package dashboard

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/matcra587/jira-cli/internal/cli"
)

func (m *Model) compactViewTabs() bool {
	return ansi.WcWidth.StringWidth(m.tabs.View()) > m.pageWidth()
}

// A compact strip keeps the active view visible instead of clipping arbitrary
// configured labels. Primer owns selection; the ordinary picker exposes all tabs.
func (m *Model) viewTabStrip() string {
	if !m.compactViewTabs() {
		return m.tabs.View()
	}
	position := fmt.Sprintf(" %d/%d ▾", m.tabs.Active()+1, len(m.tabIDs))
	name := ansi.WcWidth.Truncate(cli.Text(m.tabs.ActiveItem()), max(1, m.pageWidth()-ansi.WcWidth.StringWidth(position)-2), "…")
	return accent("‹ "+name) + dim(position)
}

func (m *Model) openWorkspaceTabs() {
	choices := make([]choice, 0, len(m.tabIDs))
	for _, id := range m.tabIDs {
		description := m.workspaceBaseQuery(id)
		if id == m.activeViewID() {
			description = "Current view · " + description
		}
		choices = append(choices, choice{label: m.viewTitle(id), description: description, value: id})
	}
	m.pick("Switch view", "workspace-tab", "", choices)
	m.dialogs.Top().(*pickerDialog).selectValue(m.activeViewID())
}

func (m *Model) workspaceTabExists(id string) bool { return slices.Contains(m.tabIDs, id) }
