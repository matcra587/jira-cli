package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type fixtureClient struct {
	search func(context.Context, string, string, int) (core.IssueSearchRead, error)
	get    func(context.Context, string) (core.IssueRead, error)
}

func (c fixtureClient) BaseURL() string { return "https://example.atlassian.net" }
func (c fixtureClient) SearchIssues(ctx context.Context, q string, _ []string, cursor string, size int, _ ...string) (core.IssueSearchRead, error) {
	return c.search(ctx, q, cursor, size)
}

func (c fixtureClient) GetIssue(ctx context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	if c.get != nil {
		return c.get(ctx, key)
	}
	bean := issue(key, "Selected issue")
	return core.IssueRead{Issue: &bean}, nil
}

func issue(key, summary string) jira.IssueBean {
	return jira.IssueBean{Key: jira.NullableOf(key), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf(summary)})}
}

func page(issues []jira.IssueBean, last bool, cursor string) core.IssueSearchRead {
	if issues == nil {
		issues = []jira.IssueBean{}
	}
	return core.IssueSearchRead{Results: &jira.SearchResults{Issues: jira.NullableOf(issues), IsLast: jira.NullableOf(last), NextPageToken: jira.NullableOf(cursor)}}
}

func model(t *testing.T, beans ...jira.IssueBean) *Model {
	t.Helper()
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(beans, true, ""), nil
	}}, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(m.Init()())
	return m
}

func press(m *Model, key rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
	return cmd
}

func TestDashboardRejectsSupersededQueriesAndIssueDetails(t *testing.T) {
	client := fixtureClient{search: func(_ context.Context, q, _ string, _ int) (core.IssueSearchRead, error) {
		if q == myWorkQuery {
			return page([]jira.IssueBean{issue("DEMO-1", "Old query")}, true, ""), nil
		}
		return page([]jira.IssueBean{issue("DEMO-2", "New query"), issue("DEMO-3", "Another issue")}, true, ""), nil
	}}
	m := New(t.Context(), client, Options{})
	old := m.Init()
	m.query = "project = DEMO"
	newer := m.newQuery()
	m.Update(newer())
	m.Update(old())
	if m.selectedKey() != "DEMO-2" {
		t.Fatalf("stale search replaced current selection: %q", m.selectedKey())
	}
	_, oldDetail := m.Update(detailDue{key: "DEMO-2"})
	press(m, 'j')
	_, newDetail := m.Update(detailDue{key: "DEMO-3"})
	m.Update(newDetail())
	m.Update(oldDetail())
	if m.detailIssue == nil || m.detailIssue.Key.Value != "DEMO-3" {
		t.Fatal("stale detail replaced the selected issue")
	}
}

func TestDashboardRefreshPreservesIssueIdentityAndRefetchesPreview(t *testing.T) {
	m := model(t, issue("DEMO-1", "One"), issue("DEMO-2", "Two"))
	press(m, 'j')
	_, detail := m.Update(detailDue{key: "DEMO-2"})
	m.Update(detail())
	m.search = func(string, string) (envelope.IssueListOutput, error) {
		return envelope.IssueListOutput{Issues: []envelope.IssueView{envelope.IssueViewFromBean(issue("DEMO-2", "Updated")), envelope.IssueViewFromBean(issue("DEMO-1", "One"))}, Pagination: envelope.CursorPagination{IsLast: true}}, nil
	}
	refresh := press(m, 'r')
	m.Update(refresh())
	if m.selectedKey() != "DEMO-2" || m.rows.Cursor() != 0 || !m.detailLoading || m.detailIssue != nil {
		t.Fatal("refresh lost identity or retained stale detail")
	}
}

func TestDashboardPaginationRefusesCrossPageCursorCyclesAndDuplicates(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "cursor cycle", true: "duplicate issue"}[duplicate], func(t *testing.T) {
			beans := make([]jira.IssueBean, pageSize)
			for i := range beans {
				beans[i] = issue(fmtKey(i), "Issue")
			}
			calls := 0
			m := New(t.Context(), fixtureClient{search: func(_ context.Context, _, cursor string, size int) (core.IssueSearchRead, error) {
				calls++
				if calls == 1 {
					if cursor != "" || size != 50 {
						t.Fatal("wrong first-page request")
					}
					return page(beans, false, "next"), nil
				}
				if cursor != "next" {
					t.Fatal("did not resume the native cursor")
				}
				key := "DEMO-999"
				next := "next"
				if duplicate {
					key = beans[0].Key.Value
					next = "third"
				}
				out := make([]jira.IssueBean, pageSize)
				for i := range out {
					out[i] = issue(fmt.Sprintf("MORE-%d", i+1), "More")
				}
				out[0] = issue(key, "Repeated")
				return page(out, false, next), nil
			}}, Options{})
			m.Update(m.Init()())
			cmd := press(m, 'm')
			m.Update(cmd())
			if m.err == nil || !m.truncated || press(m, 'm') != nil || calls != 2 {
				t.Fatalf("broken pagination remained loadable: %v, %d calls", m.err, calls)
			}
		})
	}
}
func fmtKey(i int) string { return fmt.Sprintf("DEMO-%d", i+1) }

