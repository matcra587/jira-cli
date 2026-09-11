package issues

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/matcra587/jira-cli/internal/adf"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/markdown"
	"github.com/matcra587/jira-cli/internal/tui/core"
)

// mdr builds a fresh themed markdown renderer for render tests.
func mdr() *markdown.Renderer {
	return markdown.NewRenderer(markdown.StyleFromTheme(config.DefaultTheme()))
}

// adfText builds a one-paragraph ADF document with the given text.
func adfDoc(text string) *adf.Document {
	return &adf.Document{
		Type:    "doc",
		Version: 1,
		Content: []adf.Node{{Type: "paragraph", Content: []adf.Node{{Type: "text", Text: text}}}},
	}
}

// adfParagraphs builds an ADF document of n short paragraphs, tall enough to
// overflow a small preview pane.
func adfParagraphs(n int) *adf.Document {
	doc := &adf.Document{Type: "doc", Version: 1}
	for range n {
		doc.Content = append(doc.Content, adf.Node{
			Type:    "paragraph",
			Content: []adf.Node{{Type: "text", Text: "line"}},
		})
	}
	return doc
}

// TestPreviewScrollsAndResetsOnSelectionChange pins the preview-sync invariant:
// PageDown scrolls the preview (not the list cursor), and moving the selection
// repoints the preview and snaps it back to the top. Without the previewKey
// guard this would either never scroll or jump to the top every frame.
func TestPreviewScrollsAndResetsOnSelectionChange(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	ctx.SetSize(120, 12) // small body so a long description overflows the preview
	m := New(ctx).(*Model)
	m.Init(ctx)

	long := mkIssue("JCT-1", "To Do", "long one")
	long.Fields.Description = adfParagraphs(40)
	short := mkIssue("JCT-2", "To Do", "short two")
	m.all = []*jira.Issue{long, short}
	m.applyFilter() // selects JCT-1, syncs the preview to the top

	if got := m.preview.YOffset(); got != 0 {
		t.Fatalf("preview should start at top, YOffset=%d", got)
	}
	if m.preview.TotalLineCount() <= m.preview.Height() {
		t.Fatalf("test needs an overflowing preview: total=%d height=%d",
			m.preview.TotalLineCount(), m.preview.Height())
	}

	// PageDown scrolls the preview, not the list cursor.
	cur := m.list.Cursor()
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.preview.YOffset() == 0 {
		t.Error("PageDown should have scrolled the preview")
	}
	if m.list.Cursor() != cur {
		t.Errorf("PageDown must not move the list cursor (was %d, now %d)", cur, m.list.Cursor())
	}

	// Moving the selection repoints the preview and resets it to the top.
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.preview.YOffset(); got != 0 {
		t.Errorf("selection change should reset preview to top, YOffset=%d", got)
	}
}

// TestWheelScrollsPreview verifies a mouse-wheel event scrolls the focused
// preview viewport.
func TestWheelScrollsPreview(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	ctx.SetSize(120, 12)
	m := New(ctx).(*Model)
	m.Init(ctx)

	iss := mkIssue("JCT-1", "To Do", "long one")
	iss.Fields.Description = adfParagraphs(40)
	m.all = []*jira.Issue{iss}
	m.applyFilter()

	// Pointer inside the preview region (right dock starts at MainWidth).
	m.Update(tea.MouseWheelMsg{X: 100, Y: 5, Button: tea.MouseWheelDown})
	if m.preview.YOffset() == 0 {
		t.Error("wheel-down should have scrolled the preview")
	}
}

// TestWheelMovesListWhenSidebarHidden covers the handleWheel fallback: with no
// preview or detail to scroll, the wheel moves the list selection so the event
// is never dead.
func TestWheelMovesListWhenSidebarHidden(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	ctx.SidebarOpen = false
	ctx.SetSize(120, 40)
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{
		mkIssue("JCT-1", "To Do", "one"),
		mkIssue("JCT-2", "To Do", "two"),
		mkIssue("JCT-3", "To Do", "three"),
	}
	m.applyFilter()

	if m.list.Cursor() != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.list.Cursor())
	}
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.list.Cursor() == 0 {
		t.Error("wheel-down should move the list cursor when the sidebar is hidden")
	}
}

