package dashboard

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	hierarchyTab                = "Hierarchy"
	hierarchyScope   task.Scope = "hierarchy"
	maxHierarchyBack            = 12
)

type hierarchyLocation struct {
	read    core.IssueHierarchyRead
	warning string
}

type hierarchyFrame struct {
	autoPageCursor              string
	tab, query, title, selected string
	issues                      []envelope.IssueView
	next                        string
	last, truncated             bool
	progress                    *command.SearchProgress
	filter                      string
	facet                       issueFacet
	marks                       map[string]bool
	anchor                      string
	err                         error
	location                    *hierarchyLocation
}

type hierarchyWorkspace struct {
	location        *hierarchyLocation
	parked          *hierarchyFrame
	back            []hierarchyFrame
	selectAfterLoad string
}

type hierarchyResult struct {
	request     uint64
	key         string
	around      bool
	selectChild string
	read        core.IssueHierarchyRead
}

func (m *Model) hierarchyActive() bool { return m.tabs.ActiveItem() == hierarchyTab }

func (m *Model) hierarchyRootQuery() string {
	if value := m.options.SettingValues["tui_hierarchy_query"]; value != "" {
		return value
	}
	return config.DefaultTUIHierarchyQuery
}

func (m *Model) hierarchySnapshot() hierarchyFrame {
	return hierarchyFrame{
		autoPageCursor: m.autoPageCursor,
		tab:            m.tabs.ActiveItem(), query: m.query, title: m.viewName, selected: m.selectedKey(),
		issues: slices.Clone(m.issues), next: m.next, last: m.isLast, truncated: m.truncated,
		progress: m.progress, filter: m.localFilter, facet: m.facet, marks: maps.Clone(m.marks), anchor: m.markAnchor,
		err: m.err, location: m.hierarchy.location,
	}
}

func (m *Model) parkHierarchy() {
	if m.hierarchyActive() {
		frame := m.hierarchySnapshot()
		m.hierarchy.parked = &frame
	}
}

func (m *Model) restoreHierarchyFrame(frame hierarchyFrame) tea.Cmd {
	m.tabs.SetActive(slices.Index(m.tabs.Items(), frame.tab))
	m.query, m.viewName = frame.query, frame.title
	m.hierarchy.location = frame.location
	m.hierarchy.selectAfterLoad = ""
	if frame.tab == hierarchyTab && frame.location == nil && frame.query != m.hierarchyRootQuery() {
		m.query = m.hierarchyRootQuery()
		return m.newQuery()
	}
	// Supersede in-flight pages before restoring an independently owned snapshot.
	m.resetQuery()
	m.issues, m.next, m.isLast, m.truncated = frame.issues, frame.next, frame.last, frame.truncated
	m.progress, m.localFilter, m.facet, m.marks, m.markAnchor = frame.progress, frame.filter, frame.facet, frame.marks, frame.anchor
	m.err, m.autoPageCursor = frame.err, frame.autoPageCursor
	m.rebuildRows()
	m.restoreCursor(frame.selected)
	return m.selectDetail()
}

func (m *Model) hierarchyHome() tea.Cmd {
	if !m.hierarchyActive() {
		m.hierarchy.back = []hierarchyFrame{m.hierarchySnapshot()}
	} else {
		m.hierarchy.back = nil
	}
	m.tabs.SetActive(slices.Index(m.tabs.Items(), hierarchyTab))
	m.hierarchy.location, m.hierarchy.parked = nil, nil
	m.hierarchy.selectAfterLoad = ""
	m.query, m.viewName = m.hierarchyRootQuery(), ""
	m.notice = "Hierarchy roots · → children · ← parent · backspace back"
	return m.newQuery()
}

func (m *Model) browseHierarchy(around bool) tea.Cmd {
	issue := m.selected()
	if m.detailFocused || m.previewFocused {
		if m.detailIssue != nil {
			return m.startHierarchy(core.IssueHierarchyReference{ID: m.detailIssue.ID.Value, Key: m.detailIssue.Key.Value}, around, "")
		}
	}
	if issue == nil {
		m.notice = "Select an issue, or press H for hierarchy roots."
		return nil
	}
	return m.startHierarchy(core.IssueHierarchyReference{ID: issue.ID, Key: issue.Key}, around, "")
}

func (m *Model) startHierarchy(reference core.IssueHierarchyReference, around bool, selectChild string) tea.Cmd {
	request, key, read := m.interaction, m.activeIssueKey(), m.hierarchyRead
	m.notice = "Reading native parent relationships…"
	return m.tasks.Start(task.Spec{Scope: hierarchyScope, Run: func() (any, error) {
		value, err := read(reference)
		return hierarchyResult{request: request, key: key, around: around, selectChild: selectChild, read: value}, err
	}})
}

