package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Clock commands are driven explicitly so these tests never sleep or poll.
func refreshModel(t *testing.T, beans ...jira.IssueBean) *Model {
	t.Helper()
	m := model(t, beans...)
	m.refresh.running = true
	m.preferences["tui_refresh_on_focus"] = true
	m.preferences["tui_change_markers"] = true
	m.clock = func(time.Duration, uint64) (tea.Cmd, context.CancelFunc) { return nil, func() {} }
	return m
}

func TestRefreshClockCancellationAndGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	command, stop := newRefreshClock(ctx)(time.Hour, 7)
	stop()
	if command() != nil {
		t.Fatal("canceled clock emitted a tick")
	}
	command, stop = newRefreshClock(ctx)(time.Hour, 8)
	defer stop()
	cancel()
	if command() != nil {
		t.Fatal("clock outlived its runtime context")
	}
	m := refreshModel(t, issue("DEMO-1", "Original"))
	starts, stops := 0, 0
	m.clock = func(time.Duration, uint64) (tea.Cmd, context.CancelFunc) {
		starts++
		return nil, func() { stops++ }
	}
	m.Update(nil)
	generation := m.refresh.generation
	m.Update(nil)
	if starts != 1 || stops != 0 {
		t.Fatal("ordinary input restarted the active clock")
	}
	m.Update(tea.BlurMsg{})
	m.Update(refreshTick{generation: generation})
	if starts != 1 || stops != 1 || m.loading || m.refresh.pending != refreshNone {
		t.Fatal("blur did not cancel and fence the old tick")
	}
}

func TestRefreshDefersInputAndCoalescesFocusAndTicks(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	calls := 0
	m.search = func(string, string, int) (envelope.IssueListOutput, error) {
		calls++
		return envelope.IssueListOutput{Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Changed"))}, Pagination: envelope.CursorPagination{IsLast: true}}, nil
	}
	press(m, '/')
	m.input.SetValue("unsubmitted JQL")
	m.Update(refreshTick{generation: m.refresh.generation})
	m.Update(tea.BlurMsg{})
	m.Update(tea.FocusMsg{})
	m.Update(refreshTick{generation: m.refresh.generation})
	if calls != 0 || m.loading || m.input.Value() != "unsubmitted JQL" {
		t.Fatal("automatic refresh interfered with query editing")
	}
	_, command := m.Update(key.Replay("esc"))
	applyCommand(m, command)
	if calls != 1 || m.selected().Fields.Summary.Value != "Changed" || m.input.Value() != "unsubmitted JQL" {
		t.Fatal("closing input did not admit exactly one pending refresh")
	}
}

func TestRefreshHoldsResultsUntilInputClosesAndFencesWrites(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprintf("write=%t", write), func(t *testing.T) {
			m := refreshModel(t, issue("DEMO-1", "Original"))
			m.requestRefresh(refreshAutomatic)
			command := m.flushRefresh()
			if command == nil || !m.loading {
				t.Fatal("refresh did not start")
			}
			// Capture the admitted response before opening input.
			result := task.FinishedMsg{Scope: listScope, Gen: m.tasks.Generation(listScope), Result: envelope.IssueListOutput{
				Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Before write"))}, Pagination: envelope.CursorPagination{IsLast: true},
			}}
			press(m, '/')
			m.Update(result)
			if m.selected().Fields.Summary.Value != "Original" || m.refresh.held == nil {
				t.Fatal("in-flight result replaced rows beneath an editor")
			}
			if write {
				m.fenceRefreshForWrite()
				m.actionBusy = true
			}
			m.Update(key.Replay("esc"))
			if write {
				m.Update(result)
				if m.selected().Fields.Summary.Value != "Original" || m.refresh.held != nil {
					t.Fatal("pre-write observation crossed the write fence")
				}
			} else if m.selected().Fields.Summary.Value != "Before write" || m.loading {
				t.Fatal("closing editor did not publish the held observation")
			}
		})
	}
}

