package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

const operationHistoryLimit = 100

type operation struct {
	id                          uint64
	kind, target, state, detail string
	started, finished           time.Time
	batch                       *bulkResult
}

func (m *Model) startOperation(kind, target string) uint64 {
	m.nextOperation++
	m.operations = append(m.operations, operation{id: m.nextOperation, kind: kind, target: target, state: "pending", started: time.Now().UTC()})
	if len(m.operations) > operationHistoryLimit {
		m.operations = m.operations[len(m.operations)-operationHistoryLimit:]
	}
	return m.nextOperation
}

func (m *Model) finishOperation(id uint64, receipt core.IssueActionReceipt, err error) {
	for i := range m.operations {
		entry := &m.operations[i]
		if entry.id != id {
			continue
		}
		entry.finished = time.Now().UTC()
		switch {
		case receipt.Applied:
			entry.state = "acknowledged"
		case receipt.Attempted:
			entry.state = "unconfirmed"
		default:
			entry.state = "not attempted"
		}
		if err != nil {
			entry.detail = m.errorText(err)
		}
		if receipt.CommentID != "" {
			entry.detail = "Comment " + cli.Text(receipt.CommentID) + " · " + entry.detail
		}
		return
	}
}

func (m *Model) operationText(width int) string { return m.filteredOperations(width, "") }

func (m *Model) filteredOperations(width int, query string) string {
	if len(m.operations) == 0 {
		return strings.Join(layout.WrapLines("No Jira actions in this session.", max(1, width)), "\n")
	}
	lines := []string{}
	if m.lastBatch != nil {
		lines = append(lines, fmt.Sprintf("Latest batch: %d issue outcomes retained · b opens the full result.", len(m.lastBatch.outcomes)), "")
	}
	lines = append(lines, dim("Acknowledgements are not independent readbacks. Unconfirmed writes need inspection before repeating."), "")
	for i := len(m.operations) - 1; i >= 0; i-- {
		entry := m.operations[i]
		if !strings.Contains(strings.ToLower(entry.kind+" "+entry.target+" "+entry.state+" "+entry.detail), strings.ToLower(query)) {
			continue
		}
		text := fmt.Sprintf("%s · %s · %s · %s", entry.started.Format(time.RFC3339), entry.state, entry.kind, cli.Text(entry.target))
		if !entry.finished.IsZero() {
			text += " · " + entry.finished.Sub(entry.started).Round(time.Millisecond).String()
		}
		lines = append(lines, accent(text))
		if entry.detail != "" {
			lines = append(lines, cli.Text(entry.detail))
		}
		lines = append(lines, "")
	}
	return strings.Join(layout.WrapLines(strings.Join(lines, "\n"), max(1, width)), "\n")
}

type operationDialog struct {
	render       func(int, string) string
	filter       input.Line
	searching    bool
	earlier      bool
	canOpenBatch func() bool
}

func (*operationDialog) Title() string     { return "Operation history" }
func (*operationDialog) Hints() []key.Hint { return nil }
func (d *operationDialog) Footer() string {
	if d.searching {
		return "enter results · esc clear"
	}
	if d.canOpenBatch != nil && d.canOpenBatch() {
		return "b latest · h all · esc"
	}
	return "/ filter · esc"
}

func (d *operationDialog) Content(width int) string {
	d.filter.SetWidth(max(1, width-2))
	return d.filter.View() + "\n\n" + d.render(width, strings.TrimSpace(d.filter.Value()))
}

func (d *operationDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if d.searching {
		if press, ok := msg.(tea.KeyPressMsg); ok {
			switch press.String() {
			case "enter":
				d.searching = false
				d.filter.Blur()
				return d, nil, dialog.ResultNone
			case "esc":
				d.searching = false
				d.filter.SetValue("")
				d.filter.Blur()
				return d, nil, dialog.ResultNone
			}
		}
		return d, d.filter.Update(msg), dialog.ResultNone
	}
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "/":
			d.searching = true
			d.filter.Focus()
			return d, nil, dialog.ResultNone
		case "h":
			d.earlier = true
			return d, nil, dialog.ResultSubmit
		case "b":
			if d.canOpenBatch != nil && d.canOpenBatch() {
				return d, nil, dialog.ResultSubmit
			}
		case "esc", "enter", "q":
			return d, nil, dialog.ResultClose
		}
	}
	return d, nil, dialog.ResultNone
}

func (m *Model) openOperations() {
	m.dialogs.Push(&operationDialog{render: m.filteredOperations, filter: input.NewLine("/ ", ""), canOpenBatch: func() bool { return m.lastBatch != nil }})
}

func (m *Model) openBatchHistory() {
	choices := []choice{}
	for i := len(m.operations) - 1; i >= 0; i-- {
		entry := m.operations[i]
		if entry.batch != nil {
			choices = append(choices, choice{label: fmt.Sprintf("%s · %s · %d issues", entry.started.Format(time.RFC3339), entry.kind, len(entry.batch.outcomes)), value: fmt.Sprint(entry.id)})
		}
	}
	m.pick("Batch history", "batch-history", "", choices)
}

func (m *Model) openBatchResult(result *bulkResult) {
	m.dialogs.Push(&operationDialog{render: func(width int, query string) string { return m.batchResultText(result, width, query) }, filter: input.NewLine("/ ", "")})
}
