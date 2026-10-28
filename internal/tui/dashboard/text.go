package dashboard

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
)

type textDialog struct {
	title, kind, target, description string
	line                             input.Line
	area                             *textarea.Model
	inputLine                        int
}

func (d *textDialog) Title() string     { return d.title }
func (d *textDialog) Hints() []key.Hint { return nil }
func (d *textDialog) Footer() string {
	if d.area != nil {
		return "ctrl+s review · esc"
	}
	return "enter next · esc"
}

func (d *textDialog) Content(width int) string {
	value := ""
	if d.area != nil {
		d.area.SetWidth(max(1, width))
		d.area.SetHeight(6)
		value = d.area.View()
	} else {
		d.line.SetWidth(max(1, width-2))
		value = d.line.View()
	}
	description := layout.WrapLines(d.description, width)
	d.inputLine = len(description) + 1
	return strings.Join(description, "\n") + "\n\n" + value
}

func (d *textDialog) ScrollTo() (int, int, bool) {
	line := d.inputLine
	if d.area != nil {
		line += min(5, d.area.Line())
	}
	return line, 1, true
}

func (d *textDialog) value() string {
	if d.area != nil {
		return d.area.Value()
	}
	return d.line.Value()
}

func (d *textDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return d, nil, dialog.ResultClose
		case "enter":
			if d.area == nil && strings.TrimSpace(d.value()) != "" {
				return d, nil, dialog.ResultSubmit
			}
		case "ctrl+s":
			if d.area != nil && strings.TrimSpace(d.value()) != "" {
				return d, nil, dialog.ResultSubmit
			}
		}
	}
	if d.area != nil {
		next, cmd := d.area.Update(msg)
		*d.area = next
		return d, cmd, dialog.ResultNone
	}
	return d, d.line.Update(msg), dialog.ResultNone
}

func (m *Model) text(title, kind, target, description string) {
	m.dialogs.Push(&textDialog{title: title, kind: kind, target: target, description: description, line: input.NewLine("› ", "")})
}