func TestRefreshPauseAllowsManualAndFocusIsIndependentOfTimer(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	m.options.SettingValues = map[string]string{"tui_refresh_interval": "0"}
	press(m, 'R')
	m.Update(tea.BlurMsg{})
	m.Update(tea.FocusMsg{})
	if m.loading || m.refresh.pending != refreshNone {
		t.Fatal("paused focus triggered a read")
	}
	applyCommand(m, press(m, 'r'))
	if !m.refresh.paused || m.err != nil {
		t.Fatal("manual refresh changed the pause state")
	}
	applyCommand(m, press(m, 'R'))
	m.Update(tea.BlurMsg{})
	_, command := m.Update(tea.FocusMsg{})
	if command == nil || !m.loading {
		t.Fatal("disabling the timer disabled the independent focus trigger")
	}
	applyCommand(m, command)
	m.preferences["tui_refresh_on_focus"] = false
	m.Update(tea.BlurMsg{})
	m.Update(tea.FocusMsg{})
	if m.loading {
		t.Fatal("disabled focus trigger performed a read")
	}
}

func TestRefreshRechecksEligibilityAfterDeferredInput(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	press(m, '/')
	m.Update(refreshTick{generation: m.refresh.generation})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	m.Update(key.Replay("esc"))
	if m.loading {
		t.Fatal("deferred tick started a read after the terminal became unusable")
	}
	_, command := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if command == nil || !m.loading {
		t.Fatal("returning to a usable terminal lost the pending refresh")
	}
}

