package dashboard

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestWorkspaceViewsRestoreContextByIdentityAndComposeLenses(t *testing.T) {
	workspace := config.Workspace{Sections: []config.WorkspaceSection{{ID: "triage", Title: "Triage", JQL: "project = DEMO ORDER BY created DESC"}}, Lenses: []config.WorkspaceSection{{ID: "mine", Title: "Mine", JQL: "assignee = currentUser()"}}, Tabs: []string{"my-work", "triage", "search"}}
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(pagingIssues(1, 20), true, ""), nil
	}}, Options{Workspace: workspace})
	applyCommand(m, m.Init())
	m.rows.SetCursor(8)
	m.localFilter = "Paged"
	m.rebuildRows()
	m.rows.SetCursor(8)
	selected := m.selectedKey()
	if selected == "" {
		t.Fatal("fixture filter removed selection")
	}
	m.Update(key.Replay("tab"))
	if m.activeViewID() != "triage" || m.query != "project = DEMO ORDER BY created DESC" {
		t.Fatal("custom tab did not use authored JQL")
	}
	m.Update(key.Replay("shift+tab"))
	if m.activeViewID() != "my-work" || m.selectedKey() != selected || m.localFilter != "Paged" {
		t.Fatal("tab return lost view-local selection or filter")
	}
	applyCommand(m, m.selectLens("mine"))
	if m.query != "(assignee = currentUser() AND statusCategory != Done) AND (assignee = currentUser()) ORDER BY updated DESC" {
		t.Fatal("lens did not explicitly compose at the shared JQL owner", m.query)
	}
	m.Update(key.Replay("tab"))
	if m.query != "(project = DEMO) AND (assignee = currentUser()) ORDER BY created DESC" {
		t.Fatal("custom view lost lens or ordering")
	}
	m.activateView("hierarchy")
	m.switchView()
	if m.query != m.hierarchyRootQuery() {
		t.Fatal("lens narrowed native hierarchy implicitly")
	}
	if !slices.Contains(m.tabIDs, "hierarchy") {
		t.Fatal("explicit hierarchy shortcut did not expose its transient tab")
	}
}

func TestWorkspaceHiddenSearchBecomesExplicitTransientTab(t *testing.T) {
	m := model(t)
	m.workspace.Tabs = []string{"my-work"}
	m.tabIDs = []string{"my-work"}
	m.tabs.SetItems([]string{"My work"})
	press(m, '/')
	m.input.SetValue("project = DEMO")
	m.Update(key.Replay("enter"))
	if m.activeViewID() != "search" || len(m.tabIDs) != 2 || len(m.workspace.Tabs) != 1 {
		t.Fatal("explicit search edited the saved tab layout or failed to expose Search")
	}
}

func TestWorkspaceSemanticShortcutsAndCustomHierarchyTitleUseIDs(t *testing.T) {
	w := config.Workspace{Sections: []config.WorkspaceSection{{ID: "custom", Title: "Hierarchy", JQL: "project = CUSTOM"}}, Tabs: []string{"custom", "search", "my-work"}, LandingView: "custom"}
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(pagingIssues(1, 2), true, ""), nil
	}}, Options{Workspace: w, ProjectQuery: "project = DEMO"})
	applyCommand(m, m.Init())
	applyCommand(m, press(m, '1'))
	if m.activeViewID() != "my-work" || m.query != myWorkQuery {
		t.Fatal("reordered tabs changed the My work shortcut")
	}
	applyCommand(m, press(m, '2'))
	if m.activeViewID() != "project" || m.query != "project = DEMO" {
		t.Fatal("Project shortcut selected a tab by position")
	}
	m.parkView()
	m.activateView("custom")
	applyCommand(m, m.switchView())
	if m.query != "project = CUSTOM" || m.hierarchyActive() {
		t.Fatal("custom Hierarchy title activated native hierarchy behavior")
	}
}

func TestWorkspaceHierarchyLensChangeRetainsChildLocation(t *testing.T) {
	m := model(t, issue("DEMO-1", "Child"))
	m.workspace.Lenses = []config.WorkspaceSection{{ID: "mine", Title: "Mine", JQL: "assignee = currentUser()"}}
	m.activateView("hierarchy")
	m.hierarchy.location = &hierarchyLocation{}
	m.query = `parent = "20" ORDER BY updated DESC`
	before := m.hierarchy.location
	if command := m.selectLens("mine"); command != nil || m.query != `parent = "20" ORDER BY updated DESC` || m.hierarchy.location != before {
		t.Fatal("an unapplied lens changed native hierarchy location or started a root read")
	}
}

func TestWorkspaceCommentPageCannotCrossCachedSameIssueViews(t *testing.T) {
	w := config.Workspace{Sections: []config.WorkspaceSection{{ID: "other", Title: "Other", JQL: "project = DEMO"}}, Tabs: []string{"my-work", "other"}}
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page([]jira.IssueBean{issue("DEMO-1", "Same issue")}, true, ""), nil
	}}, Options{Workspace: w})
	m.options.Actions.Comments = func(_ string, start int) (*jira.CommentPage, error) {
		return &jira.CommentPage{Comments: []jira.Comment{{ID: jira.NullableOf(string(rune('1' + start)))}}, Total: 2}, nil
	}
	applyCommand(m, m.Init())
	_, detail := m.Update(detailDue{key: "DEMO-1"})
	applyCommand(m, detail)
	_, switcher := m.Update(key.Replay("tab"))
	applyCommand(m, switcher)
	_, back := m.Update(key.Replay("shift+tab"))
	applyCommand(m, back)
	applyCommand(m, m.changeDetailSection(1))
	pending := m.loadComments(true)
	if pending == nil {
		t.Fatal("fixture did not start the next comment page")
	}
	stale := pending()
	_, switcher = m.Update(key.Replay("tab"))
	applyCommand(m, switcher)
	m.Update(stale)
	if m.detailSection != 0 || len(m.comments) != 0 || m.commentsKey != "" {
		t.Fatal("a comment page from another view entered the restored context")
	}
}

