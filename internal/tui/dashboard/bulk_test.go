package dashboard

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func transition(id, name, status string) jira.IssueTransition {
	return jira.IssueTransition{ID: jira.NullableOf(id), Name: jira.NullableOf(name), To: jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf(status)}), HasScreen: jira.NullableOf(false)}
}

func TestBulkTransitionBindsNativeIDsAndRetainsPartialOutcomes(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	var mu sync.Mutex
	writes := map[string]string{}
	m.options.Actions = Actions{
		Transitions: func(target string) ([]jira.IssueTransition, error) {
			id := "31"
			if target == "DEMO-2" {
				id = "41"
			}
			return []jira.IssueTransition{transition(id, "Finish", "Done")}, nil
		},
		Write: func(kind, target, value string) (core.IssueActionReceipt, error) {
			mu.Lock()
			writes[target] = value
			mu.Unlock()
			if kind != "transition" {
				return core.IssueActionReceipt{}, fmt.Errorf("wrong write kind %s", kind)
			}
			if target == "DEMO-2" {
				return core.IssueActionReceipt{Attempted: true}, errors.New("response unavailable")
			}
			return core.IssueActionReceipt{Attempted: true, Applied: true}, nil
		},
	}
	m.Update(key.Replay("ctrl+a"))
	applyCommand(m, press(m, 't'))
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("enter"))
	if len(writes) != 0 || m.dialogs.Active() {
		t.Fatal("bulk review did not default to cancellation")
	}
	applyCommand(m, press(m, 't'))
	m.Update(key.Replay("enter"))
	_, cmd := m.Update(key.Replay("y"))
	if len(writes) != 0 || cmd == nil {
		t.Fatal("bulk writes ran before the accepted command was scheduled")
	}
	press(m, 'X')
	if len(m.markedKeys()) != 2 {
		t.Fatal("selection changed under an admitted batch")
	}
	applyCommand(m, cmd)
	if !maps.Equal(writes, map[string]string{"DEMO-1": "31", "DEMO-2": "41"}) {
		t.Fatalf("batch did not retain per-issue workflow identities: %v", writes)
	}
	if m.lastBatch == nil || m.lastBatch.outcomes[0].key != "DEMO-1" || m.lastBatch.outcomes[1].key != "DEMO-2" {
		t.Fatal("worker completion order replaced review order")
	}
	if m.marks["DEMO-1"] || !m.marks["DEMO-2"] {
		t.Fatal("acknowledged and unconfirmed targets were not distinguished in selection")
	}
	if !strings.Contains(m.notice, "1 acknowledged") || !strings.Contains(m.notice, "1 unconfirmed") {
		t.Fatal("batch summary lost a partial outcome")
	}
	press(m, 'L')
	if !strings.Contains(m.View().Content, "batch complete") {
		t.Fatal("batch receipt was not retained in operation history")
	}
	press(m, 'b')
	view := m.View().Content
	if !strings.Contains(view, "DEMO-1 · acknowledged") || !strings.Contains(view, "DEMO-2 · unconfirmed") {
		t.Fatal("full per-issue outcomes were not reachable")
	}
}

func TestBulkChoicesExcludeAmbiguousAndUnsharedWorkflowEdges(t *testing.T) {
	keys := []string{"DEMO-1", "DEMO-2"}
	lists := [][]jira.IssueTransition{
		{transition("31", "Finish", "Done"), transition("11", "Start", "In Progress")},
		{transition("41", "Finish", "Done"), transition("42", "Finish", "Done"), transition("12", "Start", "In Progress")},
	}
	choices, ids := commonTransitions(keys, lists)
	if len(choices) != 1 || choices[0].label != "Start → In Progress" || !maps.Equal(ids[choices[0].value], map[string]string{"DEMO-1": "11", "DEMO-2": "12"}) {
		t.Fatal("ambiguous workflow edges were admitted")
	}
	lists[1][2].IsAvailable = jira.NullableOf(false)
	choices, _ = commonTransitions(keys, lists)
	if len(choices) != 0 {
		t.Fatal("an explicitly unavailable common transition was offered")
	}
}

func TestBulkCommentSnapshotSurvivesQueryChangeWithoutRestoringOldMarks(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	var mu sync.Mutex
	wrote := map[string]string{}
	m.options.Actions.Write = func(kind, target, value string) (core.IssueActionReceipt, error) {
		mu.Lock()
		defer mu.Unlock()
		wrote[target] = value
		return core.IssueActionReceipt{Attempted: true, Applied: true}, nil
	}
	m.Update(key.Replay("ctrl+a"))
	press(m, 'c')
	m.Update(tea.PasteMsg{Content: "Frozen group comment"})
	m.Update(key.Replay("ctrl+s"))
	_, cmd := m.Update(key.Replay("y"))
	press(m, '/')
	m.Update(key.Replay("ctrl+a"))
	// Replace the query through its editor rather than mutating batch state.
	m.input.SetValue("project = OTHER")
	m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if !maps.Equal(wrote, map[string]string{"DEMO-1": "Frozen group comment", "DEMO-2": "Frozen group comment"}) {
		t.Fatalf("editing the query changed an accepted batch: %v", wrote)
	}
	if len(m.markedKeys()) != 0 || m.query != "project = OTHER" {
		t.Fatal("late batch completion restored selection from another view")
	}
}

