package dashboard

import (
	"maps"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/carousel"
	"github.com/gechr/primer/list"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/config"
)

const settingsScope task.Scope = "settings"

type setting struct{ key, name, group, description string }

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
		tabs: carousel.New("All", "Appearance", "Issue list", "Issue preview", "Hierarchy", "Refresh", "Defaults", "Tools", "Connection", "Authentication", "Identity"),
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
	if _, boolean := m.preferences[key]; !boolean && key != "theme" {
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
		m.preferences[key] = config.Defaults()[key] == true
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
