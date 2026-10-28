package dashboard

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLocalFilterKeepsNativeSelectionAndInputFocus(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"), issue("DEMO-3", "Third"))
	query := m.query
	m.Update(key.Replay("ctrl+f"))
	m.Update(tea.PasteMsg{Content: "Second"})
	if m.selectedKey() != "DEMO-2" || m.query != query || !strings.Contains(m.View().Content, "1 / 3 loaded") {
		t.Fatal("local filter did not narrow loaded rows without changing JQL")
	}
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("space"))
	if len(m.markedKeys()) != 1 || m.markedKeys()[0] != "DEMO-2" {
		t.Fatal("filtered row selected the wrong native issue")
	}
	m.Update(key.Replay("ctrl+f"))
	m.Update(key.Replay("esc"))
	if m.selectedKey() != "DEMO-2" || len(m.markedKeys()) != 1 {
		t.Fatal("clearing filter lost selection or native marks")
	}
	m.Update(key.Replay("ctrl+f"))
	press(m, 'q')
	if m.query != query {
		t.Fatal("filter input leaked into global shortcuts")
	}
}

func TestFacetCountsAndFilteredRangeUseNativeIdentities(t *testing.T) {
	first, second, third := issue("DEMO-1", "First"), issue("DEMO-2", "Second"), issue("DEMO-3", "Third")
	first.Fields.Value.Labels = jira.NullableOf([]string{"focus", "focus"})
	third.Fields.Value.Labels = jira.NullableOf([]string{"focus"})
	first.Fields.Value.Assignee = jira.NullableNull[jira.UserDetails]()
	second.Fields.Value.Assignee = jira.NullableOf(jira.UserDetails{AccountID: jira.NullableOf("account-2"), DisplayName: jira.NullableOf("Same Name")})
	third.Fields.Value.Assignee = jira.NullableOf(jira.UserDetails{AccountID: jira.NullableOf("account-3"), DisplayName: jira.NullableOf("Same Name")})
	m := model(t, first, second, third)
	press(m, 'f')
	picker := m.dialogs.Top().(*pickerDialog)
	if !strings.Contains(picker.Content(100), "label: focus (2)") {
		t.Fatal("duplicate labels inflated facet counts")
	}
	var value string
	for id, facet := range picker.facets {
		if facet.field == "label" && facet.value == "focus" {
			value = id
		}
	}
	m.Update(key.Replay("esc"))
	m.facet = picker.facets[value]
	m.applyLocalFilter()
	m.Update(key.Replay("space"))
	press(m, 'j')
	press(m, 'x')
	if len(m.markedKeys()) != 2 || m.marks["DEMO-2"] {
		t.Fatal("filtered range selected a hidden source offset")
	}
	m.facet = issueFacet{field: "assignee", value: "account-3"}
	m.applyLocalFilter()
	if m.selectedKey() != "DEMO-3" {
		t.Fatal("identically named accounts were merged")
	}
	m.facet = issueFacet{field: "assignee"}
	m.applyLocalFilter()
	if m.selectedKey() != "DEMO-1" {
		t.Fatal("explicitly unassigned facet did not match null")
	}
	if m.facet.matches(&envelope.IssueView{}) {
		t.Fatal("absent assignee was treated as explicit null")
	}
	m.markAll()
	if m.marks["DEMO-2"] {
		t.Fatal("select-all included hidden loaded issues")
	}
}

func TestFilteredRefreshAndHiddenIssueSwitcherPreserveScope(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	m.localFilter = "Second"
	m.applyLocalFilter()
	m.acceptList(task.FinishedMsg{Result: envelope.IssueListOutput{Issues: []envelope.IssueView{m.issues[1], m.issues[0]}}})
	if m.selectedKey() != "DEMO-2" {
		t.Fatal("refresh treated filtered offset as a source index")
	}
	applyCommand(m, m.openIssue("DEMO-1"))
	if !m.standalone || !m.detailFocused || m.detailKey != "DEMO-1" {
		t.Fatal("hidden direct issue did not open independently of filtered list")
	}
	m.Update(key.Replay("esc"))
	if m.selectedKey() != "DEMO-2" || m.detailKey != "DEMO-2" {
		t.Fatal("return from hidden direct issue damaged filtered selection")
	}
	m.localFilter = "no matches"
	m.applyLocalFilter()
	if m.selected() != nil || m.detailKey != "" {
		t.Fatal("empty filter retained a stale action target")
	}
}

func TestMouseSelectsFilteredRowsAndDoesNotEscapeOverlays(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"), issue("DEMO-3", "Third"))
	m.localFilter = "!Second"
	m.applyLocalFilter()
	click := tea.MouseClickMsg{X: 4, Y: 8, Button: tea.MouseLeft}
	m.Update(click)
	if m.selectedKey() != "DEMO-3" || m.detailFocused {
		t.Fatal("mouse did not select the filtered native row")
	}
	m.Update(click)
	if !m.detailFocused {
		t.Fatal("second click did not open selected issue")
	}
	m.Update(key.Replay("esc"))
	m.openCommands()
	m.Update(tea.MouseClickMsg{X: 4, Y: 7, Button: tea.MouseLeft})
	if m.selectedKey() != "DEMO-3" {
		t.Fatal("modal click reached background rows")
	}
}