func TestCanceledBulkCommentKeepsDraftOnlyForItsTargetSet(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	m.options.Actions.Write = func(string, string, string) (core.IssueActionReceipt, error) {
		t.Fatal("canceled draft invoked a write")
		return core.IssueActionReceipt{}, nil
	}
	m.Update(key.Replay("ctrl+a"))
	press(m, 'c')
	m.Update(tea.PasteMsg{Content: "Keep this group draft"})
	m.Update(key.Replay("esc"))
	press(m, 'c')
	if m.dialogs.Top().(*bulkTextDialog).value() != "Keep this group draft" {
		t.Fatal("canceling a bulk form lost its draft")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("space"))
	press(m, 'c')
	if m.dialogs.Top().(*bulkTextDialog).value() != "" {
		t.Fatal("a previous group's draft was reused for different targets")
	}
}

func TestOperationHistoryIsBoundedAndNeverClaimsUnconfirmedWritesSucceeded(t *testing.T) {
	m := model(t)
	for range operationHistoryLimit + 3 {
		id := m.startOperation("comment", "DEMO-1")
		m.finishOperation(id, core.IssueActionReceipt{Attempted: true}, errors.New("connection ended"))
	}
	if len(m.operations) != operationHistoryLimit || m.operations[0].id != 4 {
		t.Fatal("operation history exceeded its session bound")
	}
	text := m.operationText(100)
	if !strings.Contains(text, "unconfirmed") || strings.Contains(text, "acknowledged · comment") {
		t.Fatal("unconfirmed operations were presented as successful")
	}
	id := m.startOperation("transition", "DEMO-1")
	m.finishOperation(id, core.IssueActionReceipt{Attempted: true, Applied: true}, errors.New("interrupted after acknowledgement"))
	if m.operations[len(m.operations)-1].state != "acknowledged" {
		t.Fatal("interruption erased a joined acknowledgement")
	}
}

func TestOperationHistoryFiltersWithoutLeakingKeys(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	first := m.startOperation("assign", "DEMO-1")
	m.finishOperation(first, core.IssueActionReceipt{Applied: true}, nil)
	second := m.startOperation("comment", "DEMO-2")
	m.finishOperation(second, core.IssueActionReceipt{Attempted: true}, errors.New("response unavailable"))
	press(m, 'L')
	press(m, '/')
	for _, r := range "unconfirmed" {
		press(m, r)
	}
	view := m.dialogs.Top().(*operationDialog).Content(100)
	if !strings.Contains(view, "DEMO-2") || strings.Contains(view, "DEMO-1") {
		t.Fatalf("history did not filter by receipt state:\n%s", view)
	}
	m.Update(key.Replay("esc"))
	if !m.dialogs.Active() || !strings.Contains(m.View().Content, "DEMO-1") {
		t.Fatal("escape from filtering did not restore history")
	}
}

func TestBulkDialogsKeepControlsAtSupportedSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
		m.options.Actions.Write = func(string, string, string) (core.IssueActionReceipt, error) { return core.IssueActionReceipt{}, nil }
		m.Update(size)
		m.reviewBulk("comment", []string{"DEMO-1", "DEMO-2"}, sameValues([]string{"DEMO-1", "DEMO-2"}, strings.Repeat("Long comment\n", 80)))
		view := m.View().Content
		if !strings.Contains(view, "y apply") || strings.Contains(view, "terminal too narrow") {
			t.Fatalf("review controls missing at %dx%d", size.Width, size.Height)
		}
		m.Update(key.Replay("esc"))
		m.openOperations()
		view = m.View().Content
		if !strings.Contains(view, "/ filter") || strings.Contains(view, "terminal too narrow") {
			t.Fatalf("history controls missing at %dx%d:\n%s", size.Width, size.Height, view)
		}
	}
}

func TestEarlierBatchRetainsPerIssueUncertainty(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	first := m.startOperation("bulk comment", "DEMO-1, DEMO-2")
	m.acceptBulkFinished(task.FinishedMsg{Result: bulkFinished{operation: first, result: bulkResult{kind: "comment", outcomes: []bulkOutcome{{key: "DEMO-1", receipt: core.IssueActionReceipt{Applied: true}}, {key: "DEMO-2", receipt: core.IssueActionReceipt{Attempted: true}}}}}})
	second := m.startOperation("bulk assign", "DEMO-3")
	m.acceptBulkFinished(task.FinishedMsg{Result: bulkFinished{operation: second, result: bulkResult{kind: "assign", outcomes: []bulkOutcome{{key: "DEMO-3", receipt: core.IssueActionReceipt{Applied: true}}}}}})
	press(m, 'L')
	press(m, 'h')
	m.Update(key.Replay("down"))
	m.Update(key.Replay("enter"))
	view := m.dialogs.Top().(*operationDialog).Content(100)
	if !strings.Contains(view, "DEMO-1 · acknowledged") || !strings.Contains(view, "DEMO-2 · unconfirmed") || strings.Contains(view, "DEMO-3") {
		t.Fatal("a later batch replaced earlier per-issue receipts")
	}
}

func TestStaleBulkChoicesClearOnlyTheirLoadingNotice(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	m.interaction = 2
	m.bulkNoticeRequest = 1
	m.notice = "Checking transitions for 1 marked issues…"
	stale := task.FinishedMsg{Result: bulkChoices{request: 1, keys: []string{"DEMO-1"}}}
	m.acceptBulkChoices(stale)
	if m.notice != "" || m.dialogs.Active() {
		t.Fatal("stale metadata left an active loading state")
	}
	m.bulkNoticeRequest = 2
	m.notice = "Checking transitions for 1 marked issues…"
	m.acceptBulkChoices(stale)
	if m.notice == "" {
		t.Fatal("stale completion cleared a newer request's notice")
	}
}