func TestDashboardQueryInputOwnsPrintableKeysAndPaste(t *testing.T) {
	m := model(t)
	press(m, '/')
	m.input.SetValue("")
	press(m, 'q')
	press(m, 'r')
	m.Update(tea.PasteMsg{Content: " project = DEMO"})
	if !m.editing || m.input.Value() != "qr project = DEMO" {
		t.Fatalf("global key consumed query input: %q", m.input.Value())
	}
	m.input.SetValue("project = DEMO ORDER BY updated DESC")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.editing || m.tabs.ActiveItem() != "Search" || m.query != m.customQuery {
		t.Fatal("query was not submitted through Search")
	}
}

func TestDashboardErrorsRemainRetryableWithoutClaimingEmptyResults(t *testing.T) {
	m := model(t)
	m.search = func(string, string) (envelope.IssueListOutput, error) {
		return envelope.IssueListOutput{}, errors.New("service unavailable")
	}
	cmd := press(m, 'r')
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "Could not load issues") || strings.Contains(view, "No issues match") || m.loading {
		t.Fatalf("failed search claimed emptiness: %s", view)
	}
	if press(m, 'r') == nil {
		t.Fatal("failed search could not be retried")
	}
}

func TestDashboardFramesFitAndSanitizeEverySize(t *testing.T) {
	m := model(t, issue("DEMO-1", "Wide 界 ⚠️ 🇬🇧 summary\x1b[2J\nforged row"))
	_, cmd := m.Update(detailDue{key: "DEMO-1"})
	m.Update(cmd())
	for _, size := range [][2]int{{120, 40}, {110, 24}, {80, 24}, {40, 12}, {20, 5}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, focused := range []bool{false, true} {
			m.detailFocused = focused
			m.resize()
			v := m.View()
			lines := strings.Split(v.Content, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v: got %d rows", size, len(lines))
			}
			for i, line := range lines {
				if w := xansi.WcWidth.StringWidth(line); w != size[0] {
					t.Fatalf("%v row %d: width %d", size, i, w)
				}
			}
			if strings.Contains(v.Content, "\x1b[2J") {
				t.Fatal("Jira content injected a terminal control")
			}
			if !v.AltScreen {
				t.Fatal("dashboard did not select alternate screen")
			}
		}
	}
}

