package dashboard

import (
	"errors"
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func applyCommand(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			applyCommand(m, nested)
		}
		return
	}
	_, next := m.Update(msg)
	applyCommand(m, next)
}

func TestSavedViewsKeepAuthoredJQLAndCancelOutdatedMenus(t *testing.T) {
	m := model(t, issue("DEMO-1", "Current"))
	const query = `project = OTHER AND statusCategory != Done ORDER BY created ASC`
	m.options.LoadViews = func() ([]SavedView, error) {
		return []SavedView{{Name: "Authored view", Description: "Scope is explicit", Query: query}}, nil
	}
	applyCommand(m, press(m, 'v'))
	m.Update(tea.PasteMsg{Content: "Authored view"})
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.query != query || m.customQuery != query || m.viewName != "Saved · Authored view" {
		t.Fatal("saved JQL was rewritten or its identity lost")
	}
	_, pending := m.Update(key.Replay("v"))
	m.Update(key.Replay("esc"))
	applyCommand(m, pending)
	if m.dialogs.Active() {
		t.Fatal("a canceled local read reopened a menu")
	}
}

func TestIssueSwitcherOpensExactKeysAndKeepsOriginalQuery(t *testing.T) {
	m := model(t, issue("DEMO-1", "Current"))
	query := m.query
	m.rememberIssue("DEMO-9", "A recent issue")
	m.openIssues()
	m.Update(tea.PasteMsg{Content: "DEMO-9"})
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.detailKey != "DEMO-9" || m.query != query || m.activeIssueKey() != "DEMO-9" || !m.standalone {
		t.Fatal("switching an issue changed the search or action target")
	}
	m.Update(key.Replay("esc"))
	if m.detailKey != "DEMO-1" || m.standalone {
		t.Fatal("returning from a direct issue lost the original list selection")
	}
	m.openIssues()
	m.Update(tea.PasteMsg{Content: "DEMO-88"})
	_, cmd = m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.detailKey != "DEMO-88" || m.detailIssue == nil {
		t.Fatal("an exact key outside local matches was not fetched")
	}
}

func TestActionsRequireReviewBindTargetsAndRetainUncertainDrafts(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	writes := 0
	m.options.Actions = Actions{
		Transitions: func(target string) ([]jira.IssueTransition, error) {
			if target != "DEMO-1" {
				t.Fatal("workflow was read for a different issue")
			}
			return []jira.IssueTransition{{ID: jira.NullableOf("31"), Name: jira.NullableOf("Finish"), To: jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("Done")})}}, nil
		},
		Write: func(kind, target, value string) (core.IssueActionReceipt, error) {
			writes++
			if kind != "transition" || target != "DEMO-1" || value != "31" {
				t.Fatalf("review target changed: %s %s %s", kind, target, value)
			}
			return core.IssueActionReceipt{Attempted: true, Applied: true}, nil
		},
	}
	applyCommand(m, press(m, 't'))
	m.Update(key.Replay("enter"))
	if writes != 0 || !m.dialogs.Active() {
		t.Fatal("choosing a transition wrote before review")
	}
	m.rows.SetCursor(1)
	_, cmd := m.Update(key.Replay("enter"))
	if writes != 0 {
		t.Fatal("write ran on the update loop")
	}
	applyCommand(m, cmd)
	if writes != 1 || !strings.Contains(m.notice, "acknowledged transition for DEMO-1") {
		t.Fatal("write receipt or bound target was lost")
	}
	m.options.Actions.Write = func(kind, target, value string) (core.IssueActionReceipt, error) {
		writes++
		return core.IssueActionReceipt{Attempted: true}, errors.New("connection ended after request")
	}
	press(m, 'c')
	m.Update(tea.PasteMsg{Content: "Keep this draft"})
	m.Update(key.Replay("ctrl+s"))
	_, cmd = m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if writes != 2 || m.drafts[m.selectedKey()] != "Keep this draft" || !strings.Contains(m.notice, "inspect Jira before repeating") {
		t.Fatal("uncertain comment was retried, discarded or presented as safe to repeat")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if writes != 2 {
		t.Fatal("repaint retried a write")
	}
}