// TestEnterClosesDetail documents the open/close toggle: Enter opens the detail
// view and Enter again closes it (the same as Esc).
func TestEnterClosesDetail(t *testing.T) {
	full := mkIssue("JCT-1", "To Do", "summary")
	ctx := newTestCtx(fakeServices{issue: fakeIssueSvc{full: full}})
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{mkIssue("JCT-1", "To Do", "summary")}
	m.applyFilter()

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // open
	if !m.detailing {
		t.Fatal("enter should open the detail view")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // toggle closed
	if m.detailing {
		t.Error("a second enter should close the detail view")
	}
}

// TestRenderDetailSubTabs pins the sub-tab split: Overview carries the
// description (and no comments), Comments carries the conversation (and no
// description body).
func TestRenderDetailSubTabs(t *testing.T) {
	iss := mkIssue("JCT-1", "To Do", "the summary")
	iss.Fields.Description = adfDoc("the full body text")
	iss.Fields.Comment = &jira.CommentPage{Comments: []*jira.Comment{
		{Author: &jira.User{DisplayName: new("Bob")}, Body: adfDoc("first comment"), Created: new("2026-01-02")},
	}}

	overview := ansi.Strip(renderDetail(iss, false, 80, detailOverview, mdr(), "", ""))
	for _, want := range []string{"JCT-1", "the summary", "Description", "the full body text"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview missing %q in:\n%s", want, overview)
		}
	}
	if strings.Contains(overview, "first comment") {
		t.Errorf("overview should not include comments:\n%s", overview)
	}

	comments := ansi.Strip(renderDetail(iss, false, 80, detailComments, mdr(), "", ""))
	for _, want := range []string{"JCT-1", "Comments", "Bob", "first comment"} {
		if !strings.Contains(comments, want) {
			t.Errorf("comments tab missing %q in:\n%s", want, comments)
		}
	}
	if strings.Contains(comments, "the full body text") {
		t.Errorf("comments tab should not include the description body:\n%s", comments)
	}
}

func TestRenderDetailLoadingShowsPlaceholder(t *testing.T) {
	iss := mkIssue("JCT-1", "To Do", "s")
	out := renderDetail(iss, true, 80, detailComments, mdr(), "", "")
	if !strings.Contains(out, "loading") {
		t.Errorf("loading detail should show a placeholder for comments:\n%s", out)
	}
}

func TestEnterOpensDetailThenEscCloses(t *testing.T) {
	full := mkIssue("JCT-1", "To Do", "the summary")
	full.Fields.Description = adfDoc("body")
	full.Fields.Comment = &jira.CommentPage{Comments: []*jira.Comment{
		{Author: &jira.User{DisplayName: new("Ann")}, Body: adfDoc("hi"), Created: new("2026-01-01")},
	}}
	ctx := newTestCtx(fakeServices{issue: fakeIssueSvc{full: full}})
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{mkIssue("JCT-1", "To Do", "the summary")}
	m.applyFilter()

	// Enter opens the detail view and captures input (so global keys don't fire).
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.detailing || !m.CapturesInput() {
		t.Fatal("enter did not open the detail view")
	}
	if cmd == nil {
		t.Fatal("opening detail should fetch the full issue")
	}
	m.Update(taskMsg(t, cmd)) // full issue (with comments) lands
	if m.detailIssue == nil || m.detailIssue.Fields.Comment == nil {
		t.Error("detail did not load the full issue with comments")
	}

	// Esc closes the detail view.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.detailing {
		t.Error("esc should close the detail view")
	}
}

// TestDetailTabKeyCyclesSubTabs pins the modal sub-tab keys: tab flips
// Overview → Comments and back, resets the scroll, and esc still closes.
func TestDetailTabKeyCyclesSubTabs(t *testing.T) {
	full := mkIssue("JCT-1", "To Do", "summary")
	full.Fields.Description = adfParagraphs(40)
	ctx := newTestCtx(fakeServices{issue: fakeIssueSvc{full: full}})
	ctx.SetSize(120, 12)
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{mkIssue("JCT-1", "To Do", "summary")}
	m.applyFilter()

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd()) // full issue lands
	if m.detailTab != detailOverview {
		t.Fatalf("detail should open on Overview, got tab %d", m.detailTab)
	}
	m.detail.ScrollDown(3)

	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.detailTab != detailComments {
		t.Errorf("tab should switch to Comments, got %d", m.detailTab)
	}
	if m.detail.YOffset() != 0 {
		t.Error("switching sub-tabs should reset the scroll to the top")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.detailTab != detailOverview {
		t.Errorf("tab should cycle back to Overview, got %d", m.detailTab)
	}

	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.detailing {
		t.Error("esc should still close the detail view")
	}
}