func TestDashboardPreservesUnknownAndNullFieldMeaning(t *testing.T) {
	if textValue(jira.Nullable[string]{}) != "not returned" || textValue(jira.NullableNull[string]()) != "-" || textValue(jira.NullableOf("")) != "-" {
		t.Fatal("unfetched and empty fields became indistinguishable")
	}
	m := model(t, issue("DEMO-1", "Summary"))
	bean := issue("DEMO-1", "Summary")
	bean.Fields.Value.Description = jira.NullableOf(json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Real description"}]},{"type":"futureNode"}]}`))
	msg := task.FinishedMsg{Scope: detailScope, Gen: 0, Result: detailResult{key: "DEMO-1", issue: &bean}}
	m.Update(msg)
	if !strings.Contains(m.preview.View(), "Real description") || !strings.Contains(m.preview.View(), "futureNode") {
		t.Fatalf("ADF content or unsupported-node disclosure lost: %s", m.preview.View())
	}
}

func TestDashboardCommandPickerOwnsInputAndReplaysSearch(t *testing.T) {
	m := model(t, issue("DEMO-1", "One"))
	m.Update(key.Replay("ctrl+p"))
	if !m.dialogs.Active() {
		t.Fatal("command picker did not open")
	}
	press(m, 'q')
	if !m.dialogs.Active() || m.dialogs.Top().(*commandDialog).menu.Query() != "q" {
		t.Fatal("typing in the command picker leaked into global shortcuts")
	}
	m.Update(key.Replay("esc"))
	if m.dialogs.Active() {
		t.Fatal("Esc did not restore the issue page")
	}
	m.Update(key.Replay("ctrl+p"))
	for _, r := range "search" {
		press(m, r)
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Active() || !m.editing || m.input.Value() != m.query {
		t.Fatal("the command picker did not replay the ordinary JQL editor action")
	}
}

func TestDashboardCommandPickerFitsInsideSmallAndWideFrames(t *testing.T) {
	m := model(t, issue("DEMO-1", "界 ⚠️"))
	for _, size := range [][2]int{{159, 45}, {120, 30}, {80, 24}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.openCommands()
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v: wrong number of rows", size)
		}
		for i, line := range lines {
			if width := xansi.WcWidth.StringWidth(line); width != size[0] {
				t.Fatalf("%v row %d has width %d", size, i, width)
			}
		}
		m.Update(key.Replay("esc"))
	}
}

func TestDashboardSelectedStatusKeepsColorAndSharedPillWidth(t *testing.T) {
	before := cli.StyleEnabled()
	cli.SetStyleEnabled(true)
	t.Cleanup(func() { cli.SetStyleEnabled(before) })
	first, second := issue("DEMO-1", "One"), issue("DEMO-2", "Two")
	first.Fields.Value.Status = jira.NullableOf(jira.StatusDetails{
		Name: jira.NullableOf("To Do"), StatusCategory: jira.NullableOf(jira.StatusCategory{Key: jira.NullableOf("new")}),
	})
	second.Fields.Value.Status = jira.NullableOf(jira.StatusDetails{
		Name: jira.NullableOf("In Progress"), StatusCategory: jira.NullableOf(jira.StatusCategory{Key: jira.NullableOf("indeterminate")}),
	})
	m := model(t, first, second)
	wantFirst := cli.StatusPadded("To Do", cli.StatusCategory("new"), 11)
	wantSecond := cli.StatusPadded("In Progress", cli.StatusCategory("indeterminate"), 11)
	if !strings.Contains(wantFirst, "\x1b[") {
		t.Fatal("styled control did not contain ANSI")
	}
	for _, selected := range []int{0, 1} {
		m.rows.SetCursor(selected)
		rendered := m.rowsView()
		if !strings.Contains(rendered, wantFirst) || !strings.Contains(rendered, wantSecond) {
			t.Fatalf("selected row %d lost a padded colored status", selected)
		}
	}
}

func TestDashboardCommandOverlayRetainsBackdropAndRestoresPage(t *testing.T) {
	m := model(t, issue("DEMO-1", "Backdrop issue remains visible"))
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	before := m.View().Content
	m.openCommands()
	shown := m.View().Content
	for _, want := range []string{"DEMO-1", "SUMMARY", "Commands", "Search issues"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("open command overlay lost %q from its page or menu", want)
		}
	}
	m.Update(key.Replay("esc"))
	if got := m.View().Content; got != before {
		t.Fatal("dismissing the overlay changed the underlying page")
	}
}

func TestDashboardPrimerOverlayKeepsWideBackdropCellsWithinBounds(t *testing.T) {
	m := model(t)
	for _, width := range []int{78, 79, 119, 120, 155, 156} {
		m.openCommands()
		backdrop := frame(strings.Repeat("界", width/2)+"x", width, 35)
		shown := m.dialogs.View(backdrop, width, 35)
		lines := strings.Split(shown, "\n")
		if len(lines) != 35 {
			t.Fatalf("width %d: overlay changed frame height", width)
		}
		for i, line := range lines {
			if cells := xansi.WcWidth.StringWidth(line); cells != width {
				t.Fatalf("width %d row %d: overlay emitted %d cells", width, i, cells)
			}
		}
		m.Update(key.Replay("esc"))
	}
}

func TestDashboardCommandOverlayKeepsItsFrameAndFooterWhileFiltering(t *testing.T) {
	m := model(t, issue("DEMO-1", "Context"))
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	m.openCommands()
	border := func(view string) (int, string) {
		for i, line := range strings.Split(xansi.Strip(view), "\n") {
			if strings.Contains(line, "╭") {
				return i, line
			}
		}
		t.Fatal("command picker has no framed overlay")
		return 0, ""
	}
	beforeRow, beforeBorder := border(m.View().Content)
	for _, r := range "search" {
		press(m, r)
	}
	afterRow, afterBorder := border(m.View().Content)
	if beforeRow != afterRow || beforeBorder != afterBorder {
		t.Fatal("filtering moved or resized the menu overlay")
	}
	m.Update(key.Replay("esc"))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.openCommands()
	for range 50 {
		m.Update(key.Replay("down"))
	}
	view := xansi.Strip(m.View().Content)
	if !strings.Contains(view, "Quit") || !strings.Contains(view, "enter run") || !strings.Contains(view, "esc back") {
		t.Fatal("scrolling the overlay lost its selected command or pinned footer", view)
	}
}
