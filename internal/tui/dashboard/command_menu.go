package dashboard

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func (m *Model) openCommands() {
	entries := []palette.Entry{
		{Name: "Hierarchy roots", Desc: "Browse configured Epics or Initiative roots", Key: "H"},
		{Name: "Hierarchy around issue", Desc: "Read native ancestors and select this issue among siblings", Key: "ctrl+g"},
		{Name: "Open issue in browser", Desc: "Hand off the selected issue URL", Key: "O"},
		{Name: "Copy issue key", Desc: "Send to the terminal clipboard", Key: "y"},
		{Name: "Copy issue URL", Desc: "Send to the terminal clipboard", Key: "Y"},
		{Name: "Operation history", Desc: "Acknowledgements and unconfirmed actions in this session", Key: "L"},
		{Name: "Switch issue", Desc: "Recent issues, current results or an exact key", Key: "ctrl+o"},
		{Name: "Focus list or preview", Desc: "Move keyboard focus between panes", Key: "ctrl+w"},
		{Name: "Next detail section", Desc: "Description, comments, activity and links", Key: "]"},
		{Name: "Open linked issue", Desc: "Choose a link from returned issue details", Key: "o"},
		{Name: "Saved views and lenses", Desc: "Choose a saved search", Key: "v"},
		{Name: "Save current view", Desc: "Save this JQL as a new named search", Key: "ctrl+s"},
		{Name: "Search issues", Desc: "Write a JQL query", Key: "/"},
		{Name: "My work", Desc: "Your unfinished issues", Key: "1"},
	}
	if m.split() || m.listZoom || m.detailFocused && m.enabled("tui_preview") {
		name := "Zoom issue list"
		if m.listZoom {
			name = "Restore preview layout"
		}
		entries = append(entries, palette.Entry{Name: name, Desc: "Temporary list focus; keep the saved preview layout", Key: "z"})
	}
	if !m.options.Actions.ReadOnly && m.options.Actions.Create != nil && !m.actionBusy {
		entries = append(entries, palette.Entry{Name: "Create issue", Desc: "Project/type, summary, description, labels and assignee", Key: "n"})
	}
	if m.options.ProjectQuery != "" {
		entries = append(entries, palette.Entry{Name: "Project", Desc: "Unfinished project work", Key: "2"})
	}
	if !m.detailFocused && !m.previewFocused && m.selected() != nil {
		entries = append(entries, palette.Entry{Name: "Issue children", Desc: "Browse visible native children", Key: "right"})
	}
	if m.hierarchyActive() && !m.detailFocused && !m.previewFocused {
		entries = append(entries, palette.Entry{Name: "Parent level", Desc: "Move to the native parent", Key: "left"}, palette.Entry{Name: "Back in hierarchy", Desc: "Restore the previous list and selection", Key: "backspace"})
	}
	if m.activeIssueKey() != "" {
		entries = append(entries, palette.Entry{Name: "Issue details", Desc: "Focus the selected issue", Key: "enter"})
	}
	if m.activeIssueKey() != "" && !m.options.Actions.ReadOnly && m.options.Actions.Write != nil && !m.actionBusy {
		if m.options.Actions.Editable != nil {
			entries = append(entries, palette.Entry{Name: "Edit issue fields", Desc: "Summary and literal labels on the focused issue", Key: "e"}, palette.Entry{Name: "Edit labels", Desc: "Add and remove labels on the focused issue", Key: "l"})
		}
		entries = append(entries, palette.Entry{Name: "Transition issue", Desc: "Choose a current workflow transition", Key: "t"}, palette.Entry{Name: "Assign issue", Desc: "Choose an exact Jira account", Key: "a"}, palette.Entry{Name: "Comment on issue", Desc: "Write and review a new comment", Key: "c"})
	}
	if !m.loading {
		entries = append(entries, palette.Entry{Name: "Refresh", Desc: "Fetch the latest issues", Key: "r"})
	}
	if !m.loading && !m.isLast && !m.truncated && m.next != "" {
		entries = append(entries, palette.Entry{Name: "Load more", Desc: "Fetch the next results", Key: "m"})
	}
	if len(m.markedKeys()) > 0 && !m.actionBusy && !m.options.Actions.ReadOnly && m.options.Actions.Write != nil {
		entries = append(entries, palette.Entry{Name: "Bulk actions", Desc: "Review actions for the marked issue keys", Key: "B"})
	}
	if !m.detailFocused && !m.previewFocused {
		entries = append(entries, palette.Entry{Name: "Filter loaded issues", Desc: "Narrow the loaded list without changing JQL", Key: "ctrl+f"}, palette.Entry{Name: "Filter by field", Desc: "Status, assignee and label facets", Key: "f"})
		if !m.actionBusy {
			entries = append(entries, palette.Entry{Name: "Select all visible issues", Desc: "Toggle selection for the filtered list", Key: "ctrl+a"}, palette.Entry{Name: "Invert selection", Desc: "Invert marks in the visible list", Key: "X"})
		}
	}
	entries = append(entries,
		palette.Entry{Name: "Settings", Desc: "Customize the issue dashboard", Key: ","},
		palette.Entry{Name: "Keyboard help", Desc: "All navigation shortcuts", Key: "?"},
		palette.Entry{Name: "Quit", Desc: "Return to the terminal", Key: "q"},
	)
	m.dialogs.SetFrame(commandFrameHeight(28))
	m.dialogs.Push(newCommandDialog(entries))
}

func commandStyles() palette.Styles {
	marker := func(row string) string { return "› " + row }
	styles := palette.Styles{Selected: lipgloss.NewStyle().Transform(marker)}
	if cli.StyleEnabled() {
		theme := cli.ResolvedTheme()
		styles.Title, styles.Desc, styles.KeyHint = *theme.Dim, *theme.Dim, *theme.Blue
		styles.Name, styles.Match, styles.Query = *theme.Bold, *theme.Blue, *theme.Blue
		styles.Selected = theme.Blue.Reverse(true).Transform(marker)
	}
	return styles
}
