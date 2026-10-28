package dashboard

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/button"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/list"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

type editFields struct {
	request      uint64
	kind, target string
	issue        *jira.IssueBean
}

type summaryDialog struct {
	textDialog
	id, original string
}

type labelDraft struct {
	labels  []string
	pending string
	changed bool
}

func (d *summaryDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.textDialog.Update(msg)
	return d, cmd, result
}

type labelsDialog struct {
	hint              string
	id, target        string
	labels, original  []string
	rows              *list.Model
	line              input.Line
	adding            bool
	inputRow, listRow int
}

func (d *labelsDialog) Title() string   { return "Labels · " + cli.Text(d.target) }
func (*labelsDialog) Hints() []key.Hint { return nil }
func (d *labelsDialog) Footer() string {
	if d.adding {
		return "enter add · tab · ^S"
	}
	return "x remove · tab · ^S"
}

func (d *labelsDialog) rebuild() {
	rows := make([]string, len(d.labels))
	for i, label := range d.labels {
		rows[i] = cli.Text(label)
	}
	d.rows.SetRows(rows)
}

func (d *labelsDialog) addPending() {
	if d.line.Value() == "" {
		return
	}
	if !slices.Contains(d.labels, d.line.Value()) {
		d.labels = append(d.labels, d.line.Value())
		d.rebuild()
	}
	d.line.SetValue("")
}

func (d *labelsDialog) Content(width int) string {
	hint := d.hint
	if hint == "" {
		hint = "Ctrl+S reviews; Esc keeps this session draft."
	}
	intro := layout.WrapLines(fmt.Sprintf("%d labels. Empty removes all. %s", len(d.labels), hint), width)
	d.line.SetWidth(max(1, width-2))
	d.rows.SetSize(max(1, width-2), 4)
	d.inputRow = len(intro) + 1
	d.listRow = d.inputRow + 2
	return strings.Join(intro, "\n") + "\n\n" + d.line.View() + "\n\n" + d.rows.View()
}

func (d *labelsDialog) ScrollTo() (int, int, bool) {
	if d.adding {
		return d.inputRow, 1, true
	}
	start, _ := d.rows.VisibleRange()
	return d.listRow + d.rows.Cursor() - start, 1, true
}

func (d *labelsDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "esc":
			return d, nil, dialog.ResultClose
		case "ctrl+s":
			d.addPending()
			return d, nil, dialog.ResultSubmit
		case "tab", "shift+tab":
			d.adding = !d.adding
			if d.adding {
				d.line.Focus()
			} else {
				d.line.Blur()
			}
			return d, nil, dialog.ResultNone
		case "enter":
			if d.adding {
				d.addPending()
			}
			return d, nil, dialog.ResultNone
		case "x", "delete", "backspace":
			if !d.adding {
				if i, ok := d.rows.Selected(); ok {
					d.labels = slices.Delete(d.labels, i, i+1)
					d.rebuild()
				}
				return d, nil, dialog.ResultNone
			}
		}
		if !d.adding {
			switch press.String() {
			case "up", "k":
				d.rows.MoveUp(1)
			case "down", "j":
				d.rows.MoveDown(1)
			case "home", "g":
				d.rows.Top()
			case "end", "G":
				d.rows.Bottom()
			case "pgup":
				d.rows.PageUp()
			case "pgdown":
				d.rows.PageDown()
			}
		}
	}
	if d.adding {
		return d, d.line.Update(msg), dialog.ResultNone
	}
	return d, d.rows.Update(msg), dialog.ResultNone
}

func (m *Model) openEditMenu() {
	if !m.canWrite() {
		return
	}
	choices := []choice{
		{label: "Summary", description: "Read and edit this issue's current summary", value: "summary"},
		{label: "Labels", description: "Add or remove literal labels on this issue", value: "labels"},
	}
	if m.options.Actions.Description != nil {
		choices = append(choices, choice{label: "Description", description: "Preserve native protected content while editing text", value: "description"})
	}
	m.pick("Edit · "+cli.Text(m.activeIssueKey()), "edit-mode", m.activeIssueKey(), choices)
}

func (m *Model) loadEdit(kind, target string) tea.Cmd {
	if !m.canWrite() {
		return nil
	}
	if m.options.Actions.Editable == nil {
		m.notice = "Issue editing is unavailable."
		return nil
	}
	request := m.interaction
	m.editReadRequest = request
	m.notice = "Loading editor for " + cli.Text(target) + "…"
	read := m.options.Actions.Editable
	return m.tasks.Start(task.Spec{Scope: "edit-fields", Run: func() (any, error) {
		issue, err := read(target)
		return editFields{request: request, kind: kind, target: target, issue: issue}, err
	}})
}

