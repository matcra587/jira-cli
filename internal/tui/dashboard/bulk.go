package dashboard

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/button"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/task"
	xsync "github.com/gechr/x/sync"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type bulkChoices struct {
	request     uint64
	keys        []string
	kind        string
	choices     []choice
	transitions map[string]map[string]string
}
type bulkReview struct {
	writeReview
	keys   []string
	values map[string]string
}

func (d *bulkReview) Footer() string { return "y apply · n/esc back" }
func (d *bulkReview) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.Confirm.Update(msg)
	return d, cmd, result
}

type bulkTextDialog struct {
	textDialog
	keys []string
}

func (d *bulkTextDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	_, cmd, result := d.textDialog.Update(msg)
	return d, cmd, result
}

type (
	bulkOutcome struct {
		key     string
		receipt core.IssueActionReceipt
		err     error
	}
	bulkResult struct {
		kind, query string
		outcomes    []bulkOutcome
	}
)

func (m *Model) canBulk(keys []string) bool {
	if m.actionBusy {
		m.notice = "A Jira action is still running."
		return false
	}
	if m.options.Actions.ReadOnly || m.options.Actions.Write == nil {
		m.notice = "Jira actions are unavailable in read-only mode."
		return false
	}
	if len(keys) == 0 {
		m.notice = "Mark issues with " + m.effectiveKey("space") + " before opening bulk actions."
		return false
	}
	return true
}

func (m *Model) pickBulk(title, kind string, keys []string, choices []choice, ids map[string]map[string]string) {
	m.pick(title, kind, "", choices)
	d := m.dialogs.Top().(*pickerDialog)
	d.keys = slices.Clone(keys)
	d.transitionIDs = ids
}

func (m *Model) openBulkActions() {
	keys := m.markedKeys()
	if !m.canBulk(keys) {
		return
	}
	m.pickBulk(fmt.Sprintf("Bulk actions · %d issues", len(keys)), "bulk-mode", keys, []choice{
		{label: "Transition selected issues", value: "transition"},
		{label: "Assign selected issues", value: "assign"},
		{label: "Comment on selected issues", value: "comment"},
	}, nil)
}

func (m *Model) openBulkAssign(keys []string) {
	if !m.canBulk(keys) {
		return
	}
	m.pickBulk(fmt.Sprintf("Assign · %d issues", len(keys)), "bulk-assign-mode", keys, []choice{
		{label: "Assign to me", value: "me"},
		{label: "Find a person", value: "search"},
		{label: "Use an account ID", value: "id"},
		{label: "Unassign", value: "clear"},
	}, nil)
}

func (m *Model) openBulkText(kind string, keys []string) {
	if !m.canBulk(keys) {
		return
	}
	d := &bulkTextDialog{keys: slices.Clone(keys), textDialog: textDialog{
		title: fmt.Sprintf("Bulk %s · %d issues", kind, len(keys)), kind: kind, line: input.NewLine("› ", ""),
		description: "The marked issue keys are fixed for this form. Review the complete target list before applying.",
	}}
	switch kind {
	case "comment":
		area := input.NewTextArea()
		area.CharLimit = 64 << 10
		if len(keys) == len(m.bulkDraftTargets) {
			same := true
			for _, key := range keys {
				if !m.bulkDraftTargets[key] {
					same = false
					break
				}
			}
			if same {
				area.SetValue(m.bulkDraft)
			}
		}
		area.Focus()
		d.area = &area
	case "assign-id":
		d.description = "Enter an exact Jira account ID for all marked issues. Jira decides assignability."
	case "find-user":
		d.description = "Search the first 50 visible user matches, then choose an explicit active account for the marked issues."
	}
	m.dialogs.Push(d)
}

func (m *Model) keepBulkDraft(d *bulkTextDialog) {
	if d.kind != "comment" {
		return
	}
	m.bulkDraft = d.value()
	m.bulkDraftTargets = make(map[string]bool, len(d.keys))
	for _, key := range d.keys {
		m.bulkDraftTargets[key] = true
	}
}