func (m *Model) acceptHierarchy(msg task.FinishedMsg) tea.Cmd {
	result, ok := msg.Result.(hierarchyResult)
	if !ok || result.request != m.interaction || result.key != m.activeIssueKey() {
		return nil
	}
	if errors.Is(msg.Err, core.ErrIssueHierarchyInvalid) || len(result.read.Nodes) == 0 {
		err := msg.Err
		if err == nil {
			err = core.ErrIssueHierarchyInvalid
		}
		m.notice = "Hierarchy unavailable: " + m.errorText(err)
		return nil
	}
	if result.around && len(result.read.Nodes) > 1 {
		result.selectChild = result.read.Nodes[0].Key
		result.read.Nodes = result.read.Nodes[1:]
	}
	root := result.read.Nodes[0]
	literal, err := command.QuoteJQL(root.ID)
	if err != nil {
		m.notice = m.errorText(err)
		return nil
	}
	previous := m.hierarchySnapshot()
	if !m.hierarchyActive() {
		m.hierarchy.back = nil
	}
	m.hierarchy.back = append(m.hierarchy.back, previous)
	m.hierarchy.back = m.hierarchy.back[max(0, len(m.hierarchy.back)-maxHierarchyBack):]
	warning := ""
	if msg.Err != nil {
		warning = "Ancestors incomplete: " + m.errorText(msg.Err)
	} else if !result.read.Complete {
		warning = "Ancestors incomplete: parent information was not returned or reached the read bound."
	}
	m.hierarchy.location = &hierarchyLocation{read: result.read, warning: warning}
	m.hierarchy.selectAfterLoad = result.selectChild
	m.tabs.SetActive(slices.Index(m.tabs.Items(), hierarchyTab))
	m.query, m.viewName = "parent = "+literal+" ORDER BY updated DESC", ""
	m.notice = warning
	return m.newQuery()
}

func (m *Model) hierarchyParent() tea.Cmd {
	if !m.hierarchyActive() {
		return nil
	}
	location := m.hierarchy.location
	if location == nil {
		return m.browseHierarchy(true)
	}
	root := location.read.Nodes[0]
	if root.ParentState == core.IssueParentNone {
		m.notice = "Jira returned no parent for this issue."
		return nil
	}
	if root.ParentState == core.IssueParentUnknown {
		m.notice = "Parent not returned; a higher level is unavailable from this observation."
		return nil
	}
	return m.startHierarchy(root.Parent, false, root.Key)
}

func (m *Model) hierarchyBack() tea.Cmd {
	if !m.hierarchyActive() {
		return nil
	}
	if len(m.hierarchy.back) == 0 {
		if m.hierarchy.location != nil {
			return m.hierarchyHome()
		}
		m.notice = "At hierarchy roots. Use Tab to switch views."
		return nil
	}
	last := len(m.hierarchy.back) - 1
	frame := m.hierarchy.back[last]
	m.hierarchy.back = m.hierarchy.back[:last]
	m.parkHierarchy()
	return m.restoreHierarchyFrame(frame)
}

func (m *Model) hierarchyBreadcrumb() string {
	if m.hierarchy.location == nil {
		return "Configured roots · → children · ← parent"
	}
	read := m.hierarchy.location.read
	parts := []string{}
	if !read.Complete {
		parts = append(parts, "…")
	}
	for i := len(read.Nodes) - 1; i >= 0; i-- {
		node := read.Nodes[i]
		parts = append(parts, strings.TrimSpace(node.Type+" "+node.Key))
	}
	return cli.Text(strings.Join(parts, " › "))
}

func (m *Model) validateHierarchyPage(page envelope.IssueListOutput) error {
	if !m.hierarchyActive() || m.hierarchy.location == nil {
		return nil
	}
	root := m.hierarchy.location.read.Nodes[0]
	for _, issue := range page.Issues {
		for _, ancestor := range m.hierarchy.location.read.Nodes {
			sameID := issue.ID != "" && issue.ID == ancestor.ID
			if sameID || issue.Key == ancestor.Key {
				return fmt.Errorf("%w: search returned an ancestor as its child", core.ErrIssueHierarchyInvalid)
			}
		}
		state, parent, err := core.DecodeIssueParent(issue.Fields)
		if err != nil {
			return err
		}
		wrongID := parent.ID != "" && parent.ID != root.ID
		wrongKey := parent.ID == "" && parent.Key != root.Key
		contradictsRoot := state == core.IssueParentKnown && (wrongID || wrongKey)
		if state == core.IssueParentNone || contradictsRoot {
			return fmt.Errorf("%w: returned child identifies a different parent; refresh to reconcile", core.ErrIssueHierarchyInvalid)
		}
	}
	return nil
}
