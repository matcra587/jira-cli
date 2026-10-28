package dashboard

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
)

// SavedView retains authored JQL; selecting a view never injects a hidden scope.
type SavedView struct{ Name, Description, Query string }

type viewsResult struct {
	request uint64
	views   []SavedView
}

type recentIssue struct{ key, summary string }

func (m *Model) rememberIssue(key, summary string) {
	m.recent = slices.DeleteFunc(m.recent, func(v recentIssue) bool { return v.key == key })
	m.recent = append([]recentIssue{{key: key, summary: summary}}, m.recent...)
	m.recent = m.recent[:min(20, len(m.recent))]
}

func (m *Model) openIssues() {
	choices := make([]choice, 0, len(m.recent)+len(m.issues))
	seen := make(map[string]bool)
	for _, issue := range m.recent {
		seen[issue.key] = true
		choices = append(choices, choice{label: cli.Text(issue.key + " " + issue.summary), description: "Recent", value: issue.key})
	}
	for _, issue := range m.issues {
		if !seen[issue.Key] {
			choices = append(choices, choice{label: cli.Text(issue.Key + " " + issue.Fields.Summary.Value), description: "Current view", value: issue.Key})
		}
	}
	m.pick("Switch issue", "issue", "", choices)
}

func (m *Model) openIssue(key string) tea.Cmd {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		m.notice = "Enter an issue key."
		return nil
	}
	for i, source := range m.visible {
		if m.issues[source].Key == key {
			m.standalone = false
			m.rows.SetCursor(i)
			m.detailFocused = true
			m.resize()
			return m.selectDetail()
		}
	}
	m.standalone = true
	m.resetComments()
	m.detailKey, m.detailIssue, m.detailErr = key, nil, nil
	m.detailLoading, m.detailFocused = true, true
	m.preview.GotoTop()
	m.resize()
	return m.tasks.Start(task.Spec{Scope: detailScope, Run: func() (any, error) { return m.detail(key) }})
}

func (m *Model) openViews() tea.Cmd {
	if m.options.LoadViews == nil {
		m.showViews(nil)
		return nil
	}
	m.notice = "Loading saved views…"
	request := m.interaction
	return m.tasks.Start(task.Spec{Scope: "views", Run: func() (any, error) {
		views, err := m.options.LoadViews()
		return viewsResult{request: request, views: views}, err
	}})
}

func (m *Model) showViews(views []SavedView) {
	m.views = views
	choices := []choice{
		{label: "My work", description: "Your unfinished issues", value: myWorkQuery},
		{label: "Updated", description: "Your issues updated in the last seven days", value: "assignee = currentUser() AND updated >= -7d ORDER BY updated DESC"},
		{label: "Reported", description: "Unfinished issues you reported", value: "reporter = currentUser() AND statusCategory != Done ORDER BY updated DESC"},
	}
	if m.options.ProjectQuery != "" {
		choices = append(choices, choice{label: "Project", description: "Unfinished project work", value: m.options.ProjectQuery})
	}
	for _, v := range views {
		choices = append(choices, choice{label: "Saved · " + cli.Text(v.Name), description: cli.Text(v.Description), value: v.Query})
	}
	m.pick("Saved views and lenses", "views", "", choices)
}

func (m *Model) acceptPicker(d *pickerDialog) tea.Cmd {
	value := d.value()
	if strings.HasPrefix(d.kind, "create-") {
		return m.acceptCreationPicker(d, value)
	}
	switch d.kind {
	case "edit-mode":
		if value == "description" {
			return m.loadDescription(d.target, "", nil)
		}
		return m.loadEdit(value, d.target)
	case "facet":
		if facet, ok := d.facets[value]; ok {
			m.facet = facet
			return m.applyLocalFilter()
		}
		return nil
	case "batch-history":
		for _, entry := range m.operations {
			if fmt.Sprint(entry.id) == value && entry.batch != nil {
				m.openBatchResult(entry.batch)
				break
			}
		}
		return nil
	case "bulk-mode", "bulk-assign-mode", "bulk-assign", "bulk-transition":
		return m.acceptBulkPicker(d, value)
	case "theme":
		m.setPreviewTheme(value)
		return nil
	case "issue":
		return m.openIssue(value)
	case "views":
		m.query, m.customQuery = value, value
		if entry, ok := d.menu.Selected(); ok {
			m.viewName = entry.Name
		}
		m.tabs.SetActive(len(m.tabs.Items()) - 1)
		return m.newQuery()
	default:
		return m.acceptActionPicker(d, value)
	}
}

func (m *Model) acceptText(d *textDialog) tea.Cmd {
	switch d.kind {
	case "save-view":
		if m.options.SaveView == nil {
			m.notice = "Saving views is unavailable."
			return nil
		}
		name, query := strings.TrimSpace(d.value()), d.target
		return m.tasks.Start(task.Spec{Scope: "save-view", Run: func() (any, error) { return name, m.options.SaveView(name, query) }})
	default:
		return m.acceptActionText(d)
	}
}

func (m *Model) navigationResult(msg task.FinishedMsg) tea.Cmd {
	if msg.Err != nil {
		m.notice = m.errorText(msg.Err)
		return nil
	}
	switch msg.Scope {
	case "views":
		result, ok := msg.Result.(viewsResult)
		if ok && result.request != m.interaction {
			m.notice = ""
			return nil
		}
		views := result.views
		if !ok {
			m.notice = "Could not load saved views."
			return nil
		}
		m.notice = ""
		m.showViews(views)
	case "save-view":
		m.notice = fmt.Sprintf("Saved view %s", cli.Text(fmt.Sprint(msg.Result)))
	}
	return nil
}