func (m *Model) reviewBulk(kind string, keys []string, values map[string]string) {
	keys = slices.Clone(keys)
	description := fmt.Sprintf("Apply %s to these %d issues?\n\n%s", kind, len(keys), cli.Text(strings.Join(keys, ", ")))
	switch kind {
	case "comment":
		description += "\n\nNew Markdown comment:\n" + cli.TextBlock(values[keys[0]])
	case "assign":
		value := values[keys[0]]
		if value == "" {
			value = "Unassigned"
		}
		description += "\n\nAssignee: " + cli.Text(value)
	default:
		for _, key := range keys {
			description += "\n" + cli.Text(key) + " → transition " + cli.Text(values[key])
		}
		description += "\n\nEvery workflow is checked again before its write."
	}
	focus := lipgloss.NewStyle().Transform(func(text string) string { return "[" + text + "]" })
	if cli.StyleEnabled() {
		focus = cli.ResolvedTheme().Blue.Reverse(true).Transform(func(text string) string { return "[" + text + "]" })
	}
	confirm := dialog.NewConfirmButtons(description,
		dialog.ConfirmButton{Button: button.Button{Label: "Cancel", Focused: focus}, Default: true, Keys: []string{"n", "N"}},
		dialog.ConfirmButton{Button: button.Button{Label: "Apply", Focused: focus}, Accept: true, Keys: []string{"y", "Y"}},
	)
	m.dialogs.Push(&bulkReview{writeReview: writeReview{Confirm: confirm, kind: kind, target: fmt.Sprintf("%d selected issues", len(keys))}, keys: keys, values: maps.Clone(values)})
}

func sameValues(keys []string, value string) map[string]string {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = value
	}
	return values
}

// Common transitions use native name/destination pairs and retain each issue's
// own ID. Equal-looking actions with multiple edges are excluded as ambiguous.
func commonTransitions(keys []string, lists [][]jira.IssueTransition) ([]choice, map[string]map[string]string) {
	type identity struct{ name, destination string }
	if len(keys) == 0 || len(keys) != len(lists) {
		return nil, nil
	}
	indexes := make([]map[identity]string, len(lists))
	for i, list := range lists {
		index := make(map[identity]string)
		for _, t := range list {
			if t.IsAvailable.Set && !t.IsAvailable.Value || !t.Name.Set || t.Name.Null || !t.To.Set || t.To.Null || !t.To.Value.Name.Set || t.To.Value.Name.Null {
				continue
			}
			signature := identity{name: t.Name.Value, destination: t.To.Value.Name.Value}
			if _, exists := index[signature]; exists {
				index[signature] = ""
			} else {
				index[signature] = t.ID.Value
			}
		}
		indexes[i] = index
	}
	choices := []choice{}
	mapping := make(map[string]map[string]string)
	if len(lists) == 0 {
		return choices, mapping
	}
	seen := make(map[identity]bool)
	for _, t := range lists[0] {
		signature := identity{name: t.Name.Value, destination: t.To.Value.Name.Value}
		if seen[signature] {
			continue
		}
		seen[signature] = true
		values := make(map[string]string, len(keys))
		complete := true
		for i, key := range keys {
			id := indexes[i][signature]
			if id == "" {
				complete = false
				break
			}
			values[key] = id
		}
		if !complete {
			continue
		}
		value := strconv.Itoa(len(choices))
		choices = append(choices, choice{label: cli.Text(signature.name + " → " + signature.destination), description: fmt.Sprintf("Available on all %d issues", len(keys)), value: value})
		mapping[value] = values
	}
	return choices, mapping
}

