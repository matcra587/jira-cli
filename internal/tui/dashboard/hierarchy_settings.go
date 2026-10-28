package dashboard

import (
	"cmp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

const hierarchyTypesScope task.Scope = "hierarchy-types"

type hierarchyTypesResult struct {
	page   *settingsPage
	editor *settingInput
	value  string
	types  []jira.IssueTypeDetails
}

type hierarchyTypeMenu struct {
	settingMenu
	editor *settingInput
}

func (d *hierarchyTypeMenu) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.settingMenu.Update(msg)
	return d, cmd, result
}

func (m *Model) loadHierarchyTypes() tea.Cmd {
	page := m.settings
	if page == nil || page.editor == nil || page.editor.setting.key != "tui_hierarchy_query" {
		return nil
	}
	read := m.options.Actions.HierarchyTypes
	if read == nil {
		page.notice = "Issue types are unavailable; edit the roots JQL directly."
		return nil
	}
	editor, value := page.editor, page.editor.line.Value()
	page.notice = "Reading visible Jira issue types…"
	return m.tasks.Start(task.Spec{Scope: hierarchyTypesScope, Run: func() (any, error) {
		types, err := read()
		return hierarchyTypesResult{page: page, editor: editor, value: value, types: types}, err
	}})
}

func (m *Model) acceptHierarchyTypes(msg task.FinishedMsg) tea.Cmd {
	result, ok := msg.Result.(hierarchyTypesResult)
	if !ok || result.page != m.settings || m.settings == nil ||
		m.settings.editor != result.editor || result.editor.line.Value() != result.value || m.dialogs.Active() {
		return nil
	}
	if msg.Err != nil {
		m.settings.notice = "Issue types unavailable: " + m.errorText(msg.Err)
		return nil
	}
	choices, err := hierarchyTypeChoices(result.types)
	if err != nil {
		m.settings.notice = m.errorText(err)
		return nil
	}
	if len(choices) == 0 {
		m.settings.notice = "Jira returned no visible issue types. Edit the roots JQL directly."
		return nil
	}
	m.settings.notice = "Choose a root type; its native ID becomes the roots query."
	m.pick("Hierarchy root type", "hierarchy-root-type", "tui_hierarchy_query", choices)
	picker := m.dialogs.Pop().(*pickerDialog)
	m.dialogs.Push(&hierarchyTypeMenu{
		settingMenu: settingMenu{pickerDialog: *picker, page: result.page}, editor: result.editor,
	})
	return nil
}

func hierarchyTypeChoices(types []jira.IssueTypeDetails) ([]choice, error) {
	choices := make([]choice, 0, len(types))
	seen := make(map[string]bool, len(types))
	for _, native := range types {
		id, name := native.ID, native.Name
		validID := id.Set && !id.Null && strings.TrimSpace(id.Value) != ""
		validName := name.Set && !name.Null && strings.TrimSpace(name.Value) != ""
		if !validID || !validName || seen[id.Value] {
			return nil, core.ErrIssueMetadataInvalid
		}
		literal, err := command.QuoteJQL(id.Value)
		if err != nil {
			return nil, core.ErrIssueMetadataInvalid
		}
		seen[id.Value] = true
		choices = append(choices, choice{
			label: cli.Text(name.Value), description: "Native type ID " + cli.Text(id.Value),
			value: "issuetype = " + literal + " ORDER BY updated DESC",
		})
	}
	slices.SortStableFunc(choices, func(a, b choice) int { return cmp.Compare(a.label, b.label) })
	return choices, nil
}
