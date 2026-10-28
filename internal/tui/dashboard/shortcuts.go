package dashboard

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
)

type browseResult struct{ key, url string }

func (m *Model) openBrowser() tea.Cmd {
	target := m.activeIssueKey()
	if target == "" {
		m.notice = "Select an issue to open."
		return nil
	}
	if m.options.Actions.Browse == nil {
		m.notice = "Browser opening is unavailable."
		return nil
	}
	browse := m.options.Actions.Browse
	return m.tasks.Start(task.Spec{Scope: "browse", Run: func() (any, error) { url, err := browse(target); return browseResult{key: target, url: url}, err }})
}

func (m *Model) acceptBrowse(msg task.FinishedMsg) {
	result, ok := msg.Result.(browseResult)
	if !ok {
		return
	}
	if msg.Err != nil {
		m.notice = fmt.Sprintf("Browser handoff failed for %s: %s · %s", cli.Text(result.key), m.errorText(msg.Err), cli.Text(result.url))
		return
	}
	m.notice = "Browser handoff for " + cli.Text(result.key) + "; page loading is not confirmed."
}

func (m *Model) copyIssue(link bool) tea.Cmd {
	value := m.activeIssueKey()
	if value == "" {
		m.notice = "Select an issue to copy."
		return nil
	}
	kind := "key"
	if link {
		url, ok := cli.IssueBrowseURL(m.site, value)
		if !ok {
			m.notice = "No valid issue URL is available."
			return nil
		}
		value, kind = url, "URL"
	}
	m.notice = "Sent issue " + kind + " to the terminal clipboard; terminal support is required."
	return tea.SetClipboard(value)
}

// Row coordinates include the two-cell page margin and the four header rows.
// The list title, blank line and column headings precede its first item.
func (m *Model) clickIssue(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button != tea.MouseLeft || m.detailFocused || m.help || m.editing || m.filtering {
		return nil
	}
	if msg.X < 2 || msg.X >= 2+m.listWidth() || msg.Y < 7 || msg.Y >= 4+m.bodyHeight() {
		return nil
	}
	offset := msg.Y - 7
	if offset%m.rowStride() != 0 {
		return nil
	}
	start, end := m.rows.VisibleRange()
	row := start + offset/m.rowStride()
	if row >= end {
		return nil
	}
	m.previewFocused = false
	if row == m.rows.Cursor() {
		m.detailFocused = true
		m.resize()
		return nil
	}
	m.rows.SetCursor(row)
	return m.selectDetail()
}
