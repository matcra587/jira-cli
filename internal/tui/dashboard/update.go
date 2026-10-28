package dashboard

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"
)

// Update routes input to one focus owner and commits only current fetches.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		m.resize()
		m.resizeSettings()
		m.resizeCreationEditor()
		return m, nil
	case detailDue:
		return m, m.startDetailRead(msg.key)
	case task.FinishedMsg:
		return m.acceptTask(msg)
	case tea.KeyPressMsg:
		m.interaction++
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "q" && m.localSaveBusy && !m.dialogs.Active() && m.settings == nil && !m.editing && !m.filtering {
			m.notice = "A saved-view publication is running; wait for its outcome before quitting."
			return m, nil
		}
		if msg.String() == "q" && m.actionBusy && !m.dialogs.Active() && !m.filtering && !m.editing && m.settings == nil && !m.help {
			m.notice = "A Jira action is running; wait for its outcome before quitting."
			return m, nil
		}
		if m.dialogs.Active() {
			return m, m.updateCommands(msg)
		}
		if m.creationEditor != nil {
			return m, m.updateCreationEditor(msg)
		}
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.filtering {
			return m, m.updateFilter(msg)
		}
		if m.editing {
			return m, m.updateQuery(msg)
		}
		if m.help {
			m.help = false
			return m, nil
		}
		return m, m.updateKey(msg)
	case tea.PasteMsg:
		if m.dialogs.Active() {
			return m, m.updateCommands(msg)
		}
		if m.creationEditor != nil {
			return m, m.updateCreationEditor(msg)
		}
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.filtering {
			return m, m.updateFilter(msg)
		}
		if m.editing {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case tea.MouseClickMsg:
		m.interaction++
		if m.dialogs.Active() {
			return m, m.updateCommands(msg)
		}
		if m.creationEditor != nil {
			return m, m.updateCreationEditor(msg)
		}
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		return m, m.clickIssue(msg)
	case tea.MouseWheelMsg:
		m.interaction++
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.dialogs.Active() {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.dialogs.ScrollBy(-3)
			case tea.MouseWheelDown:
				m.dialogs.ScrollBy(3)
			}
			cmd, _, _ := m.dialogs.Update(msg)
			return m, cmd
		}
		if m.creationEditor != nil {
			return m, m.updateCreationEditor(msg)
		}
		if m.detailFocused || m.previewContains(msg.X, msg.Y) || m.previewFocused && !m.listContains(msg.X, msg.Y) {
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(msg)
			return m, cmd
		}
		m.previewFocused = false
		before := m.selectedKey()
		switch msg.Button {
		case tea.MouseWheelUp:
			m.rows.MoveUp(3)
		case tea.MouseWheelDown:
			m.rows.MoveDown(3)
		}
		var detail tea.Cmd
		if before != m.selectedKey() {
			detail = m.selectDetail()
		}
		if msg.Button == tea.MouseWheelDown {
			return m, tea.Batch(detail, m.autoPage())
		}
		return m, detail
	}
	if m.creationEditor != nil && !m.dialogs.Active() {
		return m, m.updateCreationEditor(msg)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "n":
		return m.openCreation()
	case "e":
		m.openEditMenu()
		return nil
	case "l":
		return m.loadEdit("labels", m.activeIssueKey())
	case "O":
		return m.openBrowser()
	case "y":
		return m.copyIssue(false)
	case "Y":
		return m.copyIssue(true)
	case "ctrl+f":
		m.openFilter()
		return nil
	case "f":
		m.openFacets()
		return nil
	case "B":
		m.openBulkActions()
		return nil
	case "L":
		m.openOperations()
		return nil
	case "space":
		m.toggleMark()
		return nil
	case "ctrl+a":
		m.markAll()
		return nil
	case "X":
		m.invertMarks()
		return nil
	case "x":
		m.markRange()
		return nil
	case "[":
		return m.changeDetailSection(-1)
	case "]":
		return m.changeDetailSection(1)
	case "o":
		m.openLinkedIssues()
		return nil
	case "ctrl+o":
		m.openIssues()
		return nil
	case "v":
		return m.openViews()
	case "ctrl+s":
		m.text("Save current view", "save-view", m.query, "Choose a new saved-search name. Existing files will not be replaced.")
		return nil
	case "t":
		return m.openTransitions()
	case "a":
		m.openAssign()
		return nil
	case "c":
		m.openComment()
		return nil
	case "z":
		return m.toggleListZoom()
	case "ctrl+w":
		if m.split() {
			m.previewFocused = !m.previewFocused
		} else {
			m.detailFocused = !m.detailFocused
			m.resize()
			if !m.detailFocused && m.standalone {
				m.standalone = false
				return m.selectDetail()
			}
		}
		return nil
	case "q":
		return tea.Quit
	case ",":
		m.openSettings()
		return nil
	case "?":
		m.help = true
		return nil
	case "ctrl+p", "ctrl+k":
		m.openCommands()
		return nil
	case "H":
		return m.hierarchyHome()
	case "ctrl+g":
		return m.browseHierarchy(true)
	case "1":
		m.parkHierarchy()
		m.tabs.SetActive(0)
		return m.switchView()
	case "2":
		m.parkHierarchy()
		if m.options.ProjectQuery != "" {
			m.tabs.SetActive(1)
			return m.switchView()
		}
		return m.editQuery()
	case "/":
		return m.editQuery()
	case "tab", "shift+tab":
		m.parkHierarchy()
		if key == "tab" {
			m.tabs.Next()
		} else {
			m.tabs.Prev()
		}
		return m.switchView()
	case "r":
		if m.detailSection == 1 && (m.detailFocused || m.previewFocused) {
			return m.loadComments(false)
		}
		if m.loading {
			return nil
		}
		m.detailErr = nil
		return m.load(false)
	case "m":
		if m.detailSection == 1 && (m.detailFocused || m.previewFocused) {
			return m.loadComments(true)
		}
		return m.load(true)
	case "enter":
		if m.selected() != nil {
			m.detailFocused = true
			m.resize()
		}
		return nil
	case "esc", "h":
		if !m.detailFocused && !m.previewFocused && m.hierarchyActive() {
			return m.hierarchyBack()
		}
		m.detailFocused, m.previewFocused = false, false
		m.resize()
		if m.standalone {
			m.standalone = false
			return m.selectDetail()
		}
		return nil
	}
	if m.detailFocused || m.previewFocused {
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return cmd
	}
	before := m.selectedKey()
	switch key {
	case "right":
		return m.browseHierarchy(false)
	case "left":
		return m.hierarchyParent()
	case "backspace":
		return m.hierarchyBack()
	case "j", "down":
		m.rows.MoveDown(1)
	case "k", "up":
		m.rows.MoveUp(1)
	case "pgdown", "ctrl+d":
		m.rows.PageDown()
	case "pgup", "ctrl+u":
		m.rows.PageUp()
	case "g", "home":
		m.rows.Top()
	case "G", "end":
		m.rows.Bottom()
	}
	var detail tea.Cmd
	if before != m.selectedKey() {
		detail = m.selectDetail()
	}
	switch key {
	case "j", "down", "pgdown", "ctrl+d", "G", "end":
		return tea.Batch(detail, m.autoPage())
	default:
		return detail
	}
}

