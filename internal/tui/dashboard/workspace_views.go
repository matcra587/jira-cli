package dashboard

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type viewContext struct {
	frame                         hierarchyFrame
	lens                          string
	detail                        *jira.IssueBean
	section, offset               int
	loaded                        bool
	windowSize                    int
	used                          uint64
	comments                      []jira.Comment
	commentsKey                   string
	commentsNext, commentsTotal   int
	commentsLoading               bool
	commentsErr                   error
	detailFocused, previewFocused bool
}

func (m *Model) viewTitle(id string) string {
	switch id {
	case "my-work":
		return "My work"
	case "project":
		return "Project"
	case "search":
		return "Search"
	case "hierarchy":
		return hierarchyTab
	}
	for _, section := range m.workspace.Sections {
		if section.ID == id {
			return section.Title
		}
	}
	return id
}

func (m *Model) activeViewID() string {
	i := m.tabs.Active()
	if i < 0 || i >= len(m.tabIDs) {
		return ""
	}
	return m.tabIDs[i]
}

func (m *Model) activateView(id string) {
	if !m.viewExists(id) {
		m.notice = "This view is no longer configured."
		return
	}
	i := slices.Index(m.tabIDs, id)
	if i < 0 {
		m.tabIDs = append(m.tabIDs, id)
		labels := m.tabs.Items()
		m.tabs.SetItems(append(labels, m.viewTitle(id)))
		i = len(m.tabIDs) - 1
	}
	m.tabs.SetActive(i)
}

func (m *Model) viewExists(id string) bool {
	return slices.Contains(config.BuiltinWorkspaceViews(), id) || slices.ContainsFunc(m.workspace.Sections, func(section config.WorkspaceSection) bool { return section.ID == id })
}

func (m *Model) initializeWorkspace() {
	m.workspace = m.options.Workspace.Clone()
	m.viewContexts = make(map[string]viewContext)
	m.activeLens = m.workspace.LandingLens
	labels := []string{}
	for _, id := range m.workspace.TabIDs() {
		if id == "project" && m.options.ProjectQuery == "" {
			continue
		}
		m.tabIDs = append(m.tabIDs, id)
		labels = append(labels, m.viewTitle(id))
	}
	if len(labels) == 0 {
		m.tabIDs = []string{"my-work"}
		labels = []string{"My work"}
	}
	m.tabs.SetItems(labels)
	m.tabs.SetActive(0)
	if id := m.workspace.LandingView; id != "" {
		if i := slices.Index(m.tabIDs, id); i >= 0 {
			m.tabs.SetActive(i)
		} else {
			m.notice = "Startup view is unavailable for this profile; using the first visible view."
		}
	}
	if m.options.Query != "" {
		m.activateView("search")
	}
	m.query, m.err = m.workspaceQuery(m.activeViewID())
}

func (m *Model) workspaceBaseQuery(id string) string {
	query := ""
	switch id {
	case "my-work":
		query = myWorkQuery
	case "project":
		query = m.options.ProjectQuery
	case "search":
		query = m.customQuery
	case "hierarchy":
		return m.hierarchyRootQuery()
	default:
		for _, section := range m.workspace.Sections {
			if section.ID == id {
				query = section.JQL
				break
			}
		}
	}
	return query
}

func (m *Model) workspaceQuery(id string) (string, error) {
	query := m.workspaceBaseQuery(id)
	if id == "hierarchy" {
		return query, nil
	}
	if query == "" || m.activeLens == "" {
		return query, nil
	}
	for _, lens := range m.workspace.Lenses {
		if lens.ID == m.activeLens {
			return core.NarrowJQL(query, lens.JQL)
		}
	}
	return "", fmt.Errorf("active workspace lens is unavailable")
}

