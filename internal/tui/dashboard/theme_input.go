package dashboard

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

type themeInputRequest struct {
	title, kind, value string
	parent             *pickerDialog
	editor             *themeEditorDialog
	definition         *config.ThemeFile
	source             *config.SavedTheme
}

func newThemeInputDialog(request themeInputRequest) *themeInputDialog {
	d := &themeInputDialog{
		title: request.title, kind: request.kind, parent: request.parent,
		editor: request.editor, definition: request.definition, source: request.source,
		line: input.NewLine("› ", ""),
	}
	d.line.SetValue(request.value)
	return d
}

func (m *Model) openThemeInput(request themeInputRequest) *themeInputDialog {
	d := newThemeInputDialog(request)
	m.dialogs.Push(d)
	return d
}

type themeInputDialog struct {
	inputRow            int
	title, kind, notice string
	line                input.Line
	parent              *pickerDialog
	editor              *themeEditorDialog
	definition          *config.ThemeFile
	source              *config.SavedTheme
	apply               func(string) error
	cancel              func()
}

func (d *themeInputDialog) Title() string   { return d.title }
func (*themeInputDialog) Hints() []key.Hint { return nil }
func (d *themeInputDialog) Footer() string {
	action := "next"
	switch d.kind {
	case "color", "entities", "base":
		action = "keep"
	case "rename-name":
		action = "rename"
	case "export-path":
		action = "export"
	}
	return renderDialogHints(0, []key.Hint{{Key: "enter", Desc: action}, {Key: "esc", Desc: "cancel"}})
}

func (d *themeInputDialog) Content(width int) string {
	d.line.SetWidth(max(1, width-2))
	text := themeInputDescription(d.kind)
	if d.notice != "" {
		text = d.notice
	}
	description := layout.WrapLines(text, width)
	d.inputRow = len(description) + 1
	return strings.Join(description, "\n") + "\n\n" + d.line.View()
}

func (d *themeInputDialog) ScrollTo() (int, int, bool) { return d.inputRow, 1, true }

func (d *themeInputDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			if d.cancel != nil {
				d.cancel()
			}
			return d, nil, dialog.ResultClose
		case "enter":
			value := d.value()
			if err := validateThemeInput(d.kind, value); err != nil {
				d.notice = cli.Text(err.Error())
				return d, nil, dialog.ResultNone
			}
			if d.apply != nil {
				if err := d.apply(value); err != nil {
					d.notice = cli.Text(err.Error())
					return d, nil, dialog.ResultNone
				}
				d.editor.rebuild()
			}
			return d, nil, dialog.ResultSubmit
		}
	}
	cmd := d.line.Update(msg)
	if d.apply != nil {
		if err := d.apply(strings.TrimSpace(d.line.Value())); err == nil {
			d.notice = "Previewing this color; Enter keeps it, Esc restores."
		}
	}
	return d, cmd, dialog.ResultNone
}

func (d *themeInputDialog) value() string {
	value := d.line.Value()
	if d.kind == "import-path" || d.kind == "export-path" {
		return value
	}
	return strings.TrimSpace(value)
}

func themeInputDescription(kind string) string {
	switch kind {
	case "import-path":
		return "Path to a theme TOML file. Import copies it into Your themes when saved."
	case "export-path":
		return "Write a portable theme TOML file. Choose a new path; existing files are kept."
	case "color":
		return "Enter #RRGGBB. Valid colors preview immediately. Clear to inherit the base color."
	case "entities":
		return "Comma-separated #RRGGBB colors for issue/status accents. Clear to inherit the base palette."
	case "base":
		return "Choose a built-in base preset. Your explicit color overrides are retained."
	default:
		return "Name for Your themes. Existing themes are kept; choose a unique name."
	}
}

func validateThemeInput(kind, value string) error {
	if strings.HasSuffix(kind, "-name") {
		return config.ValidateThemeName(value)
	}
	if strings.TrimSpace(value) == "" && kind != "color" && kind != "entities" {
		return errors.New("enter a value to continue")
	}
	return nil
}