func TestWorkspaceCompactTabStripKeepsActiveViewVisibleAndClickable(t *testing.T) {
	w := config.Workspace{Sections: []config.WorkspaceSection{{ID: "first", Title: strings.Repeat("First ", 8), JQL: "project = ONE"}, {ID: "second", Title: strings.Repeat("Second ", 8), JQL: "project = TWO"}}, Tabs: []string{"first", "second"}}
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(nil, true, ""), nil
	}}, Options{Workspace: w})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.Update(key.Replay("tab"))
	if !strings.Contains(m.viewTabStrip(), "Second") || !strings.Contains(m.viewTabStrip(), "2/2") {
		t.Fatal("active custom tab disappeared beyond the terminal edge")
	}
	m.Update(tea.MouseClickMsg{X: 5, Y: 1, Button: tea.MouseLeft})
	if !m.dialogs.Active() {
		t.Fatal("compact strip did not expose visible tabs")
	}
}

func TestWorkspaceEvictedWindowRereadsWithinBudgetAndRestoresSelection(t *testing.T) {
	calls := 0
	client := fixtureClient{search: func(_ context.Context, _, cursor string, size int) (core.IssueSearchRead, error) {
		calls++
		start := 0
		if cursor != "" {
			var err error
			start, err = strconv.Atoi(cursor)
			if err != nil {
				t.Fatal(err)
			}
		}
		count := min(size, 5000-start)
		next := strconv.Itoa(start + count)
		return page(pagingIssues(start+1, count), start+count == 5000, next), nil
	}}
	w := config.Workspace{Sections: []config.WorkspaceSection{{ID: "other", Title: "Other", JQL: "project = OTHER"}}, Tabs: []string{"my-work", "other"}}
	m := New(t.Context(), client, Options{Workspace: w})
	applyCommand(m, m.Init())
	for len(m.issues) < 5000 {
		applyCommand(m, m.load(true))
		if m.err != nil {
			t.Fatal(m.err)
		}
	}
	m.localFilter = "Paged"
	m.rebuildRows()
	m.rows.SetCursor(4320)
	_, next := m.Update(key.Replay("tab"))
	applyCommand(m, next)
	retained := len(m.issues)
	for _, saved := range m.viewContexts {
		retained += len(saved.frame.issues)
	}
	if retained > 5000 {
		t.Fatal("parked views exceeded the shared issue-window budget")
	}
	before := calls
	_, back := m.Update(key.Replay("shift+tab"))
	applyCommand(m, back)
	if calls <= before || calls-before > 50 || m.selectedKey() != "DEMO-4321" || m.localFilter != "Paged" || len(m.issues) != 5000 {
		t.Fatal("evicted context did not perform a bounded reread and restore its navigation state")
	}
}

func TestWorkspaceRemovedHistoryCannotResurrectCustomQueries(t *testing.T) {
	m := model(t, issue("DEMO-1", "Current"))
	m.workspace.Sections = []config.WorkspaceSection{{ID: "old", Title: "Old view", JQL: "project = OLD"}}
	m.activateView("old")
	m.query = "project = OLD"
	frame := m.hierarchySnapshot()
	m.workspace.Sections = nil
	m.activateView("hierarchy")
	m.query = m.hierarchyRootQuery()
	m.hierarchy.back = []hierarchyFrame{frame}
	command := m.hierarchyBack()
	if command != nil || m.activeViewID() != "hierarchy" || m.query != m.hierarchyRootQuery() {
		t.Fatal("removed custom view was restored from hierarchy history")
	}
	m.workspace.Sections = []config.WorkspaceSection{{ID: "old", Title: "Renamed", JQL: "project = NEW"}}
	command = m.restoreHierarchyFrame(frame)
	if command == nil || m.activeViewID() != "old" || m.query != "project = NEW" {
		t.Fatal("edited custom view restored outdated authored JQL")
	}
}

func TestWorkspacePickersStartOnActiveChoices(t *testing.T) {
	m := model(t)
	m.workspace.Sections = []config.WorkspaceSection{{ID: "other", Title: "Other", JQL: "project = DEMO"}}
	m.workspace.Lenses = []config.WorkspaceSection{{ID: "mine", Title: "Mine", JQL: "assignee = currentUser()"}}
	m.activateView("other")
	m.activeLens = "mine"
	m.query, _ = m.workspaceQuery("other")
	m.openWorkspaceTabs()
	menu := m.dialogs.Top().(*pickerDialog)
	if menu.value() != "other" {
		t.Fatal("view picker started on another tab")
	}
	m.Update(key.Replay("enter"))
	if m.activeViewID() != "other" {
		t.Fatal("confirming active view changed tabs")
	}
	m.openLenses()
	menu = m.dialogs.Top().(*pickerDialog)
	if menu.value() != "mine" {
		t.Fatal("lens picker started on No lens")
	}
	m.Update(key.Replay("enter"))
	if m.activeLens != "mine" {
		t.Fatal("confirming current lens changed scope")
	}
}
