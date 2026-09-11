package issues

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/core"
)

func TestAge(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct{ ts, want string }{
		{"2026-06-09T11:59:30.000+0000", "now"},
		{"2026-06-09T11:30:00.000+0000", "30m"},
		{"2026-06-09T03:00:00.000+0000", "9h"},
		{"2026-06-05T12:00:00.000+0000", "4d"},
		{"2026-05-12T12:00:00.000+0000", "4w"},
		{"2026-05-08T12:00:00.000+0000", "4w"}, // 32d: the old code said "1mo"; the library caps weeks at 4
		{"2025-12-09T12:00:00.000+0000", "6mo"},
		{"2024-06-09T12:00:00.000+0000", "2y"},
		{"2026-06-09T13:00:00.000+0000", "now"}, // clock skew: future clamps to now
		{"", ""},
		{"not-a-timestamp", ""},
	}
	for _, c := range cases {
		if got := age(c.ts, now); got != c.want {
			t.Errorf("age(%q) = %q, want %q", c.ts, got, c.want)
		}
	}
}

// TestRowTextRightAlignsAge pins the column math: a width-budgeted row is
// exactly the budget wide and ends with the right-aligned age.
func TestRowTextRightAlignsAge(t *testing.T) {
	iss := mkIssue("JCT-1", "To Do", "fix the flux capacitor")
	upd := "2026-06-09T10:00:00.000+0000"
	iss.Fields.Updated = &upd
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	row := rowText(iss, 70, widestStatus([]*jira.Issue{iss}), now)
	if w := lipgloss.Width(row); w != 70 {
		t.Errorf("row width = %d, want 70:\n%q", w, row)
	}
	if !strings.HasSuffix(row, "2h") {
		t.Errorf("row should end with the age %q:\n%q", "2h", row)
	}

	// Too narrow for an age column: the summary just runs to the edge.
	narrow := rowText(iss, rowFixed+minSummary+ageCol+1, widestStatus([]*jira.Issue{iss}), now)
	if strings.HasSuffix(narrow, "2h") {
		t.Errorf("narrow row should drop the age column:\n%q", narrow)
	}
}

func TestColumnHeaderAlignsWithRows(t *testing.T) {
	h := columnHeader(70)
	if !strings.Contains(h, "AGE") {
		t.Errorf("wide header should include AGE:\n%q", h)
	}
	if got := lipgloss.Width(h); got != 70+2 { // rows carry a 2-char marker the header mirrors
		t.Errorf("header width = %d, want 72:\n%q", got, h)
	}
	if narrow := columnHeader(20); strings.Contains(narrow, "AGE") {
		t.Errorf("narrow header should drop AGE:\n%q", narrow)
	}
}

// TestChipsWithQuery pins the one-row query hint: wide terminals see the active
// lens's JQL, narrow ones just the chips.
func TestChipsWithQuery(t *testing.T) {
	wide := chipsWithQuery(Lenses(), 0, 200)
	if !strings.Contains(wide, Lenses()[0].JQL) {
		t.Errorf("wide chips should carry the active JQL:\n%q", wide)
	}
	if narrow := chipsWithQuery(Lenses(), 0, 30); narrow != chips(Lenses(), 0) {
		t.Errorf("narrow chips should be bare:\n%q", narrow)
	}
}

// TestRowTextKeepsAgeAlignedForWideGlyphs pins the cell-aware truncation: a
// CJK summary (2 cells per glyph) must not push the age column off budget.
func TestRowTextKeepsAgeAlignedForWideGlyphs(t *testing.T) {
	iss := mkIssue("JCT-2", "To Do", strings.Repeat("漢", 40))
	upd := "2026-06-09T10:00:00.000+0000"
	iss.Fields.Updated = &upd
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	row := rowText(iss, 70, widestStatus([]*jira.Issue{iss}), now)
	if w := lipgloss.Width(row); w != 70 {
		t.Errorf("CJK row width = %d, want 70:\n%q", w, row)
	}
	if !strings.HasSuffix(row, "2h") {
		t.Errorf("CJK row should keep the age column:\n%q", row)
	}
}