func TestRefreshManualSupersedesPausedHeldObservation(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	m.requestRefresh(refreshAutomatic)
	m.flushRefresh()
	press(m, 'R')
	old := task.FinishedMsg{Scope: listScope, Gen: m.tasks.Generation(listScope), Result: envelope.IssueListOutput{
		Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Paused observation"))}, Pagination: envelope.CursorPagination{IsLast: true},
	}}
	m.Update(old)
	if m.refresh.held == nil {
		t.Fatal("fixture did not hold a paused automatic response")
	}
	calls := 0
	m.search = func(string, string, int) (envelope.IssueListOutput, error) {
		calls++
		return envelope.IssueListOutput{Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Fresh manual observation"))}, Pagination: envelope.CursorPagination{IsLast: true}}, nil
	}
	applyCommand(m, press(m, 'r'))
	m.Update(old)
	if calls != 1 || m.selected().Fields.Summary.Value != "Fresh manual observation" || !m.refresh.paused || m.refresh.held != nil {
		t.Fatal("manual refresh was blocked by or applied a paused automatic observation")
	}
}

func TestRefreshDefersBackgroundResultWhileBlurred(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	m.requestRefresh(refreshAutomatic)
	m.flushRefresh()
	m.Update(tea.BlurMsg{})
	m.Update(task.FinishedMsg{Scope: listScope, Gen: m.tasks.Generation(listScope), Result: envelope.IssueListOutput{
		Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Changed"))}, Pagination: envelope.CursorPagination{IsLast: true},
	}})
	if m.refresh.held == nil || m.selected().Fields.Summary.Value != "Original" {
		t.Fatal("background result published and started detail reads while blurred")
	}
	m.Update(tea.FocusMsg{})
	if m.refresh.held != nil || m.selected().Fields.Summary.Value != "Changed" {
		t.Fatal("returning to the workspace lost the held background observation")
	}
}

func TestRefreshUsesLoadedWindowAndPreservesSelectionAndDrafts(t *testing.T) {
	beans := make([]jira.IssueBean, 75)
	for i := range beans {
		beans[i] = issue(fmtKey(i), "Original")
	}
	m := New(t.Context(), fixtureClient{search: func(_ context.Context, _, cursor string, _ int) (core.IssueSearchRead, error) {
		if cursor == "" {
			return page(beans[:50], false, "second"), nil
		}
		return page(beans[50:], true, ""), nil
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	applyCommand(m, m.load(true))
	m.refresh.running = true
	m.preferences["tui_refresh_on_focus"] = true
	m.clock = func(time.Duration, uint64) (tea.Cmd, context.CancelFunc) { return nil, func() {} }
	m.rows.SetCursor(60)
	m.drafts["DEMO-61"] = "unsubmitted comment"
	m.marks = map[string]bool{"DEMO-61": true}
	m.search = func(_, cursor string, limit int) (envelope.IssueListOutput, error) {
		if cursor != "" || limit != 75 {
			t.Fatalf("refresh shrank or drained the loaded window: cursor %q, limit %d", cursor, limit)
		}
		out := make([]envelope.IssueView, len(beans))
		for i, bean := range beans {
			out[len(beans)-1-i] = envelope.IssueViewFromBean(bean)
		}
		return envelope.IssueListOutput{Issues: out, Pagination: envelope.CursorPagination{IsLast: true}}, nil
	}
	m.Update(tea.BlurMsg{})
	_, command := m.Update(tea.FocusMsg{})
	applyCommand(m, command)
	if m.selectedKey() != "DEMO-61" || len(m.issues) != 75 || !m.marks["DEMO-61"] || m.drafts["DEMO-61"] != "unsubmitted comment" {
		t.Fatal("background refresh lost the selected identity or local work")
	}
}

func TestRefreshPreservesPreviewScrollDuringAndAfterDetailReplacement(t *testing.T) {
	bean := issue("DEMO-1", "Original")
	// Marshal the authored text through JSON rather than relying on Go string
	// escaping, which differs for non-JSON escapes.
	text, err := json.Marshal(strings.Repeat("Long preview paragraph. ", 600))
	if err != nil {
		t.Fatal(err)
	}
	content := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":` + string(text) + `}]}]}`
	bean.Fields.Value.Description = jira.NullableOf(json.RawMessage(content))
	m := refreshModel(t, bean)
	m.detailIssue = &bean
	m.detailLoading = false
	m.syncPreview()
	m.preview.SetYOffset(10)
	before := m.preview.YOffset()
	if before == 0 {
		t.Fatal("fixture did not produce a scrollable preview")
	}
	m.requestRefresh(refreshAutomatic)
	m.flushRefresh()
	_, detail := m.Update(task.FinishedMsg{Scope: listScope, Gen: m.tasks.Generation(listScope), Result: envelope.IssueListOutput{
		Issues: m.issues, Pagination: envelope.CursorPagination{IsLast: true},
	}})
	if m.preview.YOffset() != before || m.detailIssue == nil {
		t.Fatal("background list publication cleared the current preview")
	}
	// Complete the replacement detail with the same long body. The fixture's
	// default short body is deliberately bypassed to test offset preservation.
	if detail == nil {
		t.Fatal("background refresh did not schedule replacement details")
	}
	m.Update(task.FinishedMsg{Scope: detailScope, Gen: m.tasks.Generation(detailScope), Result: detailResult{key: "DEMO-1", issue: &bean}})
	if m.preview.YOffset() != before {
		t.Fatal("replacement details reset the preview scroll position")
	}
}

func TestChangedRowsTrackObservedValuesAndDeliberateViewing(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"), issue("DEMO-2", "Original"))
	m.refresh.observed = false
	m.observeChangedRows(m.issues)
	if len(m.refresh.markers) != 0 {
		t.Fatal("initial observation marked every row new")
	}
	changed := []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-1", "Changed")), m.issues[1], envelope.IssueViewFromBean(issue("DEMO-3", "New"))}
	m.observeChangedRows(changed)
	m.issues = changed
	m.rebuildRows()
	if m.refresh.markers["DEMO-1"] != rowUpdated || m.refresh.markers["DEMO-3"] != rowNew || len(m.refresh.markers) != 2 {
		t.Fatal("changed/new observation was not distinguished")
	}
	m.observeChangedRows(changed)
	if len(m.refresh.markers) != 2 {
		t.Fatal("unchanged refresh erased unacknowledged observations")
	}
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 3, Y: 8})
	if _, exists := m.refresh.markers[m.selectedKey()]; exists {
		t.Fatal("mouse navigation did not acknowledge the viewed row")
	}
	m.rows.SetCursor(0)
	m.Update(key.Replay("enter"))
	if len(m.refresh.markers) != 0 {
		t.Fatal("opening the changed issue did not acknowledge it")
	}
	m.refresh.markers["DEMO-2"] = rowUpdated
	m.resetQuery()
	if len(m.refresh.markers) != 0 || m.refresh.observed {
		t.Fatal("query change leaked prior observations")
	}
}

func TestRefreshErrorsRetainObservedRowsAndRuntimeRejectsOldTicks(t *testing.T) {
	m := refreshModel(t, issue("DEMO-1", "Original"))
	m.refresh.markers["DEMO-1"] = rowUpdated
	m.search = func(string, string, int) (envelope.IssueListOutput, error) {
		return envelope.IssueListOutput{}, errors.New("offline")
	}
	applyCommand(m, press(m, 'r'))
	if m.err == nil || len(m.issues) != 1 || m.refresh.markers["DEMO-1"] != rowUpdated {
		t.Fatal("failed refresh erased the last observation")
	}
	next := refreshModel(t, issue("DEMO-2", "New runtime"))
	oldTick := runtimeCommand(m, func() tea.Msg { return refreshTick{generation: next.refresh.generation} })()
	session := WithRuntime(next).(*runtimeModel)
	session.Update(oldTick)
	if next.loading || next.selectedKey() != "DEMO-2" {
		t.Fatal("a previous runtime's clock entered the replacement")
	}
}

func TestRefreshPageBudgetStopsSparseEndpoints(t *testing.T) {
	calls := 0
	search := dashboardSearcher(t.Context(), fixtureClient{search: func(_ context.Context, _, _ string, _ int) (core.IssueSearchRead, error) {
		calls++
		return page([]jira.IssueBean{issue(fmtKey(calls), "Sparse")}, false, fmt.Sprintf("cursor-%d", calls)), nil
	}}, false)
	result, err := search(myWorkQuery, "", 5000)
	if err != nil || calls != 50 || !result.Truncated || result.TruncatedReason != envelope.TruncatedReasonMaxPages {
		t.Fatalf("unattended walk did not stop at the request budget: calls=%d result=%+v err=%v", calls, result.Pagination, err)
	}
	m := refreshModel(t, issue("DEMO-1", "Retained observation"))
	m.refresh.markers["DEMO-1"] = rowUpdated
	m.search = func(string, string, int) (envelope.IssueListOutput, error) { return result, nil }
	m.requestRefresh(refreshAutomatic)
	applyCommand(m, m.flushRefresh())
	if len(m.issues) != 1 || m.selectedKey() != "DEMO-1" || m.err == nil || m.refresh.markers["DEMO-1"] != rowUpdated {
		t.Fatal("incomplete background read erased the prior observation")
	}
}

func TestChangedRowsAcknowledgeExplicitPreviewAndIssueSwitcher(t *testing.T) {
	for _, shortcut := range []string{"ctrl+w", "switcher", "]"} {
		t.Run(shortcut, func(t *testing.T) {
			m := refreshModel(t, issue("DEMO-1", "Changed"))
			m.refresh.markers["DEMO-1"] = rowUpdated
			if shortcut == "switcher" {
				m.openIssues()
				_, command := m.Update(key.Replay("enter"))
				applyCommand(m, command)
			} else {
				m.Update(key.Replay(shortcut))
			}
			if len(m.refresh.markers) != 0 {
				t.Fatal("explicit issue viewing retained its change marker")
			}
		})
	}
}
