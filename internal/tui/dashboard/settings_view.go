package dashboard

import (
	"strconv"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

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
		value := p.values[s.key]
		if enabled, ok := m.preferences[s.key]; ok {
			value = strconv.FormatBool(enabled)
		}
		value = config.Redacted(s.key, value)
		if s.key == "tui_workspace" {
			value = "Views, tabs and lenses"
		}
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
		} else if s.key == "tui_preview_size" {
			value = accent(value + "%")
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
		if p.editor.setting.key == "tui_hierarchy_query" && m.options.Actions.HierarchyTypes != nil {
			hints = []key.Hint{{Key: "enter", Desc: "keep"}, {Key: "esc", Desc: "cancel"}, {Key: "f2", Desc: "issue type"}}
		}
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