func (m *Model) loadBulkTransitions(keys []string) tea.Cmd {
	if !m.canBulk(keys) || m.options.Actions.Transitions == nil {
		return nil
	}
	keys = slices.Clone(keys)
	request := m.interaction
	read := m.options.Actions.Transitions
	m.bulkNoticeRequest = request
	m.notice = fmt.Sprintf("Checking transitions for %d marked issues…", len(keys))
	return m.tasks.Start(task.Spec{Scope: "bulk-choices", Run: func() (any, error) {
		lists := make([][]jira.IssueTransition, len(keys))
		failures := make([]error, len(keys))
		xsync.Parallel(4, len(keys), func(i int) { lists[i], failures[i] = read(keys[i]) })
		choices, ids := commonTransitions(keys, lists)
		return bulkChoices{request: request, keys: keys, kind: "bulk-transition", choices: choices, transitions: ids}, errors.Join(failures...)
	}})
}

func (m *Model) acceptBulkPicker(d *pickerDialog, value string) tea.Cmd {
	switch d.kind {
	case "bulk-mode":
		switch value {
		case "transition":
			return m.loadBulkTransitions(d.keys)
		case "assign":
			m.openBulkAssign(d.keys)
		case "comment":
			m.openBulkText("comment", d.keys)
		}
	case "bulk-transition":
		m.reviewBulk("transition", d.keys, d.transitionIDs[value])
	case "bulk-assign":
		m.reviewBulk("assign", d.keys, sameValues(d.keys, value))
	case "bulk-assign-mode":
		switch value {
		case "clear":
			m.reviewBulk("assign", d.keys, sameValues(d.keys, ""))
		case "id":
			m.openBulkText("assign-id", d.keys)
		case "search":
			m.openBulkText("find-user", d.keys)
		case "me":
			if m.options.Actions.CurrentUser == nil {
				return nil
			}
			request := m.interaction
			keys := slices.Clone(d.keys)
			read := m.options.Actions.CurrentUser
			return m.tasks.Start(task.Spec{Scope: "bulk-choices", Run: func() (any, error) {
				id, err := read()
				return bulkChoices{request: request, keys: keys, kind: "bulk-assign", choices: []choice{{label: "Authenticated account · " + cli.Text(id), value: id}}}, err
			}})
		}
	}
	return nil
}

func (m *Model) acceptBulkText(d *bulkTextDialog) tea.Cmd {
	switch d.kind {
	case "comment":
		m.reviewBulk("comment", d.keys, sameValues(d.keys, d.value()))
	case "assign-id":
		m.reviewBulk("assign", d.keys, sameValues(d.keys, strings.TrimSpace(d.value())))
	case "find-user":
		if m.options.Actions.Users == nil {
			return nil
		}
		request := m.interaction
		keys := slices.Clone(d.keys)
		query := strings.TrimSpace(d.value())
		read := m.options.Actions.Users
		return m.tasks.Start(task.Spec{Scope: "bulk-choices", Run: func() (any, error) {
			users, err := read(query)
			choices := make([]choice, 0, len(users))
			for _, u := range users {
				choices = append(choices, choice{label: cli.Text(u.DisplayName.Value + " · " + u.AccountID.Value), value: u.AccountID.Value})
			}
			return bulkChoices{request: request, keys: keys, kind: "bulk-assign", choices: choices}, err
		}})
	}
	return nil
}

func (m *Model) acceptBulkChoices(msg task.FinishedMsg) {
	result, ok := msg.Result.(bulkChoices)
	if !ok {
		return
	}
	if result.request != m.interaction {
		if m.bulkNoticeRequest == result.request && m.notice == fmt.Sprintf("Checking transitions for %d marked issues…", len(result.keys)) {
			m.notice = ""
		}
		return
	}
	if msg.Err != nil {
		m.notice = m.errorText(msg.Err)
		return
	}
	if len(result.choices) == 0 {
		m.notice = "No common unambiguous choice was returned for all marked issues. Use individual actions or refine the selection."
		return
	}
	m.notice = ""
	m.pickBulk(fmt.Sprintf("Choose action · %d issues", len(result.keys)), result.kind, result.keys, result.choices, result.transitions)
}

