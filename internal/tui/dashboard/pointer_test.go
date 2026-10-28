package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
)

func TestPointerNavigatesVisibleTabsWithoutFocusingSearchInput(t *testing.T) {
	m := model(t, issue("DEMO-1", "Original"))
	m.Update(tea.MouseClickMsg{X: 12, Y: 1, Button: tea.MouseLeft})
	if !m.emptySearch() || m.editing {
		t.Fatal("clicking Search started an editor or kept another tab")
	}
	m.Update(tea.MouseClickMsg{X: 19, Y: 1, Button: tea.MouseLeft})
	if !m.hierarchyActive() {
		t.Fatal("clicking Hierarchy did not navigate")
	}
	m.Update(tea.MouseClickMsg{X: 9, Y: 1, Button: tea.MouseLeft})
	if !m.hierarchyActive() {
		t.Fatal("tab separator acted as an invisible tab")
	}
	press(m, '/')
	m.input.SetValue("unsubmitted query")
	m.Update(tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft})
	if !m.editing || m.input.Value() != "unsubmitted query" || !m.hierarchyActive() {
		t.Fatal("page click stole focus from the query editor")
	}
}

func TestPointerDetailSectionsMatchWideAndCompactRendering(t *testing.T) {
	for _, dock := range []string{"right", "left", "bottom"} {
		t.Run(dock, func(t *testing.T) {
			m := model(t, issue("DEMO-1", "Current"))
			m.options.SettingValues = map[string]string{"tui_preview_dock": dock, "tui_preview_size": "40"}
			m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
			g := m.dashboardLayout()
			// "Description · " takes fourteen cells; Activity starts after
			// "Comments · ", at twenty-five cells.
			m.Update(tea.MouseClickMsg{X: 2 + g.preview.Min.X + 26, Y: 4 + g.preview.Min.Y + 1, Button: tea.MouseLeft})
			if g.preview.Dx() < 65 {
				if !m.dialogs.Active() {
					t.Fatal("compact section heading did not expose a choice menu")
				}
				m.Update(tea.PasteMsg{Content: "Activity"})
				_, command := m.Update(key.Replay("enter"))
				applyCommand(m, command)
			}
			if m.detailSection != 2 || !m.previewFocused {
				t.Fatal("visible section navigation did not focus Activity")
			}
			if g.preview.Dx() < 65 {
				m.Update(tea.MouseClickMsg{X: 2 + g.preview.Min.X + 3, Y: 4 + g.preview.Min.Y + 1, Button: tea.MouseLeft})
				_, command := m.Update(key.Replay("enter"))
				applyCommand(m, command)
				if m.detailSection != 2 {
					t.Fatal("reopened section menu did not retain the current choice")
				}
			}
			m.openCommands()
			m.Update(tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft})
			if !m.dialogs.Active() || m.tabs.ActiveItem() != "My work" {
				t.Fatal("overlay mouse event leaked to the underlying view tabs")
			}
		})
	}
}

func TestPointerLabelHitsUseCellsAndVisibleBounds(t *testing.T) {
	labels := []string{"界", "Next"}
	if index, ok := labelAtCell(labels, "  ", 4, 8); !ok || index != 1 {
		t.Fatal("wide label shifted the next tab's hit region")
	}
	for _, cell := range []int{-1, 2, 3, 8, 20} {
		if _, found := labelAtCell(labels, "  ", cell, 8); found {
			t.Fatalf("non-label or clipped cell %d remained clickable", cell)
		}
	}
}