func TestActionMetadataCannotOpenAfterNavigationAndReadOnlyRefusesWrites(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	calls := 0
	m.options.Actions = Actions{
		Transitions: func(string) ([]jira.IssueTransition, error) {
			return []jira.IssueTransition{{ID: jira.NullableOf("1")}}, nil
		},
		Write: func(string, string, string) (core.IssueActionReceipt, error) {
			calls++
			return core.IssueActionReceipt{}, nil
		},
	}
	pending := press(m, 't')
	press(m, ',')
	applyCommand(m, pending)
	if m.dialogs.Active() || m.settings == nil {
		t.Fatal("stale workflow choices stole focus from Settings")
	}
	m.Update(key.Replay("esc"))
	m.options.Actions.ReadOnly = true
	press(m, 't')
	press(m, 'a')
	press(m, 'c')
	if calls != 0 || m.dialogs.Active() || !strings.Contains(m.notice, "read-only") {
		t.Fatal("read-only actions were offered or invoked")
	}
}

func TestCommentsAreLazyBoundedAndRejectStaleOrRepeatedPages(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	calls := 0
	m.options.Actions.Comments = func(target string, start int) (*jira.CommentPage, error) {
		calls++
		return &jira.CommentPage{StartAt: start, MaxResults: 20, Total: 2, Comments: []jira.Comment{{ID: jira.NullableOf("same-comment")}}}, nil
	}
	if calls != 0 {
		t.Fatal("comments loaded before their section was opened")
	}
	pending := m.changeDetailSection(1)
	newer := press(m, 'j')
	applyCommand(m, pending)
	if len(m.comments) != 0 {
		t.Fatal("comments from another issue were published")
	}
	applyCommand(m, newer)
	if len(m.comments) != 1 {
		t.Fatal("first comment page was not retained")
	}
	applyCommand(m, m.loadComments(true))
	if len(m.comments) != 1 || m.commentsErr == nil {
		t.Fatal("a repeated comment page was appended")
	}
	m.commentsLoading = false
	m.commentsErr = nil
	m.comments = make([]jira.Comment, 200)
	m.commentsTotal = 201
	if m.loadComments(true) != nil {
		t.Fatal("comment paging exceeded its bound")
	}
}

func TestThemePreviewCancelAndSaveShareTheSettingsTransaction(t *testing.T) {
	m := model(t)
	restore, err := cli.PreviewTheme(cli.ResolvedTheme().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	original := cli.ResolvedTheme()
	m.openSettings()
	m.setPreviewTheme("light")
	if cli.ResolvedTheme() == original || !m.settingsChanged() {
		t.Fatal("theme preview did not apply")
	}
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != original {
		t.Fatal("canceled theme did not restore its exact palette")
	}
	m.options.SaveSettings = func(values map[string]string) error {
		if !maps.Equal(values, map[string]string{"theme": "light", "theme_path": ""}) {
			t.Fatalf("theme name/path were not one transaction: %v", values)
		}
		return nil
	}
	m.openSettings()
	m.setPreviewTheme("light")
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if m.settingsChanged() || m.settings.sources["theme"] != "file" {
		t.Fatal("saved theme remained dirty")
	}
	// Restore process styling after proving the saved baseline survives Escape.
	saved := cli.ResolvedTheme()
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != saved {
		t.Fatal("closing settings reverted a saved theme")
	}
}

func TestCommentsResultRetainsFailureForCurrentIssue(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"))
	m.commentsLoading = true
	m.acceptComments(task.FinishedMsg{Result: commentsResult{key: m.detailKey}, Err: errors.New("offline")})
	if m.commentsLoading || m.commentsErr == nil {
		t.Fatal("failed comments read was treated as an empty thread")
	}
}

func TestActionAndIssueDialogsFitSmallTerminals(t *testing.T) {
	m := model(t, issue("DEMO-1", "Fixture"))
	m.options.Actions.Write = func(string, string, string) (core.IssueActionReceipt, error) { return core.IssueActionReceipt{}, nil }
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m.Update(size)
		for _, open := range []func(){m.openIssues, m.openComment, func() { m.text("Save view", "save-view", m.query, "Choose a new saved-search name.") }} {
			open()
			view := m.View().Content
			if strings.Contains(view, "terminal too narrow") {
				t.Fatalf("dialog fell back at supported size %dx%d:\n%s", size.Width, size.Height, view)
			}
			m.Update(key.Replay("esc"))
		}
	}
}
