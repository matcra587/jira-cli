package dashboard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func dashboardSearcher(
	ctx context.Context,
	client Client,
	parent bool,
) func(string, string) (envelope.IssueListOutput, error) {
	fields := []string{"summary", "status", "issuetype", "priority", "assignee", "updated", "labels"}
	if parent {
		fields = append(fields, "parent")
	}
	return func(query, cursor string) (envelope.IssueListOutput, error) {
		return command.WalkIssues(ctx, client, query, fields, cursor, command.SearchWalk{Limit: pageSize})
	}
}

func (m *Model) load(more bool) tea.Cmd {
	if m.loading {
		return nil
	}
	if more && (m.isLast || m.truncated || m.next == "" || len(m.issues) >= command.MaxSearchIssues) {
		return nil
	}
	if !more {
		m.autoPageCursor = ""
	}
	m.loading, m.loadingMore, m.err = true, more, nil
	cursor := ""
	if more {
		cursor = m.next
	}
	query, search := m.query, m.search
	if m.hierarchyActive() {
		search = m.hierarchySearch
	}
	return m.tasks.Start(task.Spec{Scope: listScope, Run: func() (any, error) { return search(query, cursor) }})
}

func (m *Model) selected() *envelope.IssueView {
	i, ok := m.rows.Selected()
	if !ok || i >= len(m.visible) {
		return nil
	}
	return &m.issues[m.visible[i]]
}

func (m *Model) selectedKey() string {
	if issue := m.selected(); issue != nil {
		return issue.Key
	}
	return ""
}

func (m *Model) switchView() tea.Cmd {
	switch m.tabs.ActiveItem() {
	case hierarchyTab:
		if m.hierarchy.parked != nil {
			return m.restoreHierarchyFrame(*m.hierarchy.parked)
		}
		m.hierarchy.location = nil
		m.query = m.hierarchyRootQuery()
	case "My work":
		m.query = myWorkQuery
	case "Project":
		m.query = m.options.ProjectQuery
	case "Search":
		if m.customQuery == "" {
			return m.editQuery()
		}
		m.query = m.customQuery
	}
	return m.newQuery()
}

func (m *Model) newQuery() tea.Cmd {
	m.resetQuery()
	return m.load(false)
}

func (m *Model) resetQuery() {
	// Primer has no cancel-only API. Advancing these generations with no Run
	// invalidates old reads without scheduling any replacement I/O.
	m.tasks.Start(task.Spec{Scope: listScope})
	m.tasks.Start(task.Spec{Scope: detailScope})
	m.tasks.Start(task.Spec{Scope: hierarchyScope})
	// A new generation supersedes the old query even while its HTTP read runs.
	m.loading, m.loadingMore = false, false
	m.issues, m.next, m.err = nil, "", nil
	m.marks = nil
	m.localFilter = ""
	m.facet = issueFacet{}
	m.filtering = false
	m.markAnchor = ""
	m.detailKey, m.detailIssue, m.detailErr = "", nil, nil
	m.standalone = false
	m.detailLoading, m.detailFocused, m.previewFocused = false, false, false
	m.isLast, m.truncated = false, false
	m.progress = command.NewSearchProgress("", false)
	m.rebuildRows()
	m.syncPreview()
}

func (m *Model) editQuery() tea.Cmd {
	m.parkHierarchy()
	m.editing = true
	m.input.SetValue(m.query)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *Model) acceptList(msg task.FinishedMsg) tea.Cmd {
	more := m.loadingMore
	m.loading, m.loadingMore, m.err = false, false, msg.Err
	if msg.Err != nil {
		return nil
	}
	page, ok := msg.Result.(envelope.IssueListOutput)
	if !ok {
		m.err = fmt.Errorf("unexpected dashboard search result")
		return nil
	}
	if err := m.validateHierarchyPage(page); err != nil {
		m.err, m.truncated = err, true
		return nil
	}
	keep := m.selectedKey()
	if !more && m.hierarchy.selectAfterLoad != "" {
		keep, m.hierarchy.selectAfterLoad = m.hierarchy.selectAfterLoad, ""
	}
	seen := make(map[string]bool, len(m.issues))
	if more {
		for _, issue := range m.issues {
			seen[issue.Key] = true
		}
	}
	for _, issue := range page.Issues {
		if seen[issue.Key] {
			m.err = fmt.Errorf("search repeated an issue; refresh to restart")
			m.truncated = true
			return nil
		}
		seen[issue.Key] = true
	}
	if !more {
		m.issues = nil
		m.progress = command.NewSearchProgress("", false)
	}
	m.issues = append(m.issues, page.Issues...)
	m.next, m.isLast, m.truncated = page.Pagination.NextPageToken, page.Pagination.IsLast, page.Truncated
	if m.truncated {
		m.err = fmt.Errorf("search incomplete (%s); refresh to restart", page.TruncatedReason)
	}
	if !m.isLast && !m.truncated {
		if reason := m.progress.Advance(m.next, len(page.Issues)); reason != "" {
			m.truncated = true
			m.err = fmt.Errorf("search incomplete (%s); refresh to restart", reason)
		}
	}
	if more && m.err == nil {
		m.notice = fmt.Sprintf("Loaded %d more issues.", len(page.Issues))
	}
	if m.hierarchyActive() && m.hierarchy.location != nil && m.hierarchy.location.warning != "" {
		m.notice = m.hierarchy.location.warning
	}
	m.pruneMarks()
	m.rebuildRows()
	m.restoreCursor(keep)
	if m.standalone && m.detailFocused {
		return nil
	}
	// An explicit refresh fetches the selected detail again, even if its key stayed.
	if !more {
		m.detailKey = ""
	}
	return m.selectDetail()
}

func (m *Model) updateQuery(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return nil
	case "enter":
		query := strings.TrimSpace(m.input.Value())
		if query == "" {
			return nil
		}
		m.query, m.customQuery, m.editing = query, query, false
		m.viewName = ""
		m.input.Blur()
		m.tabs.SetActive(slices.Index(m.tabs.Items(), "Search"))
		return m.newQuery()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}