func (m *Model) parkView() {
	frame := m.hierarchySnapshot()
	if m.hierarchyActive() {
		m.hierarchy.parked = &frame
	}
	m.viewContexts[m.activeViewID()] = viewContext{
		frame: frame, lens: m.activeLens, detail: m.detailIssue, section: m.detailSection, offset: m.preview.YOffset(), loaded: !m.loading || len(m.issues) > 0, windowSize: len(m.issues), used: m.interaction,
		comments: slices.Clone(m.comments), commentsKey: m.commentsKey, commentsNext: m.commentsNext, commentsTotal: m.commentsTotal, commentsLoading: m.commentsLoading, commentsErr: m.commentsErr, detailFocused: m.detailFocused, previewFocused: m.previewFocused,
	}
	m.trimViewContexts(0)
}

// Keep parked issue windows within the same total issue budget as an active
// list. Eviction keeps lightweight navigation/filter state for the next read.
func (m *Model) trimViewContexts(active int) {
	total := active
	for _, saved := range m.viewContexts {
		total += len(saved.frame.issues)
	}
	for total > command.MaxSearchIssues {
		oldest := ""
		var used uint64
		for id, saved := range m.viewContexts {
			if len(saved.frame.issues) > 0 && (oldest == "" || saved.used < used) {
				oldest, used = id, saved.used
			}
		}
		if oldest == "" {
			return
		}
		saved := m.viewContexts[oldest]
		total -= len(saved.frame.issues)
		saved.frame.issues = nil
		saved.frame.observed = false
		saved.frame.markers = nil
		saved.detail = nil
		saved.loaded = false
		m.viewContexts[oldest] = saved
	}
}

func (m *Model) restoreViewContext(saved viewContext) tea.Cmd {
	m.detailSection = 0
	command := m.restoreHierarchyFrame(saved.frame)
	m.detailSection = saved.section
	if !saved.loaded {
		m.hierarchy.selectAfterLoad = saved.frame.selected
		return m.readList(false, max(pageSize, saved.windowSize))
	}
	if saved.detail != nil && saved.detail.Key.Value == m.selectedKey() {
		m.detailIssue = saved.detail
		m.detailKey = m.selectedKey()
		m.detailLoading = false
		m.detailErr = nil
		m.comments, m.commentsKey, m.commentsNext, m.commentsTotal, m.commentsErr = saved.comments, saved.commentsKey, saved.commentsNext, saved.commentsTotal, saved.commentsErr
		m.detailFocused, m.previewFocused = saved.detailFocused, saved.previewFocused
		m.resize()
		m.syncPreview()
		m.preview.SetYOffset(saved.offset)
		if saved.section == 1 && (saved.commentsKey != m.detailKey || saved.commentsLoading && len(saved.comments) == 0) {
			m.resetComments()
			return m.loadComments(false)
		}
		return nil
	}
	return command
}

func (m *Model) openLenses() {
	choices := []choice{{label: "No lens", description: "Use the authored view query", value: ""}}
	for _, lens := range m.workspace.Lenses {
		description := lens.JQL
		if lens.ID == m.activeLens {
			description = "Current · " + description
		}
		choices = append(choices, choice{label: lens.Title, description: description, value: lens.ID})
	}
	m.pick("View lens", "workspace-lens", "", choices)
	m.dialogs.Top().(*pickerDialog).selectValue(m.activeLens)
}

func (m *Model) selectLens(id string) tea.Cmd {
	if id != "" && !slices.ContainsFunc(m.workspace.Lenses, func(lens config.WorkspaceSection) bool { return lens.ID == id }) {
		m.notice = "Choose an existing lens."
		return nil
	}
	if id == m.activeLens {
		return nil
	}
	m.parkView()
	m.activeLens = id
	if m.hierarchyActive() {
		m.notice = "Lens selected for ordinary views; native hierarchy remains unchanged."
		return nil
	}
	m.query, m.err = m.workspaceQuery(m.activeViewID())
	if m.err != nil {
		return nil
	}
	m.notice = "View lens changed. The JQL line shows the complete effective query."
	return m.newQuery()
}