func (m *Model) submitBulk(d *bulkReview) tea.Cmd {
	if !m.canBulk(d.keys) {
		return nil
	}
	for _, key := range d.keys {
		if _, ok := d.values[key]; !ok {
			m.notice = "Review has no action value for one target; nothing was submitted."
			return nil
		}
	}
	keys, values, kind, query := slices.Clone(d.keys), maps.Clone(d.values), d.kind, m.query
	m.actionBusy = true
	m.notice = fmt.Sprintf("Applying %s to %d marked issues…", kind, len(keys))
	operation := m.startOperation("bulk "+kind, strings.Join(keys, ", "))
	write := m.options.Actions.Write
	return m.tasks.Start(task.Spec{Scope: "bulk-write", Run: func() (any, error) {
		result := bulkResult{kind: kind, query: query, outcomes: make([]bulkOutcome, len(keys))}
		xsync.Parallel(4, len(keys), func(i int) {
			receipt, err := write(kind, keys[i], values[keys[i]])
			result.outcomes[i] = bulkOutcome{key: keys[i], receipt: receipt, err: err}
		})
		return bulkFinished{result: result, operation: operation}, nil
	}})
}

type bulkFinished struct {
	result    bulkResult
	operation uint64
}

func (m *Model) acceptBulkFinished(msg task.FinishedMsg) tea.Cmd {
	m.actionBusy = false
	finished, ok := msg.Result.(bulkFinished)
	if !ok {
		m.notice = "Batch outcome unavailable; inspect every target before repeating it."
		return nil
	}
	result := finished.result
	m.lastBatch = &result
	acknowledged, unconfirmed, notAttempted := 0, 0, 0
	for _, outcome := range result.outcomes {
		switch {
		case outcome.receipt.Applied:
			acknowledged++
			if m.query == result.query {
				delete(m.marks, outcome.key)
			}
		case outcome.receipt.Attempted:
			unconfirmed++
		default:
			notAttempted++
		}
	}
	summary := fmt.Sprintf("Bulk %s: %d acknowledged · %d unconfirmed · %d not attempted. %s opens history and batch outcomes.", result.kind, acknowledged, unconfirmed, notAttempted, m.effectiveKey("L"))
	m.notice = summary
	if result.kind == "comment" && acknowledged == len(result.outcomes) {
		m.bulkDraft = ""
		m.bulkDraftTargets = nil
	}
	m.finishOperation(finished.operation, core.IssueActionReceipt{Attempted: acknowledged+unconfirmed > 0, Applied: acknowledged == len(result.outcomes)}, nil)
	for i := range m.operations {
		if m.operations[i].id == finished.operation {
			m.operations[i].batch = &result
			m.operations[i].state = "batch complete"
			m.operations[i].detail = summary
		}
	}
	return nil
}

func (m *Model) batchResultText(result *bulkResult, width int, query string) string {
	if result == nil {
		return "No batch has completed in this session."
	}
	lines := []string{"Latest batch · " + result.kind, "Unconfirmed attempts need inspection before repeating. Acknowledgements are not independent readbacks.", ""}
	for _, outcome := range result.outcomes {
		state := "not attempted"
		if outcome.receipt.Applied {
			state = "acknowledged"
		} else if outcome.receipt.Attempted {
			state = "unconfirmed"
		}
		if !strings.Contains(strings.ToLower(outcome.key+" "+state), strings.ToLower(query)) {
			continue
		}
		lines = append(lines, cli.Text(outcome.key)+" · "+state)
		if outcome.err != nil {
			lines = append(lines, m.errorText(outcome.err))
		}
		lines = append(lines, "")
	}
	return strings.Join(layout.WrapLines(strings.Join(lines, "\n"), max(1, width)), "\n")
}
