package dashboard

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/cli"
)

type commandDialog struct {
	paletteState
	padRows int
}

func newCommandDialog(entries []palette.Entry) *commandDialog {
	return &commandDialog{
		paletteState: newPaletteState(entries),
		padRows:      max(12, len(entries)+5),
	}
}

func (*commandDialog) Title() string     { return "Commands" }
func (*commandDialog) Hints() []key.Hint { return nil }

// Primer v0.2.9's palette.View puts name, description and key in one unbounded
// row, hiding shortcuts when clipped. Keep its filtering and selection owner,
// but lay out bounded name/key columns and expand only the selected description.
func (d *commandDialog) Content(width int) string {
	styles := commandStyles()
	query := styles.Query.Render("/ " + d.menu.Query())
	if d.menu.Query() == "" {
		query = styles.Query.Render("/ ") + dim("Type to filter")
	}
	rows := []string{"", query, ""}
	keyWidth := 0
	for _, entry := range d.entries {
		keyWidth = max(keyWidth, xansi.WcWidth.StringWidth(entry.Key))
	}
	keyWidth = min(keyWidth, max(1, width-5))
	nameWidth := max(1, width-keyWidth-5)
	selected, found := d.menu.Selected()
	for _, entry := range d.visibleEntries() {
		name := layout.NormalizeLine(xansi.WcWidth.Truncate(entry.Name, nameWidth, "…"), nameWidth)
		shortcut := xansi.WcWidth.Truncate(entry.Key, keyWidth, "")
		shortcut = strings.Repeat(" ", max(0, keyWidth-xansi.WcWidth.StringWidth(shortcut))) + shortcut
		if found && entry == selected {
			rows = append(rows, styles.Selected.Render(name+"  "+shortcut+" "))
			description := layout.WrapLines(cli.Text(entry.Desc), max(1, width-2))
			for i := range 2 {
				text := ""
				if i < len(description) {
					text = description[i]
				}
				if i == 1 && len(description) > 2 {
					text = xansi.WcWidth.Truncate(text+"…", max(1, width-2), "…")
				}
				rows = append(rows, "  "+dim(text))
			}
		} else {
			rows = append(rows, "  "+styles.Name.Render(name)+"  "+styles.KeyHint.Render(shortcut)+" ")
		}
	}
	if !found {
		rows = append(rows, dim("No commands match."))
	}
	for len(rows) < d.padRows {
		rows = append(rows, "")
	}
	return strings.Join(layout.NormalizeLines(rows, width), "\n")
}

func (*commandDialog) Footer() string {
	return renderDialogHints(0, []key.Hint{{Key: "enter", Desc: "run"}, {Key: "esc", Desc: "back"}})
}

func (d *commandDialog) ScrollTo() (int, int, bool) { return d.menu.CursorLine() + 1, 3, true }
func (d *commandDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "esc", "ctrl+p", "ctrl+k":
			return d, nil, dialog.ResultClose
		case "enter":
			if _, ok := d.menu.Selected(); ok {
				return d, nil, dialog.ResultSubmit
			}
		}
	}
	return d, d.menu.Update(msg), dialog.ResultNone
}
