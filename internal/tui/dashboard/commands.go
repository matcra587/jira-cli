package dashboard

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/cli"
)

func newCommandStack() *dialog.Stack {
	styles := dialog.Styles{Box: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)}
	if cli.StyleEnabled() {
		theme := cli.ResolvedTheme()
		styles.Box = styles.Box.BorderForeground(theme.Blue.GetForeground())
		styles.Title, styles.HintKey, styles.HintText = *theme.Bold, *theme.Blue, *theme.Dim
	}
	return dialog.New(dialog.NewFrame(dialog.FrameConfig{
		Styles: styles, MaxWidth: 76, MaxHeight: 24,
		WidthFraction: .95, HeightFraction: .95, Margin: 1,
	}))
}

type commandDialog struct {
	menu    palette.Model
	padRows int
}

func (d *commandDialog) Title() string { return "Commands" }

func (d *commandDialog) Content(width int) string {
	// Stable width and height keep the popup anchored while filtering.
	// Primer's frame owns shrinking, scrolling and keeping selection visible.
	rows := strings.Split(d.menu.View(), "\n")
	for len(rows) < max(12, d.padRows) {
		rows = append(rows, "")
	}
	return strings.Join(layout.NormalizeLines(rows, width), "\n")
}

func (*commandDialog) Hints() []key.Hint { return nil }

func (*commandDialog) Footer() string {
	prefix := ""
	renderer := key.Renderer{Prefix: &prefix, Gap: "  "}
	if cli.StyleEnabled() {
		renderer.Styles = key.Styles{Key: *cli.ResolvedTheme().Blue, Text: *cli.ResolvedTheme().Dim}
	}
	return renderer.Render([]key.Hint{{Key: "enter", Desc: "run"}, {Key: "esc", Desc: "back"}})
}
func (d *commandDialog) ScrollTo() (int, int, bool) { return d.menu.CursorLine(), 1, true }
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
	if m.options.ProjectQuery != "" {
		entries = append(entries, palette.Entry{Name: "Project", Desc: "Unfinished project work", Key: "2"})
	}
	if m.activeIssueKey() != "" {
		entries = append(entries, palette.Entry{Name: "Issue details", Desc: "Focus the selected issue", Key: "enter"})
	}
	if m.activeIssueKey() != "" && !m.options.Actions.ReadOnly && m.options.Actions.Write != nil && !m.actionBusy {
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
	entries = append(entries,
		palette.Entry{Name: "Select all loaded issues", Desc: "Toggle selection for the loaded list", Key: "ctrl+a"},
		palette.Entry{Name: "Invert selection", Desc: "Invert marks in the loaded list", Key: "X"},
		palette.Entry{Name: "Settings", Desc: "Customize the issue dashboard", Key: ","},
		palette.Entry{Name: "Keyboard help", Desc: "All navigation shortcuts", Key: "?"},
		palette.Entry{Name: "Quit", Desc: "Return to the terminal", Key: "q"},
	)
	menu := palette.New("Type to filter", entries, commandStyles())
	m.dialogs.Push(&commandDialog{menu: menu, padRows: len(strings.Split(menu.View(), "\n"))})
}

func (m *Model) updateCommands(msg tea.Msg) tea.Cmd {
	manualScroll := false
	switch m.dialogs.Top().(type) {
	case *writeReview, *operationDialog, *bulkReview:
		manualScroll = true
	}
	if manualScroll {
		if press, ok := msg.(tea.KeyPressMsg); ok {
			switch press.String() {
			case "pgdown":
				m.dialogs.ScrollBy(max(1, m.height/2))
				return nil
			case "pgup":
				m.dialogs.ScrollBy(-max(1, m.height/2))
				return nil
			}
		}
	}
	cmd, closed, result := m.dialogs.Update(msg)
	if result != dialog.ResultNone {
		switch d := closed.(type) {
		case *bulkTextDialog:
			m.keepBulkDraft(d)
		case *textDialog:
			if d.kind == "comment" {
				m.drafts[d.target] = d.value()
			}
		}
	}
	if result != dialog.ResultSubmit {
		return cmd
	}
	switch d := closed.(type) {
	case *commandDialog:
		if entry, ok := d.menu.Selected(); ok {
			return tea.Batch(cmd, m.updateKey(key.Replay(entry.Key)))
		}
	case *pickerDialog:
		return tea.Batch(cmd, m.acceptPicker(d))
	case *operationDialog:
		if d.earlier {
			m.openBatchHistory()
		} else {
			m.openBatchResult(m.lastBatch)
		}
		return cmd
	case *bulkReview:
		return tea.Batch(cmd, m.submitBulk(d))
	case *bulkTextDialog:
		return tea.Batch(cmd, m.acceptBulkText(d))
	case *writeReview:
		return tea.Batch(cmd, m.submitWrite(d))
	case *textDialog:
		return tea.Batch(cmd, m.acceptText(d))
	}

	return cmd
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
