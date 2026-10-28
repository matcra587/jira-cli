package dashboard

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/config"
)

type workspaceDialog struct {
	noticeRows int
	pickerDialog
	page                   *settingsPage
	working                config.Workspace
	mode, selected, notice string
	pending                config.WorkspaceSection
}

func (m *Model) openWorkspaceSettings() {
	working, err := config.ParseWorkspace(m.settings.values["tui_workspace"])
	if err != nil {
		m.settings.notice = m.errorText(err)
		return
	}
	d := &workspaceDialog{page: m.settings, working: working, mode: "root"}
	d.rebuild()
	m.dialogs.Push(d)
}

func (d *workspaceDialog) titleFor(id string) string {
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
	for _, section := range d.working.Sections {
		if section.ID == id {
			return section.Title
		}
	}
	return id
}

func (d *workspaceDialog) rebuild() {
	title := "Workspace"
	choices := []choice{}
	switch d.mode {
	case "root":
		choices = []choice{{label: "Views", description: "Create and edit named Jira queries", value: "views"}, {label: "Tabs", description: "Choose visible views and their order", value: "tabs"}, {label: "Lenses", description: "Named conditions that narrow a view", value: "lenses"}, {label: "Startup view", description: "Choose the first view on the next launch", value: "start-view"}, {label: "Startup lens", description: "Choose the initial narrowing lens", value: "start-lens"}, {label: "Reset workspace", description: "Keep built-in defaults in the settings draft", value: "reset"}}
		choices = append(choices, choice{label: "Keybindings", description: "Capture shortcuts, add alternatives and reset defaults", value: "keys"})
	case "keys":
		title = "Keybindings"
		choices = bindingChoices(d.working)
	case "key":
		title = "Edit shortcut"
		for _, action := range config.WorkspaceActions() {
			if action.ID == d.selected {
				title = action.Title
			}
		}
		current := ""
		for _, row := range bindingChoices(d.working) {
			if row.value == d.selected {
				current = row.description
			}
		}
		choices = []choice{{label: "Replace shortcut", description: current + " · current", value: "replace"}, {label: "Add alternative", description: "Keep existing shortcuts and capture another", value: "append"}, {label: "Reset default", description: "Remove this action's override from the draft", value: "reset"}}
	case "views", "lenses":
		rows := d.working.Sections
		title = "Views"
		add := "Add view"
		if d.mode == "lenses" {
			rows = d.working.Lenses
			title = "Lenses"
			add = "Add lens"
		}
		choices = append(choices, choice{label: add, description: "Title, then authored JQL", value: ":add"})
		for _, row := range rows {
			choices = append(choices, choice{label: row.Title, description: row.JQL, value: row.ID})
		}
	case "view", "lens":
		title = "Edit view"
		if d.mode == "lens" {
			title = "Edit lens"
		}
		choices = []choice{{label: "Rename", description: "Keep the stable ID and references", value: "title"}, {label: "Edit JQL", description: "Keep the authored query in the draft", value: "jql"}, {label: "Remove", description: "Remove this definition and its references from the draft", value: "remove"}}
		if d.mode == "view" {
			label := "Show as tab"
			if slices.Contains(d.working.TabIDs(), d.selected) {
				label = "Hide tab"
			}
			choices = append(choices, choice{label: label, value: "toggle-tab"})
		}
	case "tabs":
		title = "Visible tabs"
		for i, id := range d.working.TabIDs() {
			choices = append(choices, choice{label: fmt.Sprintf("%d · %s", i+1, d.titleFor(id)), value: id})
		}
		choices = append(choices, choice{label: "Add tab", description: "Show another built-in or custom view", value: ":add"})
	case "tab":
		title = d.titleFor(d.selected)
		choices = []choice{{label: "Move earlier", value: "earlier"}, {label: "Move later", value: "later"}, {label: "Hide tab", value: "hide"}}
	case "add-tab":
		title = "Add tab"
		ids := config.BuiltinWorkspaceViews()
		for _, row := range d.working.Sections {
			ids = append(ids, row.ID)
		}
		for _, id := range ids {
			if !slices.Contains(d.working.TabIDs(), id) {
				choices = append(choices, choice{label: d.titleFor(id), value: id})
			}
		}
	case "start-view":
		title = "Startup view"
		choices = append(choices, choice{label: "First visible tab", value: ""})
		for _, id := range d.working.TabIDs() {
			description := ""
			if d.working.LandingView == id {
				description = "Current startup view"
			}
			choices = append(choices, choice{label: d.titleFor(id), description: description, value: id})
		}
	case "start-lens":
		title = "Startup lens"
		choices = append(choices, choice{label: "No lens", value: ""})
		for _, row := range d.working.Lenses {
			description := row.JQL
			if row.ID == d.working.LandingLens {
				description = "Current startup lens · " + description
			}
			choices = append(choices, choice{label: row.Title, description: description, value: row.ID})
		}
	}
	d.pickerDialog = *newPicker(title, "workspace-settings", "", choices)
	switch d.mode {
	case "start-view":
		d.selectValue(d.working.LandingView)
	case "start-lens":
		d.selectValue(d.working.LandingLens)
	case "views", "lenses", "tabs", "keys":
		d.selectValue(d.selected)
	}
}

