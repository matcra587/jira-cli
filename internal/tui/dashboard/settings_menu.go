package dashboard

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/config"
)

type settingMenu struct {
	pickerDialog
	page *settingsPage
}

func (m *Model) openSettingMenu(s setting) {
	choices := make([]choice, 0)
	selected := 0
	for i, value := range config.SettingValueChoices(s.key) {
		label, description := settingChoice(s.key, value)
		if value == m.settings.values[s.key] {
			label += " · current"
			selected = i
		}
		choices = append(choices, choice{label: label, description: description, value: value})
	}
	m.pick(s.name, "setting-value", s.key, choices)
	p := m.dialogs.Pop().(*pickerDialog)
	for range selected {
		p.menu.Update(key.Replay("down"))
	}
	m.dialogs.Push(&settingMenu{pickerDialog: *p, page: m.settings})
}

func settingChoice(name, value string) (string, string) {
	switch name + "/" + value {
	case "secret_backend/keyring":
		return "System keyring", "Use your system credential store"
	case "secret_backend/env":
		return "Environment variable", "Read a token from the environment"
	case "secret_backend/1password":
		return "1Password", "Use a 1Password vault and item"
	case "tui_preview_dock/auto":
		return "Automatic", "Right on wide screens; bottom on narrower ones"
	case "tui_preview_dock/right":
		return "Right", "Place the preview beside the list when both panes fit"
	case "tui_preview_dock/left":
		return "Left", "Place the preview before the issue list when both panes fit"
	case "tui_preview_dock/bottom":
		return "Bottom", "Place the preview below the list when both panes fit"
	case "glyphs/default":
		return "Standard symbols", "Works with ordinary terminal fonts"
	case "glyphs/nerd":
		return "Nerd Font", "Requires a Nerd Font in your terminal"
	default:
		return value, ""
	}
}

func (d *settingMenu) Footer() string {
	return renderDialogHints(d.hintWidth, []key.Hint{{Key: "↑↓", Desc: "choose"}, {Key: "enter", Desc: "keep in draft"}, {Key: "esc", Desc: "cancel"}})
}

func (d *settingMenu) Content(width int) string {
	return strings.Join(layout.NormalizeLines(strings.Split(d.menu.View(), "\n"), width), "\n")
}

func (d *settingMenu) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.pickerDialog.Update(msg)
	return d, cmd, result
}
