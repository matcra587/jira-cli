package dashboard

import (
	"maps"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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
	{key: "theme", name: "Theme", group: "Appearance", description: "Choose a palette, preview the issue page with p, then save or cancel. Selecting a preset clears the saved custom palette path."},
}

type settingsPage struct {
	tabs              *carousel.Model
	rows              *list.Model
	input             textinput.Model
	visible           []setting
	baseline          map[string]bool
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
		tabs: carousel.New("All", "Appearance", "Issue list", "Issue preview"),
		rows: list.New(), input: input, baseline: maps.Clone(m.preferences), baselineTheme: m.themeName, sources: maps.Clone(m.options.SettingSources),
	}
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
	nameWidth := max(10, min(48, m.pageWidth()/2))
	for _, s := range dashboardSettings {
		if p.tabs.ActiveItem() != "All" && s.group != p.tabs.ActiveItem() {
			continue
		}
		if !term.Match(s.name + " " + s.group + " " + s.description) {
			continue
		}
		p.visible = append(p.visible, s)
		value := strconv.FormatBool(m.enabled(s.key))
		if s.key == "theme" {
			value = m.themeName
			if value == "" {
				value = "auto"
			}
		}
		source := p.sources[s.key]
		if m.preferences[s.key] != p.baseline[s.key] || s.key == "theme" && p.themeDirty {
			source = "unsaved"
		}
		if source == "" {
			source = "default"
		}
		rows = append(rows, "  "+layout.NormalizeLine(s.name, nameWidth)+"  "+value+"  "+dim(source))
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
	return !maps.Equal(m.preferences, m.settings.baseline) || m.settings.themeDirty
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
	if p.preview {
		if press, ok := msg.(tea.KeyPressMsg); ok && (press.String() == "esc" || press.String() == "p") {
			p.preview = false
		}
		return nil
	}
	if press, ok := msg.(tea.KeyPressMsg); ok {
		k := press.String()
		if p.searching {
			switch k {
			case "esc", "enter", "down":
				p.searching = false
				p.input.Blur()
				return nil
			}
		} else {
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
				if i, ok := p.rows.Selected(); ok {
					key := p.visible[i].key
					if p.sources[key] == "env" {
						p.notice = "Controlled by the environment; remove its JIRA_ override to edit here."
						return nil
					}
					p.notice = ""
					if key == "theme" {
						if k == "d" {
							m.setPreviewTheme("auto")
						} else {
							m.pickTheme()
						}
						return nil
					}
					if k == "d" {
						m.preferences[key] = true
					} else {
						m.preferences[key] = !m.preferences[key]
					}
					m.previewSettings()
				}
				return nil
			case "ctrl+s":
				if !m.settingsChanged() {
					p.notice = "No unsaved changes."
					return nil
				}
				if m.options.SaveSettings == nil {
					p.notice = "Saving is unavailable in this session."
					return nil
				}
				values := make(map[string]string)
				for key, value := range m.preferences {
					if value != p.baseline[key] {
						values[key] = strconv.FormatBool(value)
					}
				}
				if p.themeDirty {
					values["theme"] = m.themeName
					values["theme_path"] = ""
				}
				p.saving, p.notice = true, "Saving…"
				return m.tasks.Start(task.Spec{Scope: settingsScope, Run: func() (any, error) { return nil, m.options.SaveSettings(values) }})
			}
			return nil
		}
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
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		p.rows.Top()
		m.rebuildSettings()
		return cmd
	}
	return nil
}

func (m *Model) acceptSettings(msg task.FinishedMsg) {
	p := m.settings
	if p == nil {
		return
	}
	p.saving = false
	if msg.Err != nil {
		p.notice = "Could not save; changes remain unsaved. Inspect the config before retrying. " + m.errorText(msg.Err)
		return
	}
	for key, value := range m.preferences {
		if value != p.baseline[key] {
			p.sources[key] = "file"
		}
	}
	if p.themeDirty {
		p.sources["theme"] = "file"
	}
	p.baselineTheme = m.themeName
	p.restoreTheme = nil
	p.themeDirty = false
	m.options.SettingSources = maps.Clone(p.sources)
	p.baseline, p.notice = maps.Clone(m.preferences), "Settings saved."
	m.rebuildSettings()
}

func (m *Model) settingsView() string {
	p := m.settings
	title := accent("JIRA") + "  " + "Settings"
	if m.settingsChanged() {
		title += dim("  · unsaved changes")
	}
	rows := p.rows.View()
	if len(p.visible) == 0 {
		rows = dim("  No settings match your search.")
	}
	description := ""
	if i, ok := p.rows.Selected(); ok {
		s := p.visible[i]
		description = s.group + " · " + s.description
		if p.sources[s.key] == "env" {
			description += " Controlled by environment."
		}
	}
	explanation := strings.Join(layout.WrapLines(description, m.pageWidth()), "\n")
	hints := []key.Hint{{Key: "ctrl+s", Desc: "save"}, {Key: "esc", Desc: "back"}, {Key: "enter", Desc: "change"}, {Key: "p", Desc: "preview"}, {Key: "/", Desc: "search"}}
	if m.pageWidth() >= 65 {
		hints = append(hints, key.Hint{Key: "↑↓", Desc: "select"}, key.Hint{Key: "tab", Desc: "section"}, key.Hint{Key: "d", Desc: "default"})
	}
	if p.searching {
		hints = []key.Hint{{Key: "enter/↓", Desc: "results"}, {Key: "esc", Desc: "finish search"}}
	}
	prefix := ""
	footer := (key.Renderer{Prefix: &prefix, Width: m.pageWidth(), Gap: " · "}).Render(hints)
	if p.saving {
		footer = "Saving settings…"
	}
	body := frame(rows, m.pageWidth(), max(1, m.height-11))
	return frame(strings.Join([]string{
		title, p.tabs.View(), "", p.input.View(), "", body, "",
		frame(explanation, m.pageWidth(), 2), frame(p.notice, m.pageWidth(), 1), frame(dim(footer), m.pageWidth(), 2),
	}, "\n"), m.pageWidth(), m.height)
}

func (m *Model) pickTheme() {
	choices := []choice{}
	for _, name := range config.ThemeChoices() {
		choices = append(choices, choice{label: name, value: name})
	}
	m.pick("Preview a theme", "theme", "", choices)
}

func (m *Model) setPreviewTheme(name string) {
	restore, err := cli.PreviewTheme(name)
	if err != nil {
		m.settings.notice = m.errorText(err)
		return
	}
	if m.settings.restoreTheme == nil {
		m.settings.restoreTheme = restore
	}
	m.themeName = name
	m.dialogs = newCommandStack()
	m.settings.themeDirty = true
	m.applyStyles()
	m.settings.tabs.ActiveStyle, m.settings.tabs.InactiveStyle = m.tabs.ActiveStyle, m.tabs.InactiveStyle
	m.previewSettings()
}