// TestDetailShiftTabCyclesBackward pins shift+tab's direction so it stays
// correct when a third sub-tab is added.
func TestDetailShiftTabCyclesBackward(t *testing.T) {
	full := mkIssue("JCT-1", "To Do", "summary")
	ctx := newTestCtx(fakeServices{issue: fakeIssueSvc{full: full}})
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{mkIssue("JCT-1", "To Do", "summary")}
	m.applyFilter()

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.detailTab != detailComments {
		t.Errorf("shift+tab from Overview should wrap backward to Comments, got %d", m.detailTab)
	}
}

// TestWheelRoutesByPointerPosition pins the hit-test: with the sidebar open,
// the wheel scrolls whichever pane the pointer is over — preview steals
// nothing. Regression: state-based routing sent every wheel event to the
// preview, making the list unscrollable by mouse while the sidebar was open.
func TestWheelRoutesByPointerPosition(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	ctx.SetSize(120, 12) // right dock: list x ∈ [0,60), preview x ∈ [60,120)
	m := New(ctx).(*Model)
	m.Init(ctx)

	long := mkIssue("JCT-1", "To Do", "long one")
	long.Fields.Description = adfParagraphs(40)
	m.all = []*jira.Issue{long, mkIssue("JCT-2", "To Do", "two"), mkIssue("JCT-3", "To Do", "three")}
	m.applyFilter()

	// Pointer over the list: the wheel moves the selection, not the preview.
	m.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	if m.list.Cursor() != 1 {
		t.Errorf("wheel over the list should move the cursor, got %d", m.list.Cursor())
	}
	if m.preview.YOffset() != 0 {
		t.Errorf("wheel over the list must not scroll the preview (YOffset=%d)", m.preview.YOffset())
	}

	// Pointer over the preview: the wheel scrolls it and leaves the list alone.
	cur := m.list.Cursor()
	m.Update(tea.MouseWheelMsg{X: 100, Y: 5, Button: tea.MouseWheelDown})
	if m.preview.YOffset() == 0 {
		t.Error("wheel over the preview should scroll it")
	}
	if m.list.Cursor() != cur {
		t.Errorf("wheel over the preview must not move the list (was %d, now %d)", cur, m.list.Cursor())
	}
}

// TestBottomDockDrawsDivider pins the missing separator: a bottom-docked
// preview renders a horizontal rule between the list and the pane (the
// vertical-divider analog of the side docks) and gives that row up from its
// viewport height.
func TestBottomDockDrawsDivider(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	ctx.SetPreviewPosition(core.PreviewBottom)
	ctx.SetSize(80, 24)
	m := New(ctx).(*Model)
	m.Init(ctx)
	m.all = []*jira.Issue{mkIssue("JCT-1", "To Do", "one")}
	m.applyFilter()

	if !strings.Contains(m.View(), "─") {
		t.Error("bottom dock should draw a horizontal divider above the preview")
	}
	if got, want := m.preview.Height(), ctx.PreviewHeight-1; got != want {
		t.Errorf("preview height = %d, want %d (one row given to the divider)", got, want)
	}
}

// TestDetailDescriptionRendersStyledMarkdown pins the glamour path: ADF strong
// marks become terminal styling, not literal markdown markers.
func TestDetailDescriptionRendersStyledMarkdown(t *testing.T) {
	iss := mkIssue("JCT-1", "To Do", "s")
	iss.Fields.Description = &adf.Document{
		Type: "doc", Version: 1,
		Content: []adf.Node{{Type: "paragraph", Content: []adf.Node{
			{Type: "text", Text: "very ", Marks: nil},
			{Type: "text", Text: "important", Marks: []adf.Mark{{Type: "strong"}}},
		}}},
	}
	out := renderDetail(iss, false, 80, detailOverview, mdr(), "", "")
	if strings.Contains(ansi.Strip(out), "**") {
		t.Error("raw markdown markers leaked into the detail view")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("description carries no terminal styling")
	}
	if !strings.Contains(ansi.Strip(out), "important") {
		t.Errorf("description text missing:\n%s", out)
	}
}
