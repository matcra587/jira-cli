package dashboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestDashboardLayoutBoundsAndReadablePanes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		for _, dock := range []string{"auto", "left", "right", "bottom"} {
			for _, share := range []int{25, 40, 75} {
				for _, compact := range []bool{true, false} {
					t.Run(fmt.Sprintf("%dx%d/%s/%d/compact=%v", size.Width, size.Height, dock, share, compact), func(t *testing.T) {
						m := model(t, issue("DEMO-1", "界 🙂 Wide summary"), issue("DEMO-2", "Another issue"))
						m.options.SettingValues = map[string]string{"tui_preview_dock": dock, "tui_preview_size": strconv.Itoa(share)}
						m.preferences["tui_compact"] = compact
						m.Update(size)
						g := m.dashboardLayout()
						want := config.PreviewDock("")
						if size.Width == 159 {
							want = config.PreviewDockRight
							if dock == "left" {
								want = config.PreviewDockLeft
							}
						}
						if size.Height >= 24 && (dock == "bottom" || dock == "auto" && size.Width == 80) {
							want = config.PreviewDockBottom
						}
						if g.dock != want {
							t.Fatalf("dock=%q want=%q", g.dock, want)
						}
						if g.dock == config.PreviewDockRight || g.dock == config.PreviewDockLeft {
							if g.list.Dx() < 44 || g.preview.Dx() < 32 || g.list.Dx()+3+g.preview.Dx() != m.pageWidth() {
								t.Fatal("horizontal layout violated readable pane widths")
							}
						}
						if g.dock == config.PreviewDockBottom {
							if g.list.Dy() < 7 || g.preview.Dy() < 8 || g.list.Dy()+1+g.preview.Dy() != m.bodyHeight() {
								t.Fatal("vertical layout violated readable pane heights")
							}
						}
						if m.preview.Width() != max(1, g.preview.Dx()-2) || m.preview.Height() != max(1, g.preview.Dy()-2) {
							t.Fatal("viewport used different geometry from its frame")
						}
						lines := strings.Split(m.View().Content, "\n")
						if len(lines) != size.Height {
							t.Fatalf("frame height=%d want=%d", len(lines), size.Height)
						}
						for _, line := range lines {
							if xansi.WcWidth.StringWidth(line) != size.Width {
								t.Fatal("layout escaped the terminal width")
							}
						}
						if !strings.Contains(xansi.Strip(lines[len(lines)-1]), "q quit") {
							t.Fatal("layout clipped the quit control")
						}
					})
				}
			}
		}
	}
}

func TestPreviewMouseUsesRenderedPaneGeometry(t *testing.T) {
	for _, dock := range []string{"left", "right", "bottom"} {
		t.Run(dock, func(t *testing.T) {
			m := model(t, pagingIssues(1, 30)...)
			m.options.SettingValues = map[string]string{"tui_preview_dock": dock}
			m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
			g := m.dashboardLayout()
			x, y := 2+g.preview.Min.X+2, 4+g.preview.Min.Y+3
			selected := m.selectedKey()
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if !m.previewFocused || m.detailFocused || m.selectedKey() != selected {
				t.Fatal("preview click selected a list row or failed to focus the preview")
			}
			m.preview.SetContent(strings.Repeat("Scroll this preview\n", 100))
			m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
			if m.preview.YOffset() == 0 || m.selectedKey() != selected {
				t.Fatal("preview wheel changed list selection")
			}
			m.Update(tea.MouseWheelMsg{X: 2 + g.list.Min.X + 1, Y: 8, Button: tea.MouseWheelDown})
			if m.previewFocused || m.selectedKey() == selected {
				t.Fatal("list wheel was captured by the preview focus")
			}
			m.previewFocused = true
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			if m.previewFocused || m.split() {
				t.Fatal("resize retained an invisible preview focus")
			}
		})
	}
}

