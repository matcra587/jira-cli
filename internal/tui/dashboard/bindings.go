package dashboard

import (
	"slices"

	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/config"
)

func workspaceBindings(overrides map[string][]string) (key.Registry, error) {
	if err := config.ValidateWorkspaceKeys(overrides); err != nil {
		return nil, err
	}
	bindings := make(key.Registry)
	for _, action := range config.WorkspaceActions() {
		binding := bkey.NewBinding(bkey.WithKeys(action.Keys...), bkey.WithHelp(action.Keys[0], action.Title))
		bindings[action.ID] = &binding
	}
	if err := bindings.Rebind(overrides); err != nil {
		return nil, err
	}
	return bindings, nil
}

func (m *Model) matchesAction(msg tea.KeyPressMsg, id string) bool {
	binding := m.bindings[id]
	return binding != nil && bkey.Matches(msg, *binding)
}

func (m *Model) effectiveKey(defaultKey string) string {
	for _, action := range m.actions {
		if slices.Contains(action.Keys, defaultKey) {
			if binding := m.bindings[action.ID]; binding != nil {
				return binding.Help().Key
			}
		}
	}
	return defaultKey
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		return m.runKey(msg)
	}
	for _, action := range m.actions {
		if m.matchesAction(msg, action.ID) {
			return m.runKey(key.Replay(action.Keys[0]))
		}
	}
	return nil
}

type keyHelpDialog struct{ commandDialog }

func (*keyHelpDialog) Title() string  { return "Keyboard shortcuts" }
func (*keyHelpDialog) Footer() string { return "shortcuts · type · esc back" }

func (d *keyHelpDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.commandDialog.Update(msg)
	if result == dialog.ResultSubmit {
		result = dialog.ResultNone
	}
	return d, cmd, result
}

func (m *Model) openKeyHelp() {
	entries := make([]palette.Entry, 0, len(m.bindings))
	for _, action := range m.actions {
		entries = append(entries, palette.Entry{Name: action.Title, Desc: "Main view · configure in Settings → Workspace → Keybindings", Key: m.bindings[action.ID].Help().Key})
	}
	m.dialogs.SetFrame(commandFrameHeight(28))
	m.dialogs.Push(&keyHelpDialog{commandDialog: *newCommandDialog(entries)})
}

func (m *Model) searchShortcutHint() string {
	details := m.effectiveKey("enter")
	if details == "enter" {
		details = "Enter"
	}
	return m.effectiveKey("/") + " or " + details
}

func (m *Model) navigationKey(defaultKey string) string {
	value := m.effectiveKey(defaultKey)
	switch value {
	case "right":
		return "→"
	case "left":
		return "←"
	}
	return value
}
