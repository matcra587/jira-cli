package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func hierarchyNode(id, key, kind, parent string) core.IssueHierarchyNode {
	node := core.IssueHierarchyNode{ID: id, Key: key, Type: kind, ParentState: core.IssueParentNone}
	if parent != "" {
		node.Parent = core.IssueHierarchyReference{ID: parent}
		node.ParentState = core.IssueParentKnown
	}
	return node
}

func hierarchyPage(t *testing.T, id, key, parent string) envelope.IssueListOutput {
	t.Helper()
	var fields jira.IssueFields
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"summary":"A child","parent":{"id":%q}}`, parent)), &fields); err != nil {
		t.Fatal(err)
	}
	return envelope.IssueListOutput{Issues: []envelope.IssueView{{ID: id, Key: key, Fields: fields}}, Pagination: envelope.CursorPagination{IsLast: true}}
}

func TestHierarchyChildrenParentBackAndTabsPreserveContext(t *testing.T) {
	m := model(t, issue("WORK-1", "Keep filter"), issue("WORK-2", "Keep selected"))
	m.localFilter = "Keep"
	m.marks = map[string]bool{"WORK-2": true}
	m.rebuildRows()
	m.rows.SetCursor(1)
	m.hierarchyRead = func(ref core.IssueHierarchyReference) (core.IssueHierarchyRead, error) {
		if ref.ID == "30" {
			return core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{hierarchyNode("30", "PLAN-1", "Initiative", "")}, Complete: true}, nil
		}
		return core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{hierarchyNode("20", "EPIC-1", "Epic", "30"), hierarchyNode("30", "PLAN-1", "Initiative", "")}, Complete: true}, nil
	}
	queries := []string{}
	m.hierarchySearch = func(q, _ string, _ int) (envelope.IssueListOutput, error) {
		queries = append(queries, q)
		if strings.Contains(q, `"30"`) {
			return hierarchyPage(t, "20", "EPIC-1", "30"), nil
		}
		return hierarchyPage(t, "10", "CHILD-1", "20"), nil
	}
	_, read := m.Update(key.Replay("right"))
	_, load := m.Update(read())
	m.Update(load())
	if !m.hierarchyActive() || m.selectedKey() != "CHILD-1" || m.query != `parent = "20" ORDER BY updated DESC` || !strings.Contains(m.hierarchyBreadcrumb(), "Initiative PLAN-1 › Epic EPIC-1") {
		t.Fatal("child navigation lost native context", m.query, m.hierarchyBreadcrumb())
	}
	_, read = m.Update(key.Replay("left"))
	_, load = m.Update(read())
	m.Update(load())
	if m.selectedKey() != "EPIC-1" || len(queries) != 2 {
		t.Fatal("parent level did not select the prior container", m.selectedKey(), queries)
	}
	m.Update(key.Replay("backspace"))
	if m.selectedKey() != "CHILD-1" || len(queries) != 2 {
		t.Fatal("back made a redundant search or lost loaded rows")
	}
	m.Update(key.Replay("1"))
	m.Update(key.Replay("shift+tab"))
	if !m.hierarchyActive() || m.selectedKey() != "CHILD-1" {
		t.Fatal("returning to hierarchy lost its parked frame")
	}
	m.Update(key.Replay("backspace"))
	if m.tabs.ActiveItem() != "My work" || m.selectedKey() != "WORK-2" || m.localFilter != "Keep" || !m.marks["WORK-2"] {
		t.Fatal("back lost original list/filter/selection/marks")
	}
}

func TestHierarchyRejectsStaleAndMalformedResultsButDisclosesPartialAncestors(t *testing.T) {
	m := model(t, issue("DEMO-1", "Selected"), issue("DEMO-2", "Other"))
	m.hierarchyRead = func(core.IssueHierarchyReference) (core.IssueHierarchyRead, error) {
		return core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{hierarchyNode("1", "DEMO-1", "Epic", "2")}, Reason: "ancestor_unavailable"}, errors.New("denied")
	}
	_, cmd := m.Update(key.Replay("right"))
	press(m, 'j')
	m.Update(cmd())
	if m.hierarchyActive() {
		t.Fatal("late hierarchy result changed the view")
	}
	_, cmd = m.Update(key.Replay("right"))
	_, load := m.Update(cmd())
	if !m.hierarchyActive() || load == nil || !strings.Contains(m.notice, "Ancestors incomplete") || !strings.HasPrefix(m.hierarchyBreadcrumb(), "…") {
		t.Fatal("partial ancestors were silently claimed complete", m.notice)
	}
	for _, page := range []envelope.IssueListOutput{hierarchyPage(t, "1", "DEMO-1", "1"), hierarchyPage(t, "3", "DEMO-3", "999")} {
		if !errors.Is(m.validateHierarchyPage(page), core.ErrIssueHierarchyInvalid) {
			t.Fatal("contradictory native relationship admitted")
		}
	}
	if err := m.validateHierarchyPage(hierarchyPage(t, "3", "DEMO-3", "1")); err != nil {
		t.Fatal(err)
	}
}

func TestHierarchyBackSupersedesPendingPages(t *testing.T) {
	m := model(t, issue("WORK-1", "Original"))
	m.hierarchySearch = func(string, string, int) (envelope.IssueListOutput, error) {
		return hierarchyPage(t, "3", "DEMO-3", "1"), nil
	}
	pending := m.hierarchyHome()
	m.hierarchyBack()
	m.Update(pending())
	if m.hierarchyActive() || m.selectedKey() != "WORK-1" || m.loading {
		t.Fatal("late page replaced a restored frame")
	}
}

func TestHierarchyTypeChoiceIsNativeDraftAndRejectsStaleEditor(t *testing.T) {
	m := model(t, issue("DEMO-1", "Selected"))
	m.options.Actions.HierarchyTypes = func() ([]jira.IssueTypeDetails, error) {
		return []jira.IssueTypeDetails{{ID: jira.NullableOf("300"), Name: jira.NullableOf("Initiative")}}, nil
	}
	m.openSettings()
	selectAuditSetting(t, m, "tui_hierarchy_query")
	m.Update(key.Replay("enter"))
	_, read := m.Update(key.Replay("f2"))
	m.Update(read())
	menu, ok := m.dialogs.Top().(*hierarchyTypeMenu)
	if !ok || menu.value() != `issuetype = "300" ORDER BY updated DESC` {
		t.Fatal("root type choice did not use native ID")
	}
	m.Update(key.Replay("esc"))
	if m.settings.editor == nil {
		t.Fatal("cancel discarded authored roots editor")
	}
	_, read = m.Update(key.Replay("f2"))
	m.Update(key.Replay("esc"))
	m.Update(read())
	if m.dialogs.Active() {
		t.Fatal("closed editor accepted late type choices")
	}
	m.Update(key.Replay("enter"))
	_, read = m.Update(key.Replay("f2"))
	m.Update(read())
	m.Update(key.Replay("enter"))
	if m.settings.editor != nil || m.settings.values["tui_hierarchy_query"] != `issuetype = "300" ORDER BY updated DESC` {
		t.Fatal("type did not become a settings draft")
	}
}

func TestHierarchyReloadKeepsLocationAndUsesNewRootPreference(t *testing.T) {
	m := model(t, issue("DEMO-1", "Selected"))
	m.options.SettingValues = map[string]string{"tui_hierarchy_query": config.DefaultTUIHierarchyQuery}
	m.hierarchyHome()
	m.loading = false
	m.openSettings()
	next := model(t)
	next.options.SettingValues = map[string]string{"tui_hierarchy_query": `issuetype = "300"`}
	got, _ := m.adoptRuntime(next)
	reloaded := got.(*Model)
	if !reloaded.hierarchyActive() || reloaded.query != `issuetype = "300"` {
		t.Fatal("hot reload ignored changed root preference", reloaded.query)
	}
	m.hierarchy.location = &hierarchyLocation{read: core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{hierarchyNode("2", "EPIC-2", "Epic", "")}, Complete: true}}
	m.query = `parent = "2" ORDER BY updated DESC`
	next = model(t)
	got, _ = m.adoptRuntime(next)
	if got.(*Model).query != m.query || got.(*Model).hierarchy.location == nil {
		t.Fatal("reload lost current child container")
	}
}

func TestHierarchyFramesFitAndRootEditorKeepsHints(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m := model(t)
		m.hierarchyHome()
		m.loading = false
		m.hierarchy.location = &hierarchyLocation{read: core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{hierarchyNode("1", "CURRENT-1", "Epic", ""), hierarchyNode("2", "AN-EXTREMELY-LONG-ANCESTOR-2", "Initiative", "")}, Complete: true}}
		m.Update(size)
		if !strings.Contains(m.listView(), "Epic CURRENT-1") {
			t.Fatal("breadcrumb clipped current level")
		}
		m.options.Actions.HierarchyTypes = func() ([]jira.IssueTypeDetails, error) { return nil, nil }
		m.openSettings()
		selectAuditSetting(t, m, "tui_hierarchy_query")
		m.Update(key.Replay("enter"))
		view := m.View().Content
		for _, hint := range []string{"enter keep", "esc cancel", "f2 issue type"} {
			if !strings.Contains(view, hint) {
				t.Fatalf("root editor lost %q at %dx%d", hint, size.Width, size.Height)
			}
		}
	}
}

func TestHierarchyTypeMenuRefusesAmbiguousMetadata(t *testing.T) {
	good := jira.IssueTypeDetails{ID: jira.NullableOf("1"), Name: jira.NullableOf("Epic")}
	for _, types := range [][]jira.IssueTypeDetails{{good, good}, {{ID: jira.NullableOf("bad\nID"), Name: good.Name}}, {{ID: good.ID}}} {
		if _, err := hierarchyTypeChoices(types); !errors.Is(err, core.ErrIssueMetadataInvalid) {
			t.Fatal("invalid root metadata admitted", err)
		}
	}
}

func TestHierarchyBackRetainsFailedAutomaticPageCursor(t *testing.T) {
	m := model(t, issue("WORK-1", "Original"))
	m.next, m.isLast, m.autoPageCursor = "resume", false, "resume"
	failed := errors.New("page unavailable")
	m.err = failed
	m.hierarchyHome()
	m.hierarchyBack()
	if m.next != "resume" || m.isLast || m.autoPageCursor != "resume" || !errors.Is(m.err, failed) {
		t.Fatal("back discarded the failed page's explicit-retry state")
	}
	if m.autoPage() != nil || m.loading || m.loadingMore {
		t.Fatal("restored failed page automatically retried or retained phantom loading")
	}
}

func TestSavedViewSwitchParksOriginalHierarchyContext(t *testing.T) {
	for _, children := range []bool{false, true} {
		name := "roots"
		if children {
			name = "children"
		}
		t.Run(name, func(t *testing.T) {
			m := model(t, issue("CHILD-1", "Child one"), issue("CHILD-2", "Child two"))
			m.Update(m.hierarchyHome()())
			if children {
				m.hierarchy.location = &hierarchyLocation{read: core.IssueHierarchyRead{
					Nodes: []core.IssueHierarchyNode{hierarchyNode("20", "EPIC-1", "Epic", "")}, Complete: true,
				}}
				m.query = `parent = "20" ORDER BY updated DESC`
			}
			m.localFilter = "Child"
			m.marks = map[string]bool{"CHILD-2": true}
			m.rebuildRows()
			m.rows.SetCursor(1)
			query, location := m.query, m.hierarchy.location
			m.showViews([]SavedView{{Name: "Other view", Query: "project = OTHER"}})
			picker := m.dialogs.Top().(*pickerDialog)
			updatePalettePaste(&picker.menu, "Other view")
			m.Update(key.Replay("enter"))
			if m.tabs.ActiveItem() != "Search" || m.query != "project = OTHER" {
				t.Fatal("saved view was not selected")
			}
			m.Update(key.Replay("tab"))
			if !m.hierarchyActive() || m.query != query || m.hierarchy.location != location || m.loading {
				t.Fatalf("saved view changed or reloaded parked hierarchy context: query=%q want=%q loading=%v", m.query, query, m.loading)
			}
			if m.selectedKey() != "CHILD-2" || m.localFilter != "Child" || !m.marks["CHILD-2"] || m.viewName != "" {
				t.Fatal("returning to hierarchy lost its loaded rows, filter, selection, marks or title")
			}
		})
	}
}
