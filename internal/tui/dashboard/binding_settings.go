package dashboard

import (
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/config"
)

type bindingCapture struct {
	focusLine                int
	owner                    *workspaceDialog
	action, shortcut, notice string
	appendKey                bool
}

func (*bindingCapture) Title() string     { return "Capture shortcut" }
func (*bindingCapture) Hints() []key.Hint { return nil }
func (*bindingCapture) Footer() string    { return "press a key · esc cancel" }
func (d *bindingCapture) Content(width int) string {
	name := d.action
	for _, action := range config.WorkspaceActions() {
		if action.ID == d.action {
			name = action.Title
		}
	}
	text := "Action: " + name + "\n\nPress the shortcut to use for this action.\nEscape and Ctrl+C keep their cancel/exit roles."
	lines := layout.WrapLines(text, width)
	d.focusLine = 0
	if d.notice != "" {
		d.focusLine = len(lines) + 1
		lines = append(lines, "")
		lines = append(lines, layout.WrapLines(d.notice, width)...)
	}
	return strings.Join(lines, "\n")
}

func (d *bindingCapture) ScrollTo() (int, int, bool) { return d.focusLine, 2, true }

func (d *bindingCapture) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		if press.String() == "esc" {
			return d, nil, dialog.ResultClose
		}
		d.shortcut = press.String()
		return d, nil, dialog.ResultSubmit
	}
	return d, nil, dialog.ResultNone
}

func (m *Model) acceptBindingCapture(d *bindingCapture) tea.Cmd {
	owner := d.owner
	if owner.page != m.settings || m.dialogs.Top() != owner {
		return nil
	}
	w := owner.working.Clone()
	if w.Keys == nil {
		w.Keys = make(map[string][]string)
	}
	shortcuts := []string{d.shortcut}
	if d.appendKey {
		for _, action := range config.WorkspaceActions() {
			if action.ID == d.action {
				shortcuts = slices.Clone(action.Keys)
			}
		}
		if previous, ok := w.Keys[d.action]; ok {
			shortcuts = slices.Clone(previous)
		}
		shortcuts = append(shortcuts, d.shortcut)
	}
	w.Keys[d.action] = shortcuts
	if !m.keepWorkspace(owner, w) {
		d.notice = owner.notice
		m.dialogs.Push(d)
		return nil
	}
	owner.rebuild()
	return nil
}

func bindingChoices(w config.Workspace) []choice {
	choices := make([]choice, 0)
	for _, action := range config.WorkspaceActions() {
		shortcuts := action.Keys
		if override, ok := w.Keys[action.ID]; ok {
			shortcuts = override
		}
		choices = append(choices, choice{label: action.Title, description: strings.Join(shortcuts, " · "), value: action.ID})
	}
	return choices
}

func resetWorkspaceBinding(w config.Workspace, id string) config.Workspace {
	w.Keys = maps.Clone(w.Keys)
	delete(w.Keys, id)
	return w
}