func (d *workspaceDialog) Content(width int) string {
	notice := d.notice
	if notice == "" {
		notice = "Changes stay in Settings until Ctrl+S saves."
	}
	lines := layout.WrapLines(notice, width)
	d.noticeRows = len(lines) + 1
	return strings.Join(lines, "\n") + "\n\n" + d.pickerDialog.Content(width)
}

func (d *workspaceDialog) ScrollTo() (int, int, bool) {
	line, margin, ok := d.pickerDialog.ScrollTo()
	return line + d.noticeRows, margin, ok
}

func (d *workspaceDialog) Footer() string {
	return ansi.WcWidth.Truncate(d.Title(), 12, "…") + " · enter · esc"
}

func (d *workspaceDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "esc" && d.mode != "root" {
		switch d.mode {
		case "key":
			d.mode = "keys"
		case "view":
			d.mode = "views"
		case "lens":
			d.mode = "lenses"
		case "tab", "add-tab":
			d.mode = "tabs"
		default:
			d.mode = "root"
		}
		d.rebuild()
		return d, nil, dialog.ResultNone
	}
	_, cmd, result := d.pickerDialog.Update(msg)
	return d, cmd, result
}

func (m *Model) keepWorkspace(d *workspaceDialog, w config.Workspace) bool {
	if d.page != m.settings {
		return false
	}
	if err := w.Validate(); err != nil {
		d.notice = m.errorText(err)
		return false
	}
	if !m.keepSetting("tui_workspace", w.JSON()) {
		d.notice = m.settings.notice
		return false
	}
	d.working = w
	d.notice = "Kept in the settings draft. Ctrl+S saves after leaving this menu."
	return true
}

func (m *Model) acceptWorkspaceMenu(d *workspaceDialog) tea.Cmd {
	if d.page != m.settings {
		return nil
	}
	value := d.value()
	w := d.working.Clone()
	mode := d.mode
	m.dialogs.Push(d)
	switch mode {
	case "keys":
		d.selected = value
		d.mode = "key"
	case "key":
		if value == "reset" {
			m.keepWorkspace(d, resetWorkspaceBinding(w, d.selected))
		} else {
			m.dialogs.Push(&bindingCapture{owner: d, action: d.selected, appendKey: value == "append"})
			return nil
		}
	case "root":
		if value == "reset" {
			m.keepWorkspace(d, config.Workspace{})
		} else {
			d.mode = value
		}
	case "views", "lenses":
		if value == ":add" {
			prefix := "view"
			rows := w.Sections
			if mode == "lenses" {
				prefix = "lens"
				rows = w.Lenses
			}
			id := ""
			for n := 1; id == ""; n++ {
				candidate := fmt.Sprintf("%s-%d", prefix, n)
				if !slices.ContainsFunc(rows, func(row config.WorkspaceSection) bool { return row.ID == candidate }) {
					id = candidate
				}
			}
			d.pending = config.WorkspaceSection{ID: id}
			m.openWorkspaceField(d, "new-title", id, "")
			return nil
		}
		d.selected = value
		d.mode = "view"
		if mode == "lenses" {
			d.mode = "lens"
		}
	case "view", "lens":
		rows := w.Sections
		if mode == "lens" {
			rows = w.Lenses
		}
		i := slices.IndexFunc(rows, func(row config.WorkspaceSection) bool { return row.ID == d.selected })
		if i < 0 {
			d.mode = "root"
			break
		}
		if value == "title" || value == "jql" {
			text := rows[i].Title
			if value == "jql" {
				text = rows[i].JQL
			}
			m.openWorkspaceField(d, value, d.selected, text)
			return nil
		}
		switch value {
		case "remove":
			if mode == "lens" {
				w.Lenses = slices.Delete(w.Lenses, i, i+1)
				if w.LandingLens == d.selected {
					w.LandingLens = ""
				}
				d.mode = "lenses"
			} else {
				ids := w.TabIDs()
				w.Sections = slices.Delete(w.Sections, i, i+1)
				w.Tabs = slices.DeleteFunc(ids, func(id string) bool { return id == d.selected })
				if len(w.Tabs) == 0 {
					w.Tabs = []string{"my-work"}
				}
				if w.LandingView == d.selected {
					w.LandingView = ""
				}
				d.mode = "views"
			}
		case "toggle-tab":
			w.Tabs = toggleWorkspaceTab(w, d.selected)
		}
		m.keepWorkspace(d, w)
	case "tabs":
		d.mode = "tab"
		d.selected = value
		if value == ":add" {
			d.mode = "add-tab"
		}
	case "tab":
		w.Tabs = w.TabIDs()
		i := slices.Index(w.Tabs, d.selected)
		if i < 0 {
			d.mode = "tabs"
			break
		}
		switch value {
		case "earlier":
			if i > 0 {
				w.Tabs[i], w.Tabs[i-1] = w.Tabs[i-1], w.Tabs[i]
			}
		case "later":
			if i+1 < len(w.Tabs) {
				w.Tabs[i], w.Tabs[i+1] = w.Tabs[i+1], w.Tabs[i]
			}
		case "hide":
			w.Tabs = slices.Delete(w.Tabs, i, i+1)
			if w.LandingView == d.selected {
				w.LandingView = ""
			}
			d.mode = "tabs"
		}
		m.keepWorkspace(d, w)
	case "add-tab":
		if value != "" {
			w.Tabs = append(w.TabIDs(), value)
			if m.keepWorkspace(d, w) {
				d.mode = "tabs"
			}
		}
	case "start-view":
		w.LandingView = value
		m.keepWorkspace(d, w)
	case "start-lens":
		w.LandingLens = value
		m.keepWorkspace(d, w)
	}
	d.rebuild()
	return nil
}

