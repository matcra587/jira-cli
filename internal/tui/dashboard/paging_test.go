package dashboard

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func pagingIssues(start, count int) []jira.IssueBean {
	issues := make([]jira.IssueBean, count)
	for i := range issues {
		issues[i] = issue(fmt.Sprintf("DEMO-%d", start+i), "Paged issue")
	}
	return issues
}

func TestAutoPageNavigationIsSingleFlightAndPreservesSelection(t *testing.T) {
	var cursors []string
	m := New(t.Context(), fixtureClient{search: func(_ context.Context, _, cursor string, _ int) (core.IssueSearchRead, error) {
		cursors = append(cursors, cursor)
		if cursor == "" {
			return page(pagingIssues(1, 50), false, "second"), nil
		}
		return page(pagingIssues(51, 10), true, ""), nil
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	applyCommand(m, press(m, 'j'))
	if len(cursors) != 1 {
		t.Fatal("loaded more before approaching the result boundary")
	}
	m.rows.SetCursor(43)
	cmd := press(m, 'j')
	if !m.loading || !m.loadingMore {
		t.Fatal("forward navigation did not admit the next page")
	}
	second := press(m, 'j')
	selected := m.selectedKey()
	applyCommand(m, cmd)
	applyCommand(m, second)
	if len(cursors) != 2 || cursors[1] != "second" || len(m.issues) != 60 || m.selectedKey() != selected {
		t.Fatalf("pagination repeated a cursor or moved selection: %v selected=%s", cursors, m.selectedKey())
	}
	applyCommand(m, press(m, 'G'))
	if len(cursors) != 2 {
		t.Fatal("last page was requested again")
	}
}

func TestAutoPageFailureWaitsForExplicitRetry(t *testing.T) {
	var requests int
	m := New(t.Context(), fixtureClient{search: func(_ context.Context, _, cursor string, _ int) (core.IssueSearchRead, error) {
		requests++
		if cursor == "" {
			return page(pagingIssues(1, 50), false, "next"), nil
		}
		if requests == 2 {
			return core.IssueSearchRead{}, errors.New("synthetic read failure")
		}
		return page(pagingIssues(51, 2), true, ""), nil
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	applyCommand(m, press(m, 'G'))
	if requests != 2 || m.err == nil || len(m.issues) != 50 {
		t.Fatal("failed page did not retain the loaded list")
	}
	for range 3 {
		applyCommand(m, press(m, 'j'))
	}
	if requests != 2 {
		t.Fatal("navigation automatically retried a failed cursor")
	}
	applyCommand(m, press(m, 'm'))
	if requests != 3 || m.err != nil || len(m.issues) != 52 {
		t.Fatal("manual loading could not recover the failed cursor")
	}
}

func TestAutoPageRespectsSettingsFocusAndBounds(t *testing.T) {
	for _, state := range []string{"disabled", "last", "truncated", "no-cursor", "empty", "budget", "settings", "detail", "filter"} {
		t.Run(state, func(t *testing.T) {
			m := model(t, pagingIssues(1, 10)...)
			m.isLast, m.next = false, "next"
			m.rows.Bottom()
			switch state {
			case "disabled":
				m.preferences["tui_auto_page"] = false
			case "last":
				m.isLast = true
			case "truncated":
				m.truncated = true
			case "no-cursor":
				m.next = ""
			case "empty":
				m.issues = nil
				m.rebuildRows()
			case "budget":
				m.issues = make([]envelope.IssueView, command.MaxSearchIssues)
			case "settings":
				m.openSettings()
			case "detail":
				m.detailFocused = true
			case "filter":
				m.filtering = true
			}
			press(m, 'j')
			if m.loading {
				t.Fatal("auto loading escaped the active context or result bound")
			}
		})
	}
}

func TestAutoPageMouseAndNewQueryRejectOldPages(t *testing.T) {
	m := New(t.Context(), fixtureClient{search: func(_ context.Context, query, cursor string, _ int) (core.IssueSearchRead, error) {
		if query != myWorkQuery {
			return page(pagingIssues(100, 50), false, "next"), nil
		}
		if cursor == "" {
			return page(pagingIssues(1, 50), false, "next"), nil
		}
		return page(pagingIssues(51, 50), true, ""), nil
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	m.rows.Bottom()
	_, old := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if !m.loading {
		t.Fatal("wheel navigation did not load more")
	}
	m.query = "project = OTHER"
	applyCommand(m, m.newQuery())
	applyCommand(m, old)
	if len(m.issues) != 50 || m.issues[0].Key != "DEMO-100" {
		t.Fatal("old page replaced the newer query")
	}
	m.rows.Bottom()
	_, next := m.Update(key.Replay("down"))
	if next == nil || !m.loading {
		t.Fatal("a reused cursor in a new query was incorrectly suppressed")
	}
}

func TestAutoPageSparseFilterLoadsOnlyOneBatchPerNavigation(t *testing.T) {
	requests := 0
	m := New(t.Context(), fixtureClient{search: func(_ context.Context, _, cursor string, _ int) (core.IssueSearchRead, error) {
		requests++
		switch cursor {
		case "":
			return page(pagingIssues(1, 50), false, "second"), nil
		case "second":
			return page(pagingIssues(51, 50), false, "third"), nil
		default:
			return page(pagingIssues(101, 1), true, ""), nil
		}
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	m.localFilter = "DEMO-49"
	applyCommand(m, m.applyLocalFilter())
	if requests != 1 || len(m.visible) != 1 {
		t.Fatal("local filtering fetched unseen rows")
	}
	applyCommand(m, press(m, 'G'))
	if requests != 2 || len(m.issues) != 100 || len(m.visible) != 1 || m.selectedKey() != "DEMO-49" {
		t.Fatal("sparse filter drained another batch or lost selection")
	}
	m.localFilter = "absent-filter-value"
	applyCommand(m, m.applyLocalFilter())
	applyCommand(m, press(m, 'G'))
	if requests != 2 {
		t.Fatal("empty filtered list drained unseen results")
	}
	m.localFilter = "DEMO-49"
	applyCommand(m, m.applyLocalFilter())
	applyCommand(m, press(m, 'j'))
	if requests != 3 || len(m.issues) != 101 {
		t.Fatal("further explicit navigation could not resume a sparse filtered query")
	}
}