func (m *Model) acceptEditFields(msg task.FinishedMsg) {
	result, ok := msg.Result.(editFields)
	if !ok {
		return
	}
	if result.request != m.interaction {
		if m.editReadRequest == result.request && m.notice == "Loading editor for "+cli.Text(result.target)+"…" {
			m.notice = ""
		}
		return
	}
	if msg.Err != nil {
		m.notice = m.errorText(msg.Err)
		return
	}
	issue := result.issue
	if issue == nil || !issue.ID.Set || issue.ID.Null || issue.ID.Value == "" || issue.Key.Value != result.target || !issue.Fields.Set || issue.Fields.Null {
		m.notice = "Issue editing response has incomplete identity or fields."
		return
	}
	m.notice = ""
	switch result.kind {
	case "summary":
		value := issue.Fields.Value.Summary
		if !value.Set || value.Null {
			m.notice = "Summary was not returned; no editor opened."
			return
		}
		d := &summaryDialog{id: issue.ID.Value, original: value.Value, textDialog: textDialog{title: "Summary · " + cli.Text(result.target), kind: "summary", target: result.target, description: "Edit this issue's summary. Enter opens review; Esc keeps a session draft.", line: input.NewLine("› ", "")}}
		d.line.SetValue(value.Value)
		if d.line.Value() != value.Value {
			m.notice = "Summary contains characters this line editor cannot preserve. Use native issue edit input."
			return
		}
		if draft, exists := m.fieldDrafts[issue.ID.Value+":summary"]; exists {
			d.line.SetValue(draft)
		}
		m.dialogs.Push(d)
	case "labels":
		value := issue.Fields.Value.Labels
		if !value.Set {
			m.notice = "Labels were not returned; no editor opened."
			return
		}
		labels := slices.Clone(value.Value)
		d := &labelsDialog{id: issue.ID.Value, target: result.target, labels: labels, original: slices.Clone(labels), rows: list.New(), line: input.NewLine("+ ", "Enter a literal label"), adding: true}
		d.rows.Styles.Cursor = lipgloss.NewStyle().Transform(func(text string) string { return "› " + text })
		if draft, exists := m.labelDrafts[d.id]; exists {
			if draft.changed {
				d.labels = slices.Clone(draft.labels)
			}
			d.line.SetValue(draft.pending)
		}
		d.rebuild()
		m.dialogs.Push(d)
	}
}

func (m *Model) reviewField(kind, key, id, value, description string) {
	confirm := cancelDefaultConfirm(description+"\n\nThis replaces the field on issue ID "+cli.Text(id)+". Metadata is checked again; concurrent edits can still race this write.", "Apply")
	m.dialogs.Push(&writeReview{Confirm: confirm, kind: kind, target: key, value: value, targetID: id, cancelDefault: true})
}

func cancelDefaultConfirm(description, acceptLabel string) *dialog.Confirm {
	focus := lipgloss.NewStyle().Transform(func(text string) string { return "[" + text + "]" })
	if cli.StyleEnabled() {
		focus = cli.ResolvedTheme().Blue.Reverse(true).Transform(func(text string) string { return "[" + text + "]" })
	}
	return dialog.NewConfirmButtons(description,
		dialog.ConfirmButton{Button: button.Button{Label: "Cancel", Focused: focus}, Default: true, Keys: []string{"n", "N"}},
		dialog.ConfirmButton{Button: button.Button{Label: acceptLabel, Focused: focus}, Accept: true, Keys: []string{"y", "Y"}},
	)
}

func (m *Model) newLabelRows() *list.Model {
	rows := list.New()
	rows.Styles.Cursor = lipgloss.NewStyle().Transform(func(text string) string { return "› " + text })
	return rows
}

func (m *Model) acceptSummary(d *summaryDialog) {
	if d.value() == d.original {
		delete(m.fieldDrafts, d.id+":summary")
		m.notice = "Summary unchanged; no write submitted."
		return
	}
	m.reviewField("summary", d.target, d.id, d.value(), "Replace summary on "+cli.Text(d.target)+"?\n\nCurrent:\n"+cli.Text(d.original)+"\n\nNew:\n"+cli.Text(d.value()))
}

func (m *Model) acceptLabels(d *labelsDialog) {
	if slices.Equal(d.labels, d.original) {
		delete(m.labelDrafts, d.id)
		m.notice = "Labels unchanged; no write submitted."
		return
	}
	labels := slices.Clone(d.labels)
	if labels == nil {
		labels = []string{}
	}
	encoded, err := jira.MarshalNoEscape(labels)
	if err != nil {
		m.notice = m.errorText(err)
		return
	}
	description := "Replace all labels on " + cli.Text(d.target) + "?\n\nCurrent:\n" + cli.TextBlock(strings.Join(d.original, "\n")) + "\n\nNew:\n" + cli.TextBlock(strings.Join(labels, "\n"))
	if len(labels) == 0 {
		description += "\nAll labels will be removed."
	}
	m.reviewField("labels", d.target, d.id, string(encoded), description)
}