func toggleWorkspaceTab(w config.Workspace, id string) []string {
	ids := w.TabIDs()
	if slices.Contains(ids, id) {
		return slices.DeleteFunc(ids, func(value string) bool { return value == id })
	}
	return append(ids, id)
}

type workspaceFieldDialog struct {
	textDialog
	owner             *workspaceDialog
	field, id, notice string
}

func (d *workspaceFieldDialog) Footer() string {
	if d.area != nil {
		return "ctrl+s keep in draft · esc cancel"
	}
	if d.field != "new-title" {
		return "enter keep in draft · esc cancel"
	}
	return "enter next · esc cancel"
}

func (d *workspaceFieldDialog) Content(width int) string {
	d.description = d.notice
	if d.description == "" {
		d.description = "Edit this workspace definition. Jira is not changed."
	}
	return d.textDialog.Content(width)
}

func (d *workspaceFieldDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.textDialog.Update(msg)
	return d, cmd, result
}

func (m *Model) openWorkspaceField(owner *workspaceDialog, field, id, value string) {
	d := &workspaceFieldDialog{owner: owner, field: field, id: id, textDialog: textDialog{title: "View title", line: input.NewLine("› ", "Name this view")}}
	if owner.mode == "lenses" || owner.mode == "lens" {
		d.title = "Lens title"
	}
	d.line.SetValue(value)
	if field == "jql" || field == "new-jql" {
		area := textarea.New()
		area.SetVirtualCursor(false)
		area.CharLimit = 8192
		area.SetValue(value)
		area.Focus()
		d.area = &area
		d.title = "View JQL"
		if owner.mode == "lenses" || owner.mode == "lens" {
			d.title = "Lens conditions"
		}
	}
	m.dialogs.Push(d)
}

func (m *Model) acceptWorkspaceField(d *workspaceFieldDialog) tea.Cmd {
	owner := d.owner
	if owner.page != m.settings || m.dialogs.Top() != owner {
		return nil
	}
	value := d.value()
	w := owner.working.Clone()
	if d.field == "new-title" {
		owner.pending.Title = strings.TrimSpace(value)
		m.openWorkspaceField(owner, "new-jql", d.id, "")
		return nil
	}
	if d.field == "new-jql" {
		row := owner.pending
		row.JQL = value
		if owner.mode == "lenses" {
			w.Lenses = append(w.Lenses, row)
		} else {
			ids := w.TabIDs()
			w.Sections = append(w.Sections, row)
			w.Tabs = append(ids, row.ID)
		}
	} else {
		rows := w.Sections
		if owner.mode == "lens" {
			rows = w.Lenses
		}
		for i := range rows {
			if rows[i].ID == d.id {
				if d.field == "title" {
					rows[i].Title = strings.TrimSpace(value)
				} else {
					rows[i].JQL = value
				}
			}
		}
	}
	if !m.keepWorkspace(owner, w) {
		d.notice = owner.notice
		m.dialogs.Push(d)
		return nil
	}
	owner.rebuild()
	return nil
}
