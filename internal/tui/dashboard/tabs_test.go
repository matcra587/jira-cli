package dashboard

import (
	"strings"
	"testing"

	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestTabCyclesThroughEmptySearchWithoutStartingAnEditor(t *testing.T) {
	m := model(t, issue("DEMO-1", "Assigned work"))
	for _, name := range []string{"Search", hierarchyTab, "My work", "Search"} {
		m.Update(key.Replay("tab"))
		if m.tabs.ActiveItem() != name || m.editing {
			t.Fatalf("Tab trapped navigation: view=%q editing=%v want=%q", m.tabs.ActiveItem(), m.editing, name)
		}
	}
	for _, name := range []string{"My work", hierarchyTab, "Search"} {
		m.Update(key.Replay("shift+tab"))
		if m.tabs.ActiveItem() != name || m.editing {
			t.Fatalf("Shift+Tab trapped navigation: view=%q editing=%v want=%q", m.tabs.ActiveItem(), m.editing, name)
		}
	}
}

func TestEmptySearchWaitsForExplicitQueryAndNeverRequestsEmptyJQL(t *testing.T) {
	m := model(t, issue("DEMO-1", "Assigned work"))
	calls := 0
	m.search = func(query, _ string) (envelope.IssueListOutput, error) {
		calls++
		if query == "" {
			t.Fatal("idle Search sent an empty Jira query")
		}
		return envelope.IssueListOutput{}, nil
	}
	m.Update(key.Replay("tab"))
	if m.query != "" || m.selected() != nil || m.loading || !m.isLast {
		t.Fatal("empty Search inherited the previous view's rows or query")
	}
	if !strings.Contains(m.View().Content, "Enter to write JQL") {
		t.Fatal("Search did not explain how to start a query")
	}
	if cmd := m.load(false); cmd != nil {
		cmd()
	}
	if cmd := press(m, 'r'); cmd != nil {
		cmd()
	}
	if calls != 0 {
		t.Fatal("idle Search performed I/O")
	}
	m.Update(key.Replay("enter"))
	if !m.editing || m.input.Value() != "" {
		t.Fatal("Enter did not explicitly start an empty query editor")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("tab"))
	if !m.hierarchyActive() || m.editing {
		t.Fatal("canceling query editing still trapped tab navigation")
	}
}

func TestIdleSearchSurvivesRuntimeReloadWithoutReadingAnotherView(t *testing.T) {
	m := model(t, issue("DEMO-1", "Assigned work"))
	m.Update(key.Replay("tab"))
	m.openSettings()
	next := model(t, issue("DEMO-2", "Replacement my work"))
	got, load := m.adoptRuntime(next)
	reloaded := got.(*Model)
	if load != nil || !reloaded.emptySearch() || len(reloaded.issues) != 0 || reloaded.loading || reloaded.editing {
		t.Fatal("runtime reload turned idle Search into a different query or started editing")
	}
}

func TestSearchStartsOnlyAfterAnExplicitQueryIsCommitted(t *testing.T) {
	m := model(t, issue("DEMO-1", "Assigned work"))
	m.Update(key.Replay("tab"))
	calls := 0
	m.search = func(query, _ string) (envelope.IssueListOutput, error) {
		calls++
		if query != "project = DEMO" {
			t.Fatal("committed Search used another tab's query", query)
		}
		return envelope.IssueListOutput{Pagination: envelope.CursorPagination{IsLast: true}}, nil
	}
	m.Update(key.Replay("enter"))
	m.input.SetValue("project = DEMO")
	m.Update(key.Replay("tab"))
	if !m.editing || m.tabs.ActiveItem() != "Search" || calls != 0 {
		t.Fatal("tab switching stole explicitly focused query input")
	}
	_, run := m.Update(key.Replay("enter"))
	if run == nil || m.editing {
		t.Fatal("query commit did not start Search")
	}
	m.Update(run())
	if calls != 1 || m.customQuery != "project = DEMO" {
		t.Fatal("committed Search was not executed exactly once")
	}
}
