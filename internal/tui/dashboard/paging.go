package dashboard

import tea "charm.land/bubbletea/v2"

// autoPage is admitted only by forward list navigation, never rendering or a
// returned page. This avoids draining a query because a local filter is sparse.
func (m *Model) autoPage() tea.Cmd {
	if !m.enabled("tui_auto_page") || m.loading || m.err != nil || m.next == "" || m.next == m.autoPageCursor {
		return nil
	}
	index, ok := m.rows.Selected()
	if !ok || index < len(m.visible)-6 {
		return nil
	}
	cmd := m.load(true)
	if cmd != nil {
		m.autoPageCursor = m.next
	}
	return cmd
}