func TestClipboardAndBrowserShortcutsBindTheFocusedIssue(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	if cmd := press(m, 'y'); cmd == nil || fmt.Sprint(cmd()) != "DEMO-1" {
		t.Fatal("clipboard did not receive the native issue key")
	}
	if cmd := press(m, 'Y'); cmd == nil || fmt.Sprint(cmd()) != "https://example.atlassian.net/browse/DEMO-1" {
		t.Fatal("clipboard did not receive the shared issue URL")
	}
	m.detailKey = "DEMO-3"
	m.previewFocused = true
	var opened string
	m.options.Actions.Browse = func(key string) (string, error) {
		opened = key
		return "https://example.atlassian.net/browse/" + key, errors.New("opener unavailable")
	}
	cmd := press(m, 'O')
	m.previewFocused = false
	press(m, 'j')
	applyCommand(m, cmd)
	if opened != "DEMO-3" || !strings.Contains(m.notice, "Browser handoff failed for DEMO-3") {
		t.Fatal("browser action lost focused target or failure")
	}
	m.site = ""
	if cmd := press(m, 'Y'); cmd != nil {
		t.Fatal("invalid site emitted a clipboard URL")
	}
}

func TestFilterOwnsQDuringBatchAndCommandsReflectListFocus(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	m.actionBusy = true
	m.Update(key.Replay("ctrl+f"))
	press(m, 'q')
	if m.localFilter != "q" {
		t.Fatal("busy quit guard stole text input")
	}
	m.Update(key.Replay("esc"))
	m.detailFocused = true
	m.openCommands()
	d := m.dialogs.Top().(*commandDialog)
	for _, r := range "Filter loaded issues" {
		press(m, r)
	}
	if _, ok := d.menu.Selected(); ok {
		t.Fatal("command picker advertised an inert filter action in details")
	}
}

func TestMouseHitGeometryAndFilterOverlaysAtSupportedSizes(t *testing.T) {
	beans := make([]jira.IssueBean, 20)
	for i := range beans {
		beans[i] = issue(fmt.Sprintf("DEMO-%d", i+1), "Fixture")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		for _, compact := range []bool{true, false} {
			m := model(t, beans...)
			m.preferences["tui_compact"] = compact
			m.Update(size)
			for _, point := range [][2]int{{0, 7}, {1, 7}, {2, 6}, {2, 4 + m.bodyHeight()}, {2 + m.listWidth(), 7}} {
				m.Update(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft})
				if m.detailFocused || m.selectedKey() != "DEMO-1" {
					t.Fatalf("non-row click changed focus at %dx%d compact=%v point=%v", size.Width, size.Height, compact, point)
				}
			}
			if !compact {
				m.Update(tea.MouseClickMsg{X: 2, Y: 8, Button: tea.MouseLeft})
				if m.detailFocused {
					t.Fatal("comfortable blank spacer opened an issue")
				}
			}
			m.rows.Bottom()
			start, _ := m.rows.VisibleRange()
			want := m.issues[m.visible[start]].Key
			m.Update(tea.MouseClickMsg{X: 2, Y: 7, Button: tea.MouseLeft})
			if m.selectedKey() != want {
				t.Fatal("scrolled click ignored viewport offset")
			}
			m.Update(key.Replay("esc"))
			m.openFilter()
			m.Update(tea.PasteMsg{Content: "Fixture"})
			view := m.View().Content
			if !strings.Contains(view, "enter keep filter") {
				t.Fatal("filter controls disappeared at supported size")
			}
			for _, line := range strings.Split(view, "\n") {
				if xansi.WcWidth.StringWidth(line) > size.Width {
					t.Fatal("filter widened terminal frame")
				}
			}
			m.Update(key.Replay("enter"))
			m.openFacets()
			view = m.View().Content
			if strings.Contains(view, "terminal too narrow") || !strings.Contains(view, "enter select") {
				t.Fatal("facet controls disappeared at supported size")
			}
		}
	}
}

func TestMouseNavigationDiscardsEarlierActionChoices(t *testing.T) {
	for _, wheel := range []bool{false, true} {
		m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
		m.options.Actions.Write = func(string, string, string) (core.IssueActionReceipt, error) { return core.IssueActionReceipt{}, nil }
		m.options.Actions.Transitions = func(string) ([]jira.IssueTransition, error) {
			return []jira.IssueTransition{transition("31", "Finish", "Done")}, nil
		}
		cmd := press(m, 't')
		if wheel {
			m.Update(tea.MouseWheelMsg{X: 2, Y: 8, Button: tea.MouseWheelDown})
		} else {
			m.Update(tea.MouseClickMsg{X: 2, Y: 8, Button: tea.MouseLeft})
		}
		applyCommand(m, cmd)
		if m.selectedKey() != "DEMO-2" || m.dialogs.Active() {
			t.Fatal("late workflow picker survived pointer navigation")
		}
	}
}
