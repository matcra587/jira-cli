package dashboard

import (
	"image"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/matcra587/jira-cli/internal/config"
)

// dashboardLayout is the shared geometry for painting, viewports and mouse hits.
// Primer provides cell-aware framing; the pane policy is application-owned.
type dashboardLayout struct {
	dock          config.PreviewDock
	list, preview image.Rectangle
}

func (m *Model) previewPreference(name string) string {
	if m.settings != nil {
		return m.settings.values[name]
	}
	return m.options.SettingValues[name]
}

func (m *Model) dashboardLayout() dashboardLayout {
	width, height := m.pageWidth(), m.bodyHeight()
	result := dashboardLayout{list: image.Rect(0, 0, width, height), preview: image.Rect(0, 0, width, height)}
	if !m.enabled("tui_preview") || m.detailFocused || m.listZoom && m.settings == nil {
		return result
	}
	dock := config.PreviewDock(m.previewPreference("tui_preview_dock"))
	if dock == "" || dock == config.PreviewDockAuto {
		switch {
		case width >= 110 && m.height >= 24:
			dock = config.PreviewDockRight
		case width >= 60 && m.height >= 24:
			dock = config.PreviewDockBottom
		}
	}
	share, err := strconv.Atoi(m.previewPreference("tui_preview_size"))
	if err != nil || share < config.MinTUIPreviewSize || share > config.MaxTUIPreviewSize {
		share = config.DefaultTUIPreviewSize
	}
	switch {
	case (dock == config.PreviewDockRight || dock == config.PreviewDockLeft) && width >= 80 && m.height >= 24:
		available := width - 3
		previewWidth := max(32, min(available-44, available*share/100))
		listWidth := available - previewWidth
		result.dock = dock
		result.list = image.Rect(0, 0, listWidth, height)
		result.preview = image.Rect(listWidth+3, 0, width, height)
		if dock == config.PreviewDockLeft {
			result.preview = image.Rect(0, 0, previewWidth, height)
			result.list = image.Rect(previewWidth+3, 0, width, height)
		}
	case dock == config.PreviewDockBottom && width >= 60 && m.height >= 24:
		available := height - 1
		previewHeight := max(8, min(available-7, available*share/100))
		listHeight := available - previewHeight
		result.dock = dock
		result.list = image.Rect(0, 0, width, listHeight)
		result.preview = image.Rect(0, listHeight+1, width, height)
	}
	return result
}

func (m *Model) split() bool        { return m.dashboardLayout().dock != "" }
func (m *Model) listWidth() int     { return m.dashboardLayout().list.Dx() }
func (m *Model) listHeight() int    { return m.dashboardLayout().list.Dy() }
func (m *Model) previewWidth() int  { return m.dashboardLayout().preview.Dx() }
func (m *Model) previewHeight() int { return m.dashboardLayout().preview.Dy() }

func (m *Model) listContains(x, y int) bool {
	return image.Pt(x-2, y-4).In(m.dashboardLayout().list)
}

func (m *Model) previewContains(x, y int) bool {
	geometry := m.dashboardLayout()
	return geometry.dock != "" && image.Pt(x-2, y-4).In(geometry.preview)
}

func (m *Model) toggleListZoom() tea.Cmd {
	if !m.listZoom && !m.enabled("tui_preview") {
		m.notice = "Enable Show issue preview in Settings to use list zoom."
		return nil
	}
	if !m.listZoom && !m.split() && !m.detailFocused {
		m.notice = "The list already fills this terminal. " + m.effectiveKey("enter") + " opens issue details."
		return nil
	}
	m.listZoom = !m.listZoom
	m.detailFocused, m.previewFocused = false, false
	m.resize()
	m.notice = "List zoom · " + m.effectiveKey("z") + " restores the configured preview."
	if !m.listZoom {
		m.notice = "Preview layout restored; both panes must fit."
	}
	if m.standalone {
		m.standalone = false
		return m.selectDetail()
	}
	return nil
}
