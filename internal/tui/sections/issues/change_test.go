package issues

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/core"
)

// upIssue is mkIssue plus an updated timestamp, which change detection keys on.
func upIssue(key, status, summary, updated string) *jira.Issue {
	i := mkIssue(key, status, summary)
	u := updated
	i.Fields.Updated = &u
	return i
}

// changeModel builds a sized issues section (Init wires the layout; the
// fetch cmd it returns is unused — land() feeds results directly).
func changeModel(t *testing.T) *Model {
	t.Helper()
	ctx := newTestCtx(fakeServices{issue: fakeIssueSvc{}})
	m := New(ctx).(*Model)
	m.Init(ctx)
	return m
}

// land applies a first-page fetch result the way the task system does.
func land(m *Model, issues ...*jira.Issue) {
	m.handleTask(core.TaskFinishedMsg{Scope: m.fetchScope(), Result: fetchResult{issues: issues}})
}

func TestFirstFetchMarksNothing(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "2026-06-01T10:00:00.000+0000"))
	if len(m.changed) != 0 {
		t.Errorf("first fetch marked %v, want none", m.changed)
	}
}

func TestRefreshMarksNewAndUpdatedRows(t *testing.T) {
	m := changeModel(t)
	land(
		m,
		upIssue("JCT-1", "To Do", "a", "2026-06-01T10:00:00.000+0000"),
		upIssue("JCT-2", "To Do", "b", "2026-06-01T10:00:00.000+0000"),
	)
	land(
		m,
		upIssue("JCT-1", "To Do", "a", "2026-06-01T10:00:00.000+0000"),       // unchanged
		upIssue("JCT-2", "In Progress", "b", "2026-06-02T09:00:00.000+0000"), // updated
		upIssue("JCT-3", "To Do", "c", "2026-06-02T09:30:00.000+0000"),       // new
	)
	if m.changed["JCT-1"] != changeNone {
		t.Errorf("unchanged issue marked %v", m.changed["JCT-1"])
	}
	if m.changed["JCT-2"] != changeUpdated {
		t.Errorf("JCT-2 mark = %v, want updated", m.changed["JCT-2"])
	}
	if m.changed["JCT-3"] != changeNew {
		t.Errorf("JCT-3 mark = %v, want new", m.changed["JCT-3"])
	}
}

func TestChangeMarkRendersDotUntilViewed(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "1"))
	land(
		m,
		upIssue("JCT-1", "To Do", "a", "1"),
		upIssue("JCT-2", "To Do", "b", "2"),
	)
	rows := strings.Split(ansi.Strip(m.list.View()), "\n")
	var kan2 string
	for _, row := range rows {
		if strings.Contains(row, "JCT-2") {
			kan2 = row
		}
	}
	if !strings.HasPrefix(kan2, "●") {
		t.Fatalf("new row should carry the change dot, got %q", kan2)
	}

	// Navigating onto the row counts as viewing it: the dot clears. Find the
	// row by key rather than assuming its index.
	idx := -1
	for i, iss := range m.shown {
		if issueKey(iss) == "JCT-2" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("JCT-2 not in shown rows")
	}
	for i := 0; i < idx; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	rows = strings.Split(ansi.Strip(m.list.View()), "\n")
	for _, row := range rows {
		if strings.Contains(row, "JCT-2") && strings.HasPrefix(row, "●") {
			t.Errorf("viewed row still carries the change dot: %q", row)
		}
	}
	if m.changed["JCT-2"] != changeNone {
		t.Errorf("viewing did not clear the mark: %v", m.changed["JCT-2"])
	}
}

func TestFetchMoreRegistersWithoutMarking(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "1"))
	m.handleTask(core.TaskFinishedMsg{Scope: m.fetchScope(), Result: fetchMoreResult{
		issues: []*jira.Issue{upIssue("JCT-2", "To Do", "b", "2")},
	}})
	if len(m.changed) != 0 {
		t.Errorf("pagination marked %v, want none (it loads old issues, not changes)", m.changed)
	}
	// A later refresh must not call the paged-in issue "new".
	land(
		m,
		upIssue("JCT-1", "To Do", "a", "1"),
		upIssue("JCT-2", "To Do", "b", "2"),
	)
	if m.changed["JCT-2"] != changeNone {
		t.Errorf("paged-in issue marked %v on refresh", m.changed["JCT-2"])
	}
}

func TestBulkMarkOutranksChangeDot(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "1"))
	land(m, upIssue("JCT-1", "To Do", "a", "1"), upIssue("JCT-2", "To Do", "b", "2"))
	m.marks = map[string]bool{"JCT-2": true}
	m.applyFilter()
	rows := ansi.Strip(m.list.View())
	for row := range strings.SplitSeq(rows, "\n") {
		if strings.Contains(row, "JCT-2") && !strings.HasPrefix(row, "✓") {
			t.Errorf("bulk selection mark must outrank the change dot: %q", row)
		}
	}
}

func TestRefreshReorderKeepsUnviewedDot(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "1"))
	// The refresh inserts a new issue ABOVE the old selection: the stale
	// cursor index briefly points at the new row, which must keep its dot.
	land(
		m,
		upIssue("JCT-9", "To Do", "brand new on top", "9"),
		upIssue("JCT-1", "To Do", "a", "1"),
	)
	if m.changed["JCT-9"] != changeNew {
		t.Errorf("refresh cleared the unviewed dot: %v", m.changed)
	}
}

func TestQueryChangeResetsChangeTracking(t *testing.T) {
	m := changeModel(t)
	m.lastJQL = "old query"
	m.seen = map[string]string{"JCT-1": "1"}
	m.markChanged("JCT-1", changeUpdated)
	m.runFetch("new query")
	if m.seen != nil || len(m.changed) != 0 {
		t.Errorf("new query must reset the snapshot: seen=%v changed=%v", m.seen, m.changed)
	}
}

func TestPagedInIssueNotNewWhenRerankedOntoPageOne(t *testing.T) {
	m := changeModel(t)
	land(m, upIssue("JCT-1", "To Do", "a", "1"))
	m.handleTask(core.TaskFinishedMsg{Scope: m.fetchScope(), Result: fetchMoreResult{
		issues: []*jira.Issue{upIssue("JCT-2", "To Do", "b", "2")},
	}})
	// JCT-2 re-ranks onto page one unchanged: previously seen, not "new".
	land(
		m,
		upIssue("JCT-2", "To Do", "b", "2"),
		upIssue("JCT-1", "To Do", "a", "1"),
	)
	if m.changed["JCT-2"] != changeNone {
		t.Errorf("re-ranked paged-in issue marked %v", m.changed["JCT-2"])
	}
}
