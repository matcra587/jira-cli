package dashboard

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
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
	if !m.options.Actions.ReadOnly && m.options.Actions.Create != nil && !m.actionBusy {
		entries = append(entries, palette.Entry{Name: "Create issue", Desc: "Project/type, summary, description, labels and assignee", Key: "n"})
	}
	if m.options.ProjectQuery != "" {
		entries = append(entries, palette.Entry{Name: "Project", Desc: "Unfinished project work", Key: "2"})
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
	menu := palette.New("Type to filter", entries, commandStyles())
	m.dialogs.Push(&commandDialog{menu: menu, padRows: len(strings.Split(menu.View(), "\n"))})
}

func (m *Model) updateCommands(msg tea.Msg) tea.Cmd {
	manualScroll := false
	switch m.dialogs.Top().(type) {
	case *writeReview, *operationDialog, *bulkReview, *creationReview, *creationOutcomeDialog, *descriptionReview, *descriptionReload:
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
		case *descriptionDialog:
			m.keepDescriptionDraft(d)
		case *pickerDialog:
			if result == dialog.ResultClose && d.kind == "create-type" && d.creation == m.creation {
				m.creation.project = core.IssueCreateProjectRead{}
				m.creation.typeID = ""
			}
		case *creationLabels:
			if d.draft == m.creation {
				m.creation.labels = slices.Clone(d.labels)
				m.creation.labelsSet = result == dialog.ResultSubmit || !slices.Equal(d.labels, d.original) || m.creation.labelsSet
				m.creation.pendingLabel = d.line.Value()
			}
		case *summaryDialog:
			if d.value() == d.original {
				delete(m.fieldDrafts, d.id+":summary")
			} else {
				m.fieldDrafts[d.id+":summary"] = d.value()
			}
		case *labelsDialog:
			changed := !slices.Equal(d.labels, d.original)
			if !changed && d.line.Value() == "" {
				delete(m.labelDrafts, d.id)
			} else {
				m.labelDrafts[d.id] = labelDraft{labels: slices.Clone(d.labels), pending: d.line.Value(), changed: changed}
			}
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
	case *descriptionDialog:
		return tea.Batch(cmd, m.acceptDescriptionEditor(d))
	case *descriptionReview:
		return tea.Batch(cmd, m.submitDescription(d))
	case *descriptionReload:
		if m.dialogs.Top() != d.editor {
			return cmd
		}
		snapshot := d.editor.draft.session.Snapshot()
		return tea.Batch(cmd, m.loadDescription(snapshot.IssueID, snapshot.IssueID, d.editor))
	case *creationDiscard:
		if d.draft == m.creation {
			m.creation = &creationDraft{}
			m.creationEditor = nil
			return tea.Batch(cmd, m.creationProjects())
		}
		return cmd
	case *creationReview:
		return tea.Batch(cmd, m.submitCreation(d))
	case *creationLabels:
		return cmd
	case *creationText:
		return tea.Batch(cmd, m.acceptCreationText(d))
	case *summaryDialog:
		m.acceptSummary(d)
		return cmd
	case *labelsDialog:
		m.acceptLabels(d)
		return cmd
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
