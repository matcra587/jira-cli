package dashboard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/input"

	"github.com/matcra587/jira-cli/internal/config"
)

type settingInput struct {
	setting setting
	line    input.Line
}

// Copy only writable public keys. Neither credentials nor alias definitions
// enter the scalar editor; their owners have different validation contracts.
func publicSettingValues(values map[string]string) map[string]string {
	result := make(map[string]string)
	defaults := config.Defaults()
	for _, key := range config.WritableSettingKeys() {
		result[key] = fmt.Sprint(defaults[key])
		if key == "tui_workspace" {
			result[key] = "{}"
		}
		if value, ok := values[key]; ok {
			result[key] = value
		}
	}
	return result
}

func (m *Model) editSetting(s setting, reset bool) {
	p := m.settings
	if s.key == "tui_workspace" {
		if reset {
			m.keepSetting(s.key, "{}")
		} else {
			m.openWorkspaceSettings()
		}
		return
	}
	if s.key == "api_token" || s.key == "aliases" {
		p.notice = s.description
		return
	}
	value := p.values[s.key]
	if reset {
		value = fmt.Sprint(config.Defaults()[s.key])
		m.keepSetting(s.key, value)
		return
	}
	choices := config.SettingValueChoices(s.key)
	if slices.Equal(choices, []string{"true", "false"}) {
		m.keepSetting(s.key, strconv.FormatBool(value != "true"))
		return
	}
	if len(choices) > 0 {
		m.openSettingMenu(s)
		return
	}
	placeholder := "Empty clears the value"
	if s.key == "tui_preview_size" {
		placeholder = "25–75 percent"
	}
	line := input.NewLine("› ", placeholder)
	line.SetValue(value)
	if line.Value() != value {
		p.notice = "This value contains characters the single-line editor cannot preserve. Use jira config set."
		return
	}
	p.editor = &settingInput{setting: s, line: line}
}

func (m *Model) keepSetting(key, value string) bool {
	if err := config.ValidateSettingValue(key, value); err != nil {
		m.settings.notice = m.errorText(err)
		return false
	}
	m.settings.values[key] = value
	if key == "tui_preview_dock" || key == "tui_preview_size" {
		m.resize()
	}
	m.settings.notice = "Kept in draft. Ctrl+S saves; applies on next launch."
	if m.options.ReloadSettings != nil {
		m.settings.notice = "Kept in draft. Ctrl+S saves and applies."
	}
	m.rebuildSettings()
	return true
}

func (m *Model) updateSettingEditor(msg tea.Msg) tea.Cmd {
	p, editor := m.settings, m.settings.editor
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "f2":
			return m.loadHierarchyTypes()
		case "esc":
			p.editor, p.notice = nil, "Edit canceled."
			return nil
		case "enter", "ctrl+s":
			if m.keepSetting(editor.setting.key, editor.line.Value()) {
				p.editor = nil
			}
			return nil
		}
	}
	// Primer Line delegates paste to Bubbles, whose sanitization can change
	// configuration strings. Refuse controls rather than authoring altered input.
	if paste, ok := msg.(tea.PasteMsg); ok {
		if strings.ContainsFunc(paste.Content, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError }) {
			p.notice = "Paste a single-line setting without control characters."
			return nil
		}
		editor.line.ReplaceBeforeCursor(0, paste.Content)
		return nil
	}
	return editor.line.Update(msg)
}