func TestLeftPreviewUsesOneGeometryForPaintAndPointer(t *testing.T) {
	m := model(t, pagingIssues(1, 30)...)
	m.options.SettingValues = map[string]string{"tui_preview_dock": "left", "tui_preview_size": "40"}
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	g := m.dashboardLayout()
	if g.dock != config.PreviewDock("left") || g.preview.Min.X != 0 || g.list.Min.X != g.preview.Max.X+3 {
		t.Fatal("left preview did not allocate adjacent nonoverlapping panes")
	}
	lines := strings.Split(xansi.Strip(m.View().Content), "\n")
	if !strings.Contains(lines[4][:g.preview.Dx()], "Issue") && !strings.Contains(lines[4][:g.preview.Dx()], "DEMO-1") {
		t.Fatal("left preview was allocated but painted on the right")
	}
	before := m.selectedKey()
	m.Update(tea.MouseClickMsg{X: 2 + g.preview.Min.X + 2, Y: 8, Button: tea.MouseLeft})
	if !m.previewFocused || m.selectedKey() != before {
		t.Fatal("left preview click changed list selection")
	}
	m.Update(tea.MouseClickMsg{X: 2 + g.list.Min.X + 2, Y: 8, Button: tea.MouseLeft})
	if m.previewFocused || m.selectedKey() == before {
		t.Fatal("right-hand issue row did not use the list's rendered origin")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if m.split() || m.previewFocused {
		t.Fatal("small terminal retained an invisible left preview")
	}
}

func TestListZoomRestoresLayoutAndKeepsSettings(t *testing.T) {
	m := model(t, pagingIssues(1, 20)...)
	m.options.SettingValues = map[string]string{"tui_preview_dock": "bottom", "tui_preview_size": "55"}
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	m.rows.SetCursor(8)
	before, selected := m.dashboardLayout(), m.selectedKey()
	values := maps.Clone(m.options.SettingValues)
	press(m, 'z')
	if !m.listZoom || m.split() || m.listWidth() != m.pageWidth() || m.selectedKey() != selected {
		t.Fatal("zoom lost selection or failed to enlarge the list")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.split() || !m.listZoom {
		t.Fatal("resize discarded temporary list zoom")
	}
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	press(m, 'z')
	if m.listZoom || m.dashboardLayout() != before || !maps.Equal(values, m.options.SettingValues) || m.selectedKey() != selected {
		t.Fatal("restore lost the configured layout or selection")
	}
	m.openFilter()
	press(m, 'z')
	if m.listZoom || m.localFilter != "z" {
		t.Fatal("zoom stole text input")
	}
}

func TestLayoutSettingsUseScalarEditorsAndCancelPreview(t *testing.T) {
	m := model(t, issue("DEMO-1", "Draft layout"))
	m.options.SettingValues = map[string]string{"tui_preview_dock": "right", "tui_preview_size": "40"}
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	before := m.dashboardLayout()
	m.openSettings()
	selectAuditSetting(t, m, "tui_preview_dock")
	m.Update(key.Replay("enter"))
	menu, ok := m.dialogs.Top().(*settingMenu)
	if !ok || menu.value() != "right" {
		t.Fatal("dock opened a boolean toggle instead of its current choice")
	}
	m.Update(key.Replay("down"))
	m.Update(key.Replay("enter"))
	if m.dashboardLayout().dock != config.PreviewDockBottom || m.settings.values["tui_preview_dock"] != "bottom" {
		t.Fatal("dock choice did not preview")
	}
	selectAuditSetting(t, m, "tui_preview_size")
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("ctrl+u"))
	m.Update(tea.PasteMsg{Content: "90"})
	m.Update(key.Replay("enter"))
	if m.settings.editor == nil || m.settings.values["tui_preview_size"] != "40" {
		t.Fatal("invalid size changed the draft")
	}
	m.Update(key.Replay("ctrl+u"))
	m.Update(tea.PasteMsg{Content: "55"})
	m.Update(key.Replay("enter"))
	if m.settings.editor != nil || m.settings.values["tui_preview_size"] != "55" || m.settingsChangeCount() != 2 {
		t.Fatal("valid percentage did not keep one scalar draft")
	}
	if _, boolean := m.preferences["tui_preview_size"]; boolean {
		t.Fatal("numeric layout preference entered the boolean owner")
	}
	m.Update(key.Replay("esc"))
	if m.settings != nil || m.dashboardLayout() != before {
		t.Fatal("cancel retained a draft dock or size")
	}
}

func TestShortBottomPreviewReservesDescriptionSpace(t *testing.T) {
	var bean jira.IssueBean
	data := `{"key":"DEMO-1","fields":{"summary":"Compact preview","status":{"name":"In progress"},"assignee":{"displayName":"Fixture person"},"priority":{"name":"High"},"issuetype":{"name":"Task"},"labels":["very-long-label-that-is-retained-in-full-issue-details"],"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Visible description body"}]}]}}}`
	if err := json.Unmarshal([]byte(data), &bean); err != nil {
		t.Fatal(err)
	}
	m := model(t, bean)
	m.options.SettingValues = map[string]string{"tui_preview_dock": "bottom"}
	m.detailIssue, m.detailKey, m.detailLoading = &bean, "DEMO-1", false
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(xansi.Strip(m.preview.View()), "Visible description body") {
		t.Fatal("stacked metadata displaced all description text")
	}
	m.detailFocused = true
	m.resize()
	full := xansi.Strip(m.preview.View())
	for _, value := range []string{"Status", "Assignee", "Priority", "Type", "Labels", "very-long-label-that-is-retained-in-full-issue-details", "Visible description body"} {
		if !strings.Contains(full, value) {
			t.Fatal("full details lost a field", value)
		}
	}
}

func TestLayoutFootersFitAndKeepContextControls(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 120, Height: 30}, {Width: 159, Height: 45}} {
		m := model(t, issue("DEMO-1", "Footer"))
		m.Update(size)
		for _, details := range []bool{false, true} {
			m.detailFocused = details
			m.resize()
			hints := xansi.Strip(m.hints())
			if xansi.WcWidth.StringWidth(hints) > m.pageWidth() || !strings.Contains(hints, "q quit") || !strings.Contains(hints, "ctrl+p") {
				t.Fatalf("context controls do not fit %dx%d: %s", size.Width, size.Height, hints)
			}
			if details && !strings.Contains(hints, "esc back") {
				t.Fatal("details hid its back control")
			}
			if size.Width >= 80 && !strings.Contains(hints, "z zoom") {
				t.Fatal("layout hid its zoom control")
			}
		}
	}
}

func TestReloadPreservesTemporaryZoomAndAdoptsSavedDock(t *testing.T) {
	old := model(t, issue("DEMO-1", "Retained issue"))
	press(old, 'z')
	old.openSettings()
	next := model(t, issue("DEMO-1", "Retained issue"))
	next.options.SettingValues = map[string]string{"tui_preview_dock": "bottom", "tui_preview_size": "55"}
	old.options.ReloadSettings = func() (*Model, error) { return next, nil }
	_, cmd := old.Update(key.Replay("ctrl+r"))
	updated, _ := old.Update(cmd())
	if updated != next || !next.listZoom || next.dashboardLayout().dock != config.PreviewDockBottom {
		t.Fatal("reload discarded zoom or failed to preview the new layout in Settings")
	}
	next.Update(key.Replay("esc"))
	if next.split() || !next.listZoom {
		t.Fatal("closing Settings discarded temporary zoom")
	}
	press(next, 'z')
	if next.dashboardLayout().dock != config.PreviewDockBottom || next.previewPreference("tui_preview_size") != "55" {
		t.Fatal("zoom restoration resurrected the old saved layout")
	}
}