// TestStatusPillsPadToWidestInView pins the pill normalization: every badge
// in a view is as wide as the view's widest status, so the pills form an
// even column instead of a ragged one.
func TestStatusPillsPadToWidestInView(t *testing.T) {
	issues := []*jira.Issue{
		mkIssue("JCT-1", "To Do", "short"),
		mkIssue("JCT-2", "In Progress", "long"),
	}
	statusW := widestStatus(issues)
	if want := lipgloss.Width("In Progress"); statusW != want {
		t.Fatalf("widestStatus = %d, want %d", statusW, want)
	}
	a := statusCell(issues[0], statusW)
	b := statusCell(issues[1], statusW)
	// The styled badge (everything before the unstyled trailing pad) must be
	// the same width for both rows: name + 2 spaces of pill.
	wantBadge := statusW + 2
	for _, cell := range []string{a, b} {
		badge := strings.TrimRight(cell, " ")
		if w := lipgloss.Width(badge); w != wantBadge {
			t.Errorf("pill width = %d, want %d in %q", w, wantBadge, cell)
		}
	}
}

// TestAgeParsesColonOffsets covers Jira deployments that emit RFC3339 zone
// offsets (+05:30) instead of the compact +0530 form.
func TestAgeParsesColonOffsets(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if got := age("2026-06-09T15:30:00+05:30", now); got != "2h" {
		t.Errorf("age(colon offset) = %q, want 2h", got)
	}
}

// TestDetailHeadingShowsPillAndAge verifies the detail header: project
// breadcrumb, status pill text, and the relative updated age — against a
// pinned clock, so the assertion can't straddle an hour boundary.
func TestDetailHeadingShowsPillAndAge(t *testing.T) {
	iss := mkIssue("JCT-7", "In Progress", "polish the chrome")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	upd := now.Add(-2 * time.Hour).Format(jiraTimeLayout)
	iss.Fields.Updated = &upd
	out := detailHeading(iss, 60, now, "")
	for _, want := range []string{"JCT", "JCT-7", "polish the chrome", "In Progress", "updated 2h ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("heading missing %q in:\n%s", want, out)
		}
	}
}

// TestCountReportsAfterFetch pins the tab-count gate: unknown before the first
// fetch lands, the loaded total afterwards.
func TestCountReportsAfterFetch(t *testing.T) {
	ctx := newTestCtx(fakeServices{})
	m := New(ctx).(*Model)
	m.Init(ctx)
	if _, ok := m.Count(); ok {
		t.Fatal("count should be unknown before the first fetch lands")
	}
	m.Update(core.TaskFinishedMsg{
		Scope:  m.fetchScope(),
		Result: fetchResult{issues: []*jira.Issue{mkIssue("JCT-1", "To Do", "one")}},
	})
	if n, ok := m.Count(); !ok || n != 1 {
		t.Fatalf("Count = %d,%v; want 1,true", n, ok)
	}
}

// TestRowTextAssigneeColumn pins the assignee column: named assignees render in
// a fixed-width cell, unassigned shows a dash, and the column is the first to
// degrade on narrow terminals (age survives longer).
func TestRowTextAssigneeColumn(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	iss := mkIssue("JCT-1", "To Do", "summary text")
	iss.Fields.Assignee = &jira.User{DisplayName: new("Ann Example")}
	upd := "2026-06-09T10:00:00.000+0000"
	iss.Fields.Updated = &upd

	row := rowText(iss, 80, widestStatus([]*jira.Issue{iss}), now)
	if w := lipgloss.Width(row); w != 80 {
		t.Errorf("row width = %d, want 80:\n%q", w, row)
	}
	if !strings.Contains(row, "Ann Examp…") { // ellipsis-truncated to the 10-cell column
		t.Errorf("row should carry the assignee column:\n%q", row)
	}
	if h := columnHeader(80); !strings.Contains(h, "ASSIGNEE") {
		t.Errorf("wide header should include ASSIGNEE:\n%q", h)
	}

	// Mid width: assignee drops first, age survives.
	mid := rowFixed + minSummary + ageCol + 2
	if l := layoutFor(mid); l.assignee || !l.age {
		t.Errorf("layoutFor(%d) = %+v, want age-only", mid, l)
	}

	// Unassigned renders a dim dash, not the word.
	un := mkIssue("JCT-2", "To Do", "summary")
	if !strings.Contains(rowText(un, 80, widestStatus([]*jira.Issue{un}), now), "—") {
		t.Error("unassigned row should show a dash in the assignee column")
	}
}
