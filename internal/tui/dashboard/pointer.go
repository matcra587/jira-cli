package dashboard

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
)

// Primer's carousel exposes rendered labels but no hit-test API. Use the same
// display-cell widths and separators as its View, bounded by the painted strip.
func labelAtCell(labels []string, separator string, cell, width int) (int, bool) {
	if cell < 0 || cell >= width {
		return 0, false
	}
	start := 0
	for i, label := range labels {
		end := start + ansi.WcWidth.StringWidth(label)
		if cell >= start && cell < end {
			return i, true
		}
		start = end + ansi.WcWidth.StringWidth(separator)
	}
	return 0, false
}

func (m *Model) clickWorkspace(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.help || m.editing || m.filtering || m.width < 40 || m.height < 12 {
		return nil
	}
	if msg.Y == 1 {
		index, found := labelAtCell(m.tabs.Items(), m.tabs.Separator, msg.X-2, m.pageWidth())
		if !found || index == m.tabs.Active() {
			return nil
		}
		m.parkHierarchy()
		m.tabs.SetActive(index)
		return m.switchView()
	}
	geometry := m.dashboardLayout()
	if m.detailFocused || geometry.dock != "" {
		x, y := msg.X-2-geometry.preview.Min.X, msg.Y-4-geometry.preview.Min.Y
		if y == 1 && x >= 0 && x < geometry.preview.Dx() {
			if geometry.preview.Dx() < 65 {
				m.openDetailSections()
				return nil
			}
			if index, found := labelAtCell(detailSections, " · ", x, geometry.preview.Dx()); found {
				m.previewFocused = !m.detailFocused
				return m.changeDetailSection(index - m.detailSection)
			}
			return nil
		}
	}
	return m.clickIssue(msg)
}

func (m *Model) openDetailSections() {
	choices := make([]choice, len(detailSections))
	for i, name := range detailSections {
		choices[i] = choice{label: name, value: name}
		if i == m.detailSection {
			choices[i].description = "Current section"
		}
	}
	m.pick("Issue sections", "detail-section", "", choices)
	picker := m.dialogs.Top().(*pickerDialog)
	for range m.detailSection {
		picker.menu.Update(key.Replay("down"))
	}
}