// acceptTask admits one current generation before dispatching to its feature.
func (m *Model) acceptTask(msg task.FinishedMsg) (tea.Model, tea.Cmd) {
	if !m.tasks.Accept(msg.Scope, msg.Gen) {
		return m, nil
	}
	switch msg.Scope {
	case "description-source":
		m.acceptDescriptionSource(msg)
	case "description-review":
		m.acceptDescriptionPrepared(msg)
	case "description-write":
		return m, m.acceptDescriptionFinished(msg)
	case "create-choices":
		return m, m.acceptCreationChoices(msg)
	case "create-review":
		m.acceptCreationPrepared(msg)
	case "create-write":
		return m, m.acceptCreationFinished(msg)
	case "edit-fields":
		m.acceptEditFields(msg)
	case "browse":
		m.acceptBrowse(msg)
	case "bulk-choices":
		m.acceptBulkChoices(msg)
	case "bulk-write":
		return m, m.acceptBulkFinished(msg)
	case "comments":
		m.acceptComments(msg)
	case "views", "save-view":
		return m, m.navigationResult(msg)
	case "action-choices", "write":
		return m, m.actionFinished(msg)
	case themeTaskScope:
		return m, m.acceptThemeTask(msg)
	case settingsScope:
		return m.acceptSettings(msg)
	case hierarchyTypesScope:
		return m, m.acceptHierarchyTypes(msg)
	case hierarchyScope:
		return m, m.acceptHierarchy(msg)
	case listScope:
		return m, m.acceptList(msg)
	case detailScope:
		m.acceptDetail(msg)
	}
	return m, nil
}
