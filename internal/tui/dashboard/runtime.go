package dashboard

import (
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func loadSettingsRuntime(reload func() (*Model, error)) settingsSaved {
	next, err := reload()
	if next == nil && err == nil {
		err = errors.New("reload returned no replacement runtime")
	}
	return settingsSaved{next: next, reloadErr: err}
}

// WithRuntime isolates asynchronous messages across complete model replacements.
// Primer tracks generations within one model; it cannot distinguish two models
// whose counters both start at one. Terminal control messages pass unchanged.
func WithRuntime(model *Model) tea.Model { return &runtimeModel{current: model} }

type (
	runtimeModel   struct{ current *Model }
	runtimeMessage struct {
		origin *Model
		msg    tea.Msg
	}
)

func (s *runtimeModel) Init() tea.Cmd  { return runtimeCommand(s.current, s.current.Init()) }
func (s *runtimeModel) View() tea.View { return s.current.View() }
func (s *runtimeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(runtimeMessage); ok {
		if result.origin != s.current {
			return s, nil
		}
		msg = result.msg
	}
	next, cmd := s.current.Update(msg)
	s.current = next.(*Model)
	return s, runtimeCommand(s.current, cmd)
}

func runtimeCommand(origin *Model, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch result := msg.(type) {
		case tea.BatchMsg:
			commands := make(tea.BatchMsg, len(result))
			for i, child := range result {
				commands[i] = runtimeCommand(origin, child)
			}
			return commands
		case task.FinishedMsg, detailDue:
			return runtimeMessage{origin: origin, msg: msg}
		default:
			return msg
		}
	}
}

// Retain local work as data, without retaining old clients or request closures.
type runtimeWorkspace struct {
	descriptions    map[descriptionIdentity]*descriptionDraft
	drafts          map[string]string
	fieldDrafts     map[string]string
	labelDrafts     map[string]labelDraft
	creation        *creationDraft
	marks           map[string]bool
	recent          []recentIssue
	operations      []operation
	nextOperation   uint64
	lastBatch       *bulkResult
	bulkDraft       string
	bulkTargets     map[string]bool
	issues          []envelope.IssueView
	selected, query string
	activeQuery     string
	filter          string
	facet           issueFacet
	viewName, tab   string
	detailKey       string
	detailSection   int
	detailFocused   bool
	previewFocused  bool
	standalone      bool
}

func (m *Model) localWorkspace() runtimeWorkspace {
	return runtimeWorkspace{
		descriptions: m.descriptionDrafts,
		drafts:       m.drafts, fieldDrafts: m.fieldDrafts, labelDrafts: m.labelDrafts, creation: m.creation,
		marks: m.marks, recent: m.recent, operations: m.operations, nextOperation: m.nextOperation,
		lastBatch: m.lastBatch, bulkDraft: m.bulkDraft, bulkTargets: m.bulkDraftTargets,
		issues: m.issues, selected: m.selectedKey(), query: m.customQuery, filter: m.localFilter,
		activeQuery: m.query,
		facet:       m.facet, viewName: m.viewName, tab: m.tabs.ActiveItem(),
		detailKey: m.detailKey, detailSection: m.detailSection, detailFocused: m.detailFocused,
		previewFocused: m.previewFocused, standalone: m.standalone,
	}
}

func (m *Model) adoptRuntime(next *Model) (tea.Model, tea.Cmd) {
	if m.workspaces == nil {
		m.workspaces = make(map[core.JiraScope]runtimeWorkspace)
	}
	m.workspaces[m.options.RuntimeScope] = m.localWorkspace()
	next.workspaces = m.workspaces
	if saved, ok := m.workspaces[next.options.RuntimeScope]; ok {
		next.descriptionDrafts = saved.descriptions
		next.drafts, next.fieldDrafts, next.labelDrafts = saved.drafts, saved.fieldDrafts, saved.labelDrafts
		next.creation, next.recent = saved.creation, saved.recent
		next.operations, next.nextOperation, next.lastBatch = saved.operations, saved.nextOperation, saved.lastBatch
		next.bulkDraft, next.bulkDraftTargets = saved.bulkDraft, saved.bulkTargets
		next.customQuery, next.viewName = saved.query, saved.viewName
		if i := slices.Index(next.tabs.Items(), saved.tab); i >= 0 {
			next.tabs.SetActive(i)
		}
		switch next.tabs.ActiveItem() {
		case "Project":
			next.query = next.options.ProjectQuery
		case "Search":
			if saved.query != "" {
				next.query = saved.query
			}
		}
		if next.query == saved.activeQuery {
			next.issues, next.marks = saved.issues, saved.marks
			next.localFilter, next.facet = saved.filter, saved.facet
		}
		next.detailFocused, next.previewFocused, next.detailSection = saved.detailFocused, saved.previewFocused, saved.detailSection
	}
	if m.options.StopRuntime != nil {
		m.options.StopRuntime()
	}
	if next.options.ApplyRuntime != nil {
		next.options.ApplyRuntime()
	}
	next.listZoom = m.listZoom && next.enabled("tui_preview")
	next.width, next.height = m.width, m.height
	next.applyStyles()
	next.dialogs = newCommandStack()
	next.resize()
	if saved, ok := m.workspaces[next.options.RuntimeScope]; ok {
		next.restoreCursor(saved.selected)
	}
	next.openSettings()
	next.settings.tabs.SetActive(m.settings.tabs.Active())
	next.settings.input.SetValue(m.settings.input.Value())
	next.rebuildSettings()
	if i, ok := m.settings.rows.Selected(); ok && i < len(m.settings.visible) {
		selected := m.settings.visible[i].key
		for index, s := range next.settings.visible {
			if s.key == selected {
				next.settings.rows.SetCursor(index)
				break
			}
		}
	}
	next.settings.notice = "Settings active. Runtime reloaded."
	load := next.Init()
	if saved, ok := m.workspaces[next.options.RuntimeScope]; ok && saved.standalone && saved.detailKey != "" {
		return next, tea.Batch(load, next.openIssue(saved.detailKey))
	}
	return next, load
}
