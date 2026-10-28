package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/prompt"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Actions are invocation-bound capabilities supplied by the CLI domain owners.
// The view never constructs a client, resolves credentials, or owns a writer.
type Actions struct {
	Description        func(string) (*core.IssueDescriptionSession, error)
	ReviewDescription  func(*core.IssueDescriptionSession, string) (core.IssueDescriptionMerge, error)
	ReplaceDescription func(*core.IssueDescriptionSession, string, core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error)
	HierarchyTypes     func() ([]jira.IssueTypeDetails, error)
	Projects           func() ([]core.IssueCreateProjectRead, bool, error)
	Project            func(string) (core.IssueCreateProjectRead, error)
	CreateTypes        func(string) (map[string]json.RawMessage, error)
	PrepareCreate      func(core.IssueCreationInput) (jira.IssueWriteRequest, error)
	Create             func(jira.IssueWriteRequest) (envelope.IssueCreateOutput, error)
	Editable           func(string) (*jira.IssueBean, error)
	Browse             func(string) (string, error)
	ReadOnly           bool
	Transitions        func(string) ([]jira.IssueTransition, error)
	Users              func(string) ([]*jira.User, error)
	CurrentUser        func() (string, error)
	Write              func(kind, key, value string) (core.IssueActionReceipt, error)
	Comments           func(string, int) (*jira.CommentPage, error)
}

type (
	actionChoices struct {
		request      uint64
		kind, target string
		choices      []choice
	}
	actionResult struct {
		operation    uint64
		kind, target string
		targetID     string
		receipt      core.IssueActionReceipt
	}
)

type writeReview struct {
	*dialog.Confirm
	kind, target, value string
	targetID            string
	cancelDefault       bool
	framePadding        int
	hintWidth           int
}

func (d *writeReview) Title() string {
	if d.kind == "labels" {
		return prompt.CenterRow("Confirm labels · "+cli.Text(d.target), d.hintWidth)
	}
	return "Review " + d.kind + " · " + cli.Text(d.target)
}
func (d *writeReview) Hints() []key.Hint { return nil }
func (d *writeReview) Footer() string {
	if d.kind == "labels" {
		return "\n" + lipgloss.PlaceHorizontal(d.hintWidth, lipgloss.Center, renderDialogHints(d.hintWidth, []key.Hint{{Key: "tab", Desc: "select"}, {Key: "enter", Desc: "confirm"}, {Key: "esc", Desc: "back"}}))
	}
	if d.cancelDefault {
		return "y apply · n/esc back"
	}
	return "y/enter submit · esc back"
}

func (d *writeReview) Content(width int) string {
	return strings.Join(layout.WrapLines(d.Confirm.Content(width), width), "\n")
}

func (d *writeReview) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

func (m *Model) activeIssueKey() string {
	if m.detailFocused || m.previewFocused {
		return m.detailKey
	}
	return m.selectedKey()
}

func (m *Model) canWrite() bool {
	if m.actionBusy {
		m.notice = "A Jira action is still running."
		return false
	}
	if m.options.Actions.ReadOnly || m.options.Actions.Write == nil {
		m.notice = "Jira actions are unavailable in read-only mode."
		return false
	}
	if m.activeIssueKey() == "" {
		m.notice = "Select an issue first."
		return false
	}
	return true
}

func (m *Model) openTransitions() tea.Cmd {
	if !m.detailFocused && !m.previewFocused {
		if keys := m.markedKeys(); len(keys) > 0 {
			return m.loadBulkTransitions(keys)
		}
	}
	if !m.canWrite() || m.options.Actions.Transitions == nil {
		return nil
	}
	target := m.activeIssueKey()
	request := m.interaction
	m.notice = "Loading available transitions…"
	return m.tasks.Start(task.Spec{Scope: "action-choices", Run: func() (any, error) {
		transitions, err := m.options.Actions.Transitions(target)
		choices := make([]choice, 0, len(transitions))
		for _, t := range transitions {
			if t.IsAvailable.Set && !t.IsAvailable.Value {
				continue
			}
			choices = append(choices, choice{label: cli.Text(t.Name.Value + " → " + t.To.Value.Name.Value), description: "Transition " + cli.Text(t.ID.Value), value: t.ID.Value})
		}
		return actionChoices{request: request, kind: "transition", target: target, choices: choices}, err
	}})
}

func (m *Model) openAssign() {
	if !m.detailFocused && !m.previewFocused {
		if keys := m.markedKeys(); len(keys) > 0 {
			m.openBulkAssign(keys)
			return
		}
	}
	if !m.canWrite() {
		return
	}
	m.pick("Assign · "+cli.Text(m.activeIssueKey()), "assign-mode", m.activeIssueKey(), []choice{
		{label: "Assign to me", value: "me"},
		{label: "Find a person", description: "Search visible active Jira users", value: "search"},
		{label: "Use an account ID", description: "Choose one exact account", value: "id"},
		{label: "Unassign", value: "clear"},
	})
}

func (m *Model) openComment() {
	if !m.detailFocused && !m.previewFocused {
		if keys := m.markedKeys(); len(keys) > 0 {
			m.openBulkText("comment", keys)
			return
		}
	}
	if !m.canWrite() {
		return
	}
	target := m.activeIssueKey()
	area := input.NewTextArea()
	area.SetValue(m.drafts[target])
	area.Focus()
	m.dialogs.Push(&textDialog{
		title: "Comment · " + cli.Text(target), kind: "comment", target: target,
		description: "Write a new Markdown comment. Ctrl+S opens review before posting. Drafts stay in this session.", area: &area,
	})
}

