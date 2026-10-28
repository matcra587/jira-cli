package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/core"
)

const descriptionFixture = `{"type":"doc","version":1,"future":{"large":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"raw":"<>&"}}]}`

func descriptionSession(t *testing.T, id, account, source string) *core.IssueDescriptionSession {
	t.Helper()
	session, err := core.OpenIssueDescriptionSession(core.IssueDescriptionSnapshot{Version: 1, Site: "https://example.atlassian.net", Account: account, APIBaseURL: "https://example.atlassian.net/", IssueID: id, IssueKey: "DEMO-1", Description: json.RawMessage(source), DescriptionBytes: []byte(source)})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func descriptionModel(t *testing.T) *Model {
	t.Helper()
	m := editModel(t)
	session := descriptionSession(t, "101", "person@example.test", descriptionFixture)
	m.options.Actions.Description = func(string) (*core.IssueDescriptionSession, error) { return session, nil }
	m.options.Actions.ReviewDescription = func(session *core.IssueDescriptionSession, markdown string) (core.IssueDescriptionMerge, error) {
		native, err := session.Apply(markdown)
		snapshot := session.Snapshot()
		return core.IssueDescriptionMerge{Current: snapshot, Description: native, Changed: !bytes.Equal(native, snapshot.Description)}, err
	}
	m.options.Actions.ReplaceDescription = func(_ *core.IssueDescriptionSession, _ string, review core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error) {
		return core.IssueActionReceipt{Attempted: true, Applied: true}, review.Current.IssueKey, nil
	}
	return m
}

func openDescription(m *Model) {
	press(m, 'e')
	for _, r := range "Description" {
		press(m, r)
	}
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
}

func replaceDescriptionInput(m *Model, value string) {
	m.Update(key.Replay("ctrl+g"))
	m.Update(tea.PasteMsg{Content: value})
}

func TestDescriptionReviewPreservesNativeDataAndBindsOneWrite(t *testing.T) {
	m := descriptionModel(t)
	calls := 0
	var id, native string
	m.options.Actions.ReplaceDescription = func(session *core.IssueDescriptionSession, _ string, review core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error) {
		calls++
		id = session.Snapshot().IssueID
		native = string(review.Description)
		return core.IssueActionReceipt{Attempted: true, Applied: true}, review.Current.IssueKey, nil
	}
	openDescription(m)
	editor := m.dialogs.Top().(*descriptionDialog)
	changed := strings.Replace(editor.value(), "alpha", "edited alpha", 1)
	replaceDescriptionInput(m, changed)
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if calls != 0 || !strings.Contains(m.dialogs.Top().(*descriptionReview).Content(100), "unconditional") {
		t.Fatal("description review wrote or hid its concurrency limit")
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != editor || calls != 0 {
		t.Fatal("description replacement did not default to cancel")
	}
	_, cmd = m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	_, cmd = m.Update(key.Replay("y"))
	if calls != 0 || cmd == nil {
		t.Fatal("description write executed before admitted command")
	}
	press(m, 'j')
	applyCommand(m, cmd)
	if calls != 1 || id != "101" || !strings.Contains(native, `"type":"futureNode"`) || !strings.Contains(native, "9007199254740993") || !strings.Contains(native, "edited alpha") || len(m.descriptionDrafts) != 0 {
		t.Fatal("description lost native content, target or acknowledgement")
	}
}

func TestDescriptionInvalidAnchorsAndUncertaintyRetainDraft(t *testing.T) {
	m := descriptionModel(t)
	calls := 0
	m.options.Actions.ReplaceDescription = func(_ *core.IssueDescriptionSession, _ string, _ core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error) {
		calls++
		return core.IssueActionReceipt{Attempted: true}, "DEMO-1", errors.New("response lost")
	}
	openDescription(m)
	editor := m.dialogs.Top().(*descriptionDialog)
	original := editor.value()
	replaceDescriptionInput(m, "")
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if m.dialogs.Top() != editor || editor.draft.notice == "" || calls != 0 {
		t.Fatal("deleted protected content reached replacement review")
	}
	changed := strings.Replace(original, "alpha", "edited alpha", 1)
	replaceDescriptionInput(m, changed)
	_, cmd = m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	_, cmd = m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if calls != 1 || len(m.descriptionDrafts) != 1 || !strings.Contains(m.notice, "attempted") {
		t.Fatal("uncertain description lost its session draft or receipt")
	}
	openDescription(m)
	if m.dialogs.Top().(*descriptionDialog).value() != changed {
		t.Fatal("uncertain description draft could not be resumed")
	}
}

func TestDescriptionNoopReloadAndStaleReviewKeepLocalWork(t *testing.T) {
	m := descriptionModel(t)
	openDescription(m)
	editor := m.dialogs.Top().(*descriptionDialog)
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if m.dialogs.Top() != editor || !strings.Contains(editor.draft.notice, "No local changes") {
		t.Fatal("unchanged native description opened a write review")
	}
	changed := strings.Replace(editor.value(), "alpha", "local work", 1)
	replaceDescriptionInput(m, changed)
	_, cmd = m.Update(key.Replay("ctrl+s"))
	press(m, 'x')
	applyCommand(m, cmd)
	if m.dialogs.Top() != editor || !strings.Contains(editor.draft.notice, "superseded") {
		t.Fatal("stale preparation replaced current input")
	}
	m.options.Actions.Description = func(string) (*core.IssueDescriptionSession, error) { return nil, errors.New("offline") }
	m.Update(key.Replay("f4"))
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != editor {
		t.Fatal("reload did not default to retaining local edits")
	}
	m.Update(key.Replay("f4"))
	_, cmd = m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if m.dialogs.Top() != editor || !strings.Contains(editor.value(), "local work") {
		t.Fatal("failed source reload discarded the local edit")
	}
}

func TestUnmodifiedDescriptionVisitsDoNotHideFreshNativeContent(t *testing.T) {
	m := descriptionModel(t)
	openDescription(m)
	m.Update(key.Replay("esc"))
	if len(m.descriptionDrafts) != 0 {
		t.Fatal("unmodified visit created a stale native draft")
	}
	m.options.Actions.Description = func(string) (*core.IssueDescriptionSession, error) {
		return descriptionSession(t, "101", "person@example.test", strings.Replace(descriptionFixture, "alpha", "remote alpha", 1)), nil
	}
	openDescription(m)
	if !strings.Contains(m.dialogs.Top().(*descriptionDialog).value(), "remote alpha") {
		t.Fatal("unchanged visit overrode a fresh native read")
	}
}

func TestDescriptionScopeAndSupportedSizeControls(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m := descriptionModel(t)
		m.Update(size)
		openDescription(m)
		view := m.View().Content
		if strings.Contains(view, "terminal too narrow") || !strings.Contains(view, "^S review") {
			t.Fatal("description controls missing at supported size")
		}
		editor := m.dialogs.Top().(*descriptionDialog)
		replaceDescriptionInput(m, strings.Replace(editor.value(), "alpha", "changed alpha", 1))
		_, cmd := m.Update(key.Replay("ctrl+s"))
		applyCommand(m, cmd)
		if !strings.Contains(m.View().Content, "y replace") {
			t.Fatal("description confirmation controls were clipped")
		}
	}
	m := descriptionModel(t)
	cmd := m.loadDescription("DEMO-1", "", nil)
	press(m, 'j')
	applyCommand(m, cmd)
	if m.dialogs.Active() {
		t.Fatal("late description session stole navigation focus")
	}
	openDescription(m)
	editor := m.dialogs.Top().(*descriptionDialog)
	replaceDescriptionInput(m, strings.Replace(editor.value(), "alpha", "private draft", 1))
	m.Update(key.Replay("esc"))
	m.options.Actions.Description = func(string) (*core.IssueDescriptionSession, error) {
		return descriptionSession(t, "101", "other@example.test", descriptionFixture), nil
	}
	openDescription(m)
	if strings.Contains(m.dialogs.Top().(*descriptionDialog).value(), "private draft") {
		t.Fatal("description draft crossed account scope")
	}
}
