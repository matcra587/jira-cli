package dashboard

import (
	"maps"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/carousel"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/list"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

const settingsScope task.Scope = "settings"

type setting struct{ key, name, group, description string }

var dashboardSettings = []setting{
	{key: "tui_preview", name: "Side-by-side preview", group: "Appearance", description: "Show issue details beside the list when the terminal has room. Enter always opens details."},
	{key: "tui_status", name: "Show status", group: "Issue list", description: "Display the status column when the issue list has enough space."},
	{key: "tui_assignee", name: "Show assignee", group: "Issue list", description: "Display assignees in wide lists. My work omits this column because every issue is yours."},
	{key: "tui_labels", name: "Show labels", group: "Issue preview", description: "Include issue labels above the description."},
	{key: "tui_priority", name: "Show priority", group: "Issue preview", description: "Include priority above the description."},
	{key: "tui_issue_type", name: "Show issue type", group: "Issue preview", description: "Include the issue type above the description."},
	{key: "tui_compact", name: "Compact issue rows", group: "Appearance", description: "Use one line per issue. Turn off for more breathing room between rows."},
	{key: "tui_updated", name: "Show updated", group: "Issue list", description: "Display the native update timestamp when the list has enough room."},
	{key: "theme", name: "Theme", group: "Appearance", description: "Choose built-in or custom themes with live previews. Enter keeps the selection in your settings draft; Esc restores the prior colors."},
}

type settingsPage struct {
	tabs              *carousel.Model
	rows              *list.Model
	input             textinput.Model
	visible           []setting
	baseline          map[string]bool
	values, saved     map[string]string
	editor            *settingInput
	themeLabel        string
	baselineTheme     string
	restoreTheme      func()
	themeDirty        bool
	sources           map[string]string
	searching, saving bool
	preview           bool
	notice            string
}

func (m *Model) enabled(key string) bool { return m.preferences[key] }

func (m *Model) openSettings() {
	input := textinput.New()
	input.Prompt, input.Placeholder, input.CharLimit = "⌕ ", "Search settings…", 128
	m.settings = &settingsPage{
		tabs: carousel.New("All", "Appearance", "Issue list", "Issue preview", "Defaults", "Tools", "Connection", "Authentication", "Identity"),
		rows: list.New(), input: input, baseline: maps.Clone(m.preferences), baselineTheme: m.themeName, sources: maps.Clone(m.options.SettingSources),
	}
	m.settings.values = publicSettingValues(m.options.SettingValues)
	m.settings.themeLabel = m.options.ThemeLabel
	m.settings.notice = m.options.ThemeNotice
	m.settings.saved = maps.Clone(m.settings.values)
	m.settings.rows.Styles = m.rows.Styles
	m.settings.rows.Styles.Cursor = m.settings.rows.Styles.Cursor.Transform(func(row string) string {
		return "› " + strings.TrimPrefix(row, "  ")
	})
	m.settings.tabs.ActiveStyle, m.settings.tabs.InactiveStyle = m.tabs.ActiveStyle, m.tabs.InactiveStyle
	if m.settings.sources == nil {
		m.settings.sources = make(map[string]string)
	}
	m.resizeSettings()
}

func (m *Model) resizeSettings() {
	if m.settings == nil {
		return
	}
	p := m.settings
	p.input.SetWidth(max(1, m.pageWidth()-4))
	p.rows.SetSize(m.pageWidth(), max(1, m.height-11))
	m.rebuildSettings()
}

func (m *Model) rebuildSettings() {
	p := m.settings
	selected := ""
	if i, ok := p.rows.Selected(); ok && i < len(p.visible) {
		selected = p.visible[i].key
	}
	p.visible = nil
	rows := []string{}
	term := filter.Parse(p.input.Value())
	// Fixed cell budgets keep toggles, scalar values and provenance in columns
	// even when labels are clipped or a value contains wide Unicode characters.
	const sourceWidth = 7
	budget := max(1, m.pageWidth()-sourceWidth-7) // gaps, cursor and scrollbar
	nameWidth := max(12, min(32, budget/2))
	valueWidth := max(1, min(24, budget-nameWidth))
	for _, s := range allDashboardSettings() {
		if p.tabs.ActiveItem() != "All" && s.group != p.tabs.ActiveItem() {
			continue
		}
		if !term.Match(s.key + " " + s.name + " " + s.group + " " + s.description) {
			continue
		}
		p.visible = append(p.visible, s)
		value := strconv.FormatBool(m.enabled(s.key))
		if !strings.HasPrefix(s.key, "tui_") {
			value = p.values[s.key]
		}
		value = config.Redacted(s.key, value)
		if s.key == "api_token" || s.key == "aliases" {
			value = "managed separately"
		}
		if s.key == "theme" {
			value = m.themeName
			if p.values["theme_path"] != "" {
				value = p.themeLabel
				if value == "" {
					value = "Custom theme"
				}
			}
			if value == "" {
				value = "auto"
			}
		}
		source := p.sources[s.key]
		if m.preferences[s.key] != p.baseline[s.key] || s.key == "theme" && p.themeDirty || p.values[s.key] != p.saved[s.key] {
			source = "unsaved"
		}
		if source == "" {
			source = "default"
		}
		switch s.key {
		case "api_token":
			source = "auth"
		case "aliases":
			source = "alias"
		}
		if choices := config.SettingValueChoices(s.key); len(choices) == 2 && choices[0] == "true" && choices[1] == "false" {
			if value == "true" {
				value = accent("[x] On")
			} else {
				value = dim("[ ] Off")
			}
		} else {
			value = cli.Text(value)
			if value == "" {
				value = dim("—")
			}
		}
		rows = append(rows, "  "+layout.NormalizeLine(s.name, nameWidth)+"  "+layout.NormalizeLine(value, valueWidth)+"  "+dim(layout.NormalizeLine(source, sourceWidth)))
	}
	p.rows.SetRows(rows)
	for i, s := range p.visible {
		if s.key == selected {
			p.rows.SetCursor(i)
			break
		}
	}
}

func (m *Model) settingsChanged() bool {
	return !maps.Equal(m.preferences, m.settings.baseline) || m.settings.themeDirty || !maps.Equal(m.settings.values, m.settings.saved)
}

func (m *Model) previewSettings() {
	m.resize()
	m.rebuildSettings()
}

func (m *Model) updateSettings(msg tea.Msg) tea.Cmd {
	p := m.settings
	if p.saving {
		return nil
	}
	if p.editor != nil {
		return m.updateSettingEditor(msg)
	}
	if p.preview {
		if press, ok := msg.(tea.KeyPressMsg); ok && (press.String() == "esc" || press.String() == "p") {
			p.preview = false
		}
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheel.Button {
		case tea.MouseWheelUp:
			p.rows.MoveUp(3)
		case tea.MouseWheelDown:
			p.rows.MoveDown(3)
		}
		return nil
	}
	if p.searching {
		return m.updateSettingsSearch(msg)
	}
	if press, ok := msg.(tea.KeyPressMsg); ok {
		return m.updateSettingsKey(press.String())
	}
	return nil
}

func (m *Model) updateSettingsSearch(msg tea.Msg) tea.Cmd {
	p := m.settings
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "esc", "enter", "down":
			p.searching = false
			p.input.Blur()
			return nil
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.rows.Top()
	m.rebuildSettings()
	return cmd
}

func (m *Model) updateSettingsKey(k string) tea.Cmd {
	p := m.settings
	switch k {
	case "esc":
		m.preferences = maps.Clone(p.baseline)
		m.themeName = p.baselineTheme
		if p.restoreTheme != nil {
			p.restoreTheme()
		}
		m.applyStyles()
		m.dialogs = newCommandStack()
		m.settings = nil
		m.resize()
		return nil
	case "p":
		p.preview = true
		return nil
	case "/":
		p.searching = true
		return p.input.Focus()
	case "tab", "shift+tab":
		if k == "tab" {
			p.tabs.Next()
		} else {
			p.tabs.Prev()
		}
		p.rows.Top()
		m.rebuildSettings()
		return nil
	case "up", "k":
		p.rows.MoveUp(1)
		return nil
	case "down", "j":
		p.rows.MoveDown(1)
		return nil
	case "pgup":
		p.rows.PageUp()
		return nil
	case "pgdown":
		p.rows.PageDown()
		return nil
	case "enter", "space", "d":
		return m.activateSelectedSetting(k == "d")
	case "ctrl+s":
		return m.saveSettings()
	case "ctrl+r":
		return m.reloadSettings()
	}
	return nil
}

func (m *Model) activateSelectedSetting(reset bool) tea.Cmd {
	p := m.settings
	i, ok := p.rows.Selected()
	if !ok {
		return nil
	}
	key := p.visible[i].key
	if p.sources[key] == "env" {
		p.notice = "Controlled by the environment; remove its JIRA_ override to edit here."
		return nil
	}
	if p.sources[key] == "flag" {
		p.notice = "Controlled by an invocation flag; remove that override to edit here."
		return nil
	}
	p.notice = ""
	if !strings.HasPrefix(key, "tui_") && key != "theme" {
		m.editSetting(p.visible[i], reset)
		return nil
	}
	if key == "theme" {
		if reset {
			m.setPreviewTheme("auto")
		} else {
			return m.pickTheme()
		}
		return nil
	}
	if reset {
		m.preferences[key] = true
	} else {
		m.preferences[key] = !m.preferences[key]
	}
	m.previewSettings()
	return nil
}

func (m *Model) saveSettings() tea.Cmd {
	p := m.settings
	if m.actionBusy || m.localSaveBusy {
		p.notice = "Wait for the running write before saving or reloading."
		return nil
	}
	if !m.settingsChanged() {
		p.notice = "No unsaved changes."
		return nil
	}
	if m.options.SaveSettings == nil {
		p.notice = "Saving is unavailable in this session."
		return nil
	}
	values := make(map[string]string)
	for key, value := range p.values {
		if value != p.saved[key] {
			values[key] = value
		}
	}
	for key, value := range m.preferences {
		if value != p.baseline[key] {
			values[key] = strconv.FormatBool(value)
		}
	}
	if p.themeDirty {
		values["theme"] = m.themeName
		values["theme_path"] = p.values["theme_path"]
	}
	p.saving, p.notice = true, "Saving…"
	save, reload := m.options.SaveSettings, m.options.ReloadSettings
	return m.tasks.Start(task.Spec{Scope: settingsScope, Run: func() (any, error) {
		if err := save(values); err != nil {
			return nil, err
		}
		result := settingsSaved{published: true}
		if reload != nil {
			result = loadSettingsRuntime(reload)
			result.published = true
		}
		return result, nil
	}})
}

func (m *Model) reloadSettings() tea.Cmd {
	p := m.settings
	if m.actionBusy || m.localSaveBusy || m.options.ReloadSettings == nil {
		p.notice = "Reload is unavailable while a write is running or no runtime factory is configured."
		return nil
	}
	if m.settingsChanged() {
		p.notice = "Save or cancel the settings draft before reloading."
		return nil
	}
	p.saving, p.notice = true, "Reloading…"
	reload := m.options.ReloadSettings
	return m.tasks.Start(task.Spec{Scope: settingsScope, Run: func() (any, error) {
		return loadSettingsRuntime(reload), nil
	}})
}

type settingsSaved struct {
	published bool
	next      *Model
	reloadErr error
}

func (m *Model) acceptSettings(msg task.FinishedMsg) (tea.Model, tea.Cmd) {
	p := m.settings
	if p == nil {
		return m, nil
	}
	p.saving = false
	if msg.Err != nil {
		p.notice = "Could not save; changes remain unsaved. Inspect the config before retrying. " + m.errorText(msg.Err)
		return m, nil
	}
	result, hasResult := msg.Result.(settingsSaved)
	if hasResult && result.next != nil && result.reloadErr == nil {
		next, cmd := m.adoptRuntime(result.next)
		if result.published {
			result.next.settings.notice = "Saved and active. Runtime reloaded."
		}
		return next, cmd
	}
	for key, value := range m.preferences {
		if value != p.baseline[key] {
			p.sources[key] = "file"
		}
	}
	restart := false
	for key, value := range p.values {
		if value != p.saved[key] {
			p.sources[key] = "file"
			restart = true
		}
	}
	m.options.SettingValues = maps.Clone(p.values)
	p.saved = maps.Clone(p.values)
	if p.themeDirty {
		p.sources["theme"] = "file"
	}
	p.baselineTheme = m.themeName
	m.options.ThemeLabel = p.themeLabel
	p.restoreTheme = nil
	p.themeDirty = false
	m.options.SettingSources = maps.Clone(p.sources)
	p.baseline, p.notice = maps.Clone(m.preferences), "Settings saved."
	if restart {
		p.notice += " Restart the TUI to apply startup settings; this session keeps its connection and tools."
	}
	if hasResult && result.reloadErr != nil {
		m.reloadFailure = m.errorText(result.reloadErr)
		m.options.Actions.ReadOnly = true
		p.notice = "Saved config is not active; old connection retained and Jira writes blocked. Ctrl+R retries reload. " + m.reloadFailure
		if !result.published {
			p.notice = "Reload failed; old connection retained and Jira writes blocked. Ctrl+R retries. " + m.reloadFailure
		}
	}
	m.rebuildSettings()
	return m, nil
}

func (m *Model) settingsView() string {
	p := m.settings
	title := accent("JIRA") + "  " + "Settings"
	if m.settingsChanged() {
		title += dim("  · " + m.settingsStatus())
	}
	details := m.settingsDetails()
	if m.reloadFailure != "" {
		active := "Config not active; Jira writes blocked. Active connection: " + cli.Text(m.site) + " · " + cli.Text(m.options.RuntimeScope.Account)
		details = append(layout.WrapLines(active, m.pageWidth()), details...)
	}
	detailHeight := max(2, min(len(details), max(2, m.height/3), max(2, m.height-10)))
	bodyHeight := max(1, m.height-9-detailHeight)
	p.rows.SetSize(m.pageWidth(), bodyHeight)
	rows := p.rows.View()
	if len(p.visible) == 0 {
		rows = dim("  No settings match your search.")
	}
	hints := []key.Hint{{Key: "ctrl+s", Desc: "save"}, {Key: "esc", Desc: "back"}, {Key: "enter", Desc: "change"}, {Key: "p", Desc: "preview"}, {Key: "/", Desc: "search"}}
	if m.options.ReloadSettings != nil {
		hints = append(hints, key.Hint{Key: "ctrl+r", Desc: "reload"})
	}
	if m.pageWidth() >= 65 {
		hints = append(hints, key.Hint{Key: "↑↓", Desc: "select"}, key.Hint{Key: "tab", Desc: "section"}, key.Hint{Key: "d", Desc: "default"})
	}
	if p.searching {
		hints = []key.Hint{{Key: "enter/↓", Desc: "results"}, {Key: "esc", Desc: "finish search"}}
	}
	if p.editor != nil {
		p.editor.line.SetWidth(max(1, m.pageWidth()-4))
		rows = p.editor.line.View()
		if m.height >= 15 {
			rows = "  " + p.editor.setting.name + "\n\n" + rows
		}
		hints = []key.Hint{{Key: "enter", Desc: "keep in draft"}, {Key: "esc", Desc: "cancel edit"}}
	}
	prefix := ""
	footer := (key.Renderer{Prefix: &prefix, Width: m.pageWidth(), Gap: " · "}).Render(hints)
	if p.saving {
		footer = p.notice
	}
	body := frame(rows, m.pageWidth(), bodyHeight)
	notice := p.notice
	if notice == "" {
		notice = m.settingsStatus()
	}
	tabs := p.tabs.View()
	if xansi.WcWidth.StringWidth(tabs) > m.pageWidth() {
		tabs = "‹ " + p.tabs.ActiveStyle.Render(p.tabs.ActiveItem()) + " ›"
	}
	return frame(strings.Join([]string{
		title, tabs, "", p.input.View(), "", body, "",
		frame(strings.Join(details, "\n"), m.pageWidth(), detailHeight), frame(notice, m.pageWidth(), 1), frame(dim(footer), m.pageWidth(), 2),
	}, "\n"), m.pageWidth(), m.height)
}

func (m *Model) pickTheme() tea.Cmd {
	m.pick("Choose a theme", "theme", "", nil)
	p := m.dialogs.Top().(*pickerDialog)
	p.manageThemes = m.options.WriteTheme != nil
	p.initialThemes = true
	m.fillThemePicker(p, nil, m.themeName)
	return m.loadThemePicker(p)
}

// Preset resets leave the cached custom label intact; accepting a picker
// selection owns replacing that label alongside its selected file path.
func (m *Model) setPreviewTheme(name string) {
	m.applyThemeSelection(themeSelection{
		name: m.settings.themeLabel,
		base: name,
	})
}

func (m *Model) refreshThemeStyles() {
	m.applyStyles()
	m.dialogs.SetFrame(commandFrame())
	m.settings.tabs.ActiveStyle, m.settings.tabs.InactiveStyle = m.tabs.ActiveStyle, m.tabs.InactiveStyle
	m.settings.rows.Styles.Dim = m.rows.Styles.Dim
	m.previewSettings()
}

func (m *Model) previewHighlightedTheme(p *pickerDialog) {
	if _, ok := p.menu.Selected(); !ok {
		return
	}
	name := p.value()
	if name == p.previewedTheme {
		return
	}
	selection, ok := p.themes[name]
	if !ok {
		return
	}
	restore, err := previewThemeSelection(selection)
	if err != nil {
		p.notice = m.errorText(err)
		return
	}
	p.notice = ""
	if p.restorePreview == nil {
		p.restorePreview = restore
	}
	p.previewedTheme = name
	m.refreshThemeStyles()
	p.restyle()
}