func (m *Model) reviewWrite(kind, target, value, description string) {
	m.dialogs.Push(&writeReview{Confirm: dialog.NewConfirm(description), kind: kind, target: target, value: value})
}

func (m *Model) acceptActionPicker(d *pickerDialog, value string) tea.Cmd {
	switch d.kind {
	case "transition":
		m.reviewWrite("transition", d.target, value, "Move "+cli.Text(d.target)+" using transition "+cli.Text(value)+"?\n\nThe current workflow will be checked again before submission.")
	case "assign-mode":
		switch value {
		case "search":
			m.text("Find a person", "find-user", d.target, "Search visible active people. You will choose an explicit account before review.")
		case "id":
			m.text("Account ID", "assign-id", d.target, "Enter an exact Jira account ID. Jira decides assignability.")
		case "clear":
			m.reviewWrite("assign", d.target, "", "Clear the assignee of "+cli.Text(d.target)+"?")
		case "me":
			if m.options.Actions.CurrentUser == nil {
				return nil
			}
			request := m.interaction
			return m.tasks.Start(task.Spec{Scope: "action-choices", Run: func() (any, error) {
				id, err := m.options.Actions.CurrentUser()
				return actionChoices{request: request, kind: "assign", target: d.target, choices: []choice{{label: "Authenticated account · " + cli.Text(id), value: id}}}, err
			}})
		}
	case "assign":
		m.reviewWrite("assign", d.target, value, "Assign "+cli.Text(d.target)+" to account "+cli.Text(value)+"?")
	}
	return nil
}

func (m *Model) acceptActionText(d *textDialog) tea.Cmd {
	switch d.kind {
	case "comment":
		m.drafts[d.target] = d.value()
		m.reviewWrite("comment", d.target, d.value(), "Post this new comment to "+cli.Text(d.target)+"?\n\n"+cli.TextBlock(d.value()))
	case "assign-id":
		m.reviewWrite("assign", d.target, strings.TrimSpace(d.value()), "Assign "+cli.Text(d.target)+" to account "+cli.Text(strings.TrimSpace(d.value()))+"?")
	case "find-user":
		if m.options.Actions.Users == nil {
			return nil
		}
		query := strings.TrimSpace(d.value())
		request := m.interaction
		return m.tasks.Start(task.Spec{Scope: "action-choices", Run: func() (any, error) {
			users, err := m.options.Actions.Users(query)
			choices := make([]choice, 0, len(users))
			for _, u := range users {
				choices = append(choices, choice{label: cli.Text(u.DisplayName.Value + " · " + u.AccountID.Value), description: "Visible active account; Jira decides assignability", value: u.AccountID.Value})
			}
			return actionChoices{request: request, kind: "assign", target: d.target, choices: choices}, err
		}})
	}
	return nil
}

func (m *Model) submitWrite(d *writeReview) tea.Cmd {
	if m.actionBusy || m.options.Actions.Write == nil {
		return nil
	}
	m.actionBusy = true
	operationTarget := d.target
	if d.targetID != "" {
		operationTarget += " · issue ID " + d.targetID
	}
	operation := m.startOperation(d.kind, operationTarget)
	m.notice = "Submitting " + d.kind + " for " + cli.Text(d.target) + "…"
	write, kind, target, value, targetID := m.options.Actions.Write, d.kind, d.target, d.value, d.targetID
	writeTarget := target
	if d.targetID != "" {
		writeTarget = d.targetID
	}
	return m.tasks.Start(task.Spec{Scope: "write", Run: func() (any, error) {
		receipt, err := write(kind, writeTarget, value)
		return actionResult{operation: operation, kind: kind, target: target, targetID: targetID, receipt: receipt}, err
	}})
}

func (m *Model) actionFinished(msg task.FinishedMsg) tea.Cmd {
	if msg.Scope == "action-choices" {
		if msg.Err != nil {
			m.notice = m.errorText(msg.Err)
			return nil
		}
		result, ok := msg.Result.(actionChoices)
		if !ok {
			m.notice = "Could not load choices."
			return nil
		}
		if result.request != m.interaction {
			m.notice = ""
			return nil
		}
		m.notice = ""
		if len(result.choices) == 0 {
			m.notice = "No available matches. Refine the search or inspect the issue in Jira."
			return nil
		}
		m.pick("Choose "+result.kind+" · "+cli.Text(result.target), result.kind, result.target, result.choices)
		return nil
	}
	m.actionBusy = false
	result, ok := msg.Result.(actionResult)
	if !ok {
		m.notice = "Action outcome unavailable; inspect Jira before repeating it."
		return nil
	}
	m.finishOperation(result.operation, result.receipt, msg.Err)
	if result.receipt.Applied {
		if result.kind == "summary" {
			delete(m.fieldDrafts, result.targetID+":summary")
		}
		if result.kind == "labels" {
			delete(m.labelDrafts, result.targetID)
		}
	}
	if msg.Err != nil {
		prefix := "Could not submit. "
		if result.receipt.Applied {
			prefix = "Jira acknowledged the action. "
		} else if result.receipt.Attempted {
			prefix = "Request attempted; inspect Jira before repeating it. "
		}
		m.notice = prefix + m.errorText(msg.Err)
		return nil
	}
	if !result.receipt.Applied {
		m.notice = "No Jira acknowledgement; inspect the issue before repeating the action."
		return nil
	}
	if result.kind == "comment" {
		delete(m.drafts, result.target)
	}
	m.notice = fmt.Sprintf("Jira acknowledged %s for %s. Refreshing…", result.kind, cli.Text(result.target))
	m.loading = false
	return m.load(false)
}

var _ dialog.Dialog = (*writeReview)(nil)
