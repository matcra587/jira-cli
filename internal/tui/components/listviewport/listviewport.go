package listviewport

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/gechr/primer/scrollbar"
)

// sgrPattern matches ANSI SGR (color/style) escape sequences. The cursor row is
// stripped of them before the reverse-video highlight is applied, because an
// inner reset (\x1b[m emitted by a styled cell) would otherwise terminate the
// highlight partway across the row.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Model is a list viewport over caller-rendered rows.
type Model struct {
	rows   []string
	cursor int // selected row index, always within [0, len(rows))
	offset int // index of the first visible row
	width  int
	height int // number of rows visible at once

	// CursorStyle highlights the selected row. The zero value (reverse video)
	// is usable without configuration.
	CursorStyle lipgloss.Style
	// Scrollbar draws a proportional scrollbar column when the list overflows.
	Scrollbar bool
}

// New returns a list viewport with a sensible default cursor highlight. It
// returns a pointer because every mutating method has a pointer receiver.
func New() *Model {
	return &Model{
		CursorStyle: lipgloss.NewStyle().Reverse(true),
		Scrollbar:   true,
	}
}

// SetRows replaces the rows and clamps the cursor/offset to the new length so
// the selection stays valid (and visible) when the data shrinks.
func (m *Model) SetRows(rows []string) {
	m.rows = rows
	m.clamp()
}

// SetSize sets the viewport dimensions and reclamps so the cursor stays visible.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.clamp()
}

// Len returns the number of rows.
func (m *Model) Len() int { return len(m.rows) }

// Cursor returns the selected row index, or -1 when the list is empty.
func (m *Model) Cursor() int {
	if len(m.rows) == 0 {
		return -1
	}
	return m.cursor
}

// SetCursor selects row i, clamping to range and scrolling it into view.
func (m *Model) SetCursor(i int) {
	m.cursor = i
	m.clamp()
}

// MoveDown moves the cursor down by n (n may be negative).
func (m *Model) MoveDown(n int) { m.SetCursor(m.cursor + n) }

// MoveUp moves the cursor up by n.
func (m *Model) MoveUp(n int) { m.SetCursor(m.cursor - n) }

// Top selects the first row.
func (m *Model) Top() { m.SetCursor(0) }

// Bottom selects the last row.
func (m *Model) Bottom() { m.SetCursor(len(m.rows) - 1) }

// PageDown moves the cursor down by a viewport height.
func (m *Model) PageDown() { m.MoveDown(m.pageStride()) }

// PageUp moves the cursor up by a viewport height.
func (m *Model) PageUp() { m.MoveUp(m.pageStride()) }

func (m *Model) pageStride() int {
	if m.height > 1 {
		return m.height - 1 // keep one row of context across pages
	}
	return 1
}

// VisibleRange returns the [start, end) row indices currently on screen.
func (m *Model) VisibleRange() (start, end int) {
	if m.height <= 0 || len(m.rows) == 0 {
		return 0, 0
	}
	end = min(m.offset+m.height, len(m.rows))
	return m.offset, end
}

// clamp keeps cursor in range and adjusts offset so the cursor is visible and
// the window never scrolls past the end. This single method is the only place
// that mutates offset, so the visible-window invariant holds everywhere.
func (m *Model) clamp() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.height <= 0 {
		m.offset = 0
		return
	}
	// Pull the window to the cursor.
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	// Never leave a gap at the bottom when enough rows exist to fill the view.
	maxOffset := max(len(m.rows)-m.height, 0)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// View renders the visible rows, highlighting the cursor row and appending a
// scrollbar column when the list overflows and Scrollbar is enabled. Rows are
// padded/truncated to the content width (viewport width minus the scrollbar
// column) so the cursor highlight spans the full row and the output never
// exceeds the declared width.
func (m *Model) View() string {
	if m.height <= 0 || len(m.rows) == 0 {
		return ""
	}
	start, end := m.VisibleRange()
	bar := m.scrollbarColumn()

	contentWidth := m.width
	if bar != nil {
		contentWidth-- // reserve a column for the scrollbar
	}
	var rowStyle lipgloss.Style
	if contentWidth > 0 {
		rowStyle = lipgloss.NewStyle().Width(contentWidth).MaxWidth(contentWidth).Inline(true)
	}

	lines := make([]string, 0, m.height)
	for i := start; i < end; i++ {
		row := m.rows[i]
		if i == m.cursor {
			// Drop per-cell colors so the reverse-video highlight spans the whole
			// row instead of being cut short by an inner reset.
			row = sgrPattern.ReplaceAllString(row, "")
		}
		if contentWidth > 0 {
			row = rowStyle.Render(row)
		}
		if i == m.cursor {
			row = m.CursorStyle.Render(row)
		}
		if bar != nil {
			row = lipgloss.JoinHorizontal(lipgloss.Top, row, bar[i-start])
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

// scrollbarColumn returns one glyph per visible row, or nil when no scrollbar is
// needed (disabled, or the list fits). The thumb position uses the normalized
// scroll fraction (0 at the top, 1 at the bottom) — not the less-style
// percentage, which is anchored to the bottom of the viewport and would place
// the thumb at the end while the list is still at the top.
func (m *Model) scrollbarColumn() []string {
	if !m.Scrollbar || len(m.rows) <= m.height || m.height <= 0 {
		return nil
	}
	maxOffset := len(m.rows) - m.height
	fraction := float64(m.offset) / float64(maxOffset)
	thumbPos, thumbSize := scrollbar.ThumbMetrics(m.height, len(m.rows), fraction)
	thumbEnd := thumbPos + thumbSize - 1
	col := make([]string, m.height)
	for i := range col {
		if i >= thumbPos && i <= thumbEnd {
			col[i] = "█"
		} else {
			col[i] = "│"
		}
	}
	return col
}
