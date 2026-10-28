package dashboard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func creationModel(t *testing.T) *Model {
	t.Helper()
	m := model(t)
	m.options.Actions.Projects = func() ([]core.IssueCreateProjectRead, bool, error) {
		return []core.IssueCreateProjectRead{{ID: "10402", Key: "DEMO"}}, true, nil
	}
	m.options.Actions.Project = func(selector string) (core.IssueCreateProjectRead, error) {
		if selector != "10402" {
			return core.IssueCreateProjectRead{}, errors.New("wrong project selector")
		}
		return core.IssueCreateProjectRead{ID: "10402", Key: "DEMO"}, nil
	}
	m.options.Actions.CreateTypes = func(project string) (map[string]json.RawMessage, error) {
		if project != "10402" {
			return nil, errors.New("wrong project")
		}
		return map[string]json.RawMessage{"10002": json.RawMessage(`{"id":"10002","name":"Task"}`)}, nil
	}
	m.options.Actions.PrepareCreate = func(input core.IssueCreationInput) (jira.IssueWriteRequest, error) {
		var req jira.IssueWriteRequest
		if err := req.SetField("summary", input.Summary); err != nil {
			return req, err
		}
		if err := req.SetField("labels", input.Labels.Value); err != nil {
			return req, err
		}
		if err := req.SetField("description", input.Markdown); err != nil {
			return req, err
		}
		return req, nil
	}
	m.options.Actions.Create = func(jira.IssueWriteRequest) (envelope.IssueCreateOutput, error) {
		return envelope.IssueCreateOutput{Attempted: true, Applied: true, Issue: &envelope.IssueRef{ID: "9001", Key: "NEW-1"}}, nil
	}
	return m
}

func chooseCreation(m *Model, query string) {
	for _, r := range query {
		press(m, r)
	}
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
}

func startCreation(t *testing.T, m *Model) {
	t.Helper()
	applyCommand(m, press(m, 'n'))
	chooseCreation(m, "DEMO")
	chooseCreation(m, "")
	if m.creationEditor == nil {
		t.Fatal("metadata choices did not open the creation editor")
	}
}

func TestCreationReviewsBeforeWritingAndFreezesAuthoredFields(t *testing.T) {
	m := creationModel(t)
	query := m.query
	calls := 0
	var sent jira.IssueWriteRequest
	m.options.Actions.Create = func(req jira.IssueWriteRequest) (envelope.IssueCreateOutput, error) {
		calls++
		sent = req
		return envelope.IssueCreateOutput{Attempted: true, Applied: true, Issue: &envelope.IssueRef{ID: "9001", Key: "NEW-1"}}, nil
	}
	startCreation(t, m)
	m.Update(tea.PasteMsg{Content: "New literal summary"})
	m.Update(key.Replay("tab"))
	body := strings.Repeat("Long **native** body.\n\n", 80)
	m.Update(tea.PasteMsg{Content: body})
	m.Update(key.Replay("f2"))
	m.Update(tea.PasteMsg{Content: `literal,"label`})
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("f3"))
	chooseCreation(m, "account ID")
	m.Update(tea.PasteMsg{Content: "account-7"})
	m.Update(key.Replay("enter"))
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if calls != 0 || m.creation.markdown != body || !strings.Contains(m.View().Content, "Review new issue") {
		t.Fatal("creation lost the body or submitted before review")
	}
	if !strings.Contains(m.dialogs.Top().(*creationReview).Content(200), "body.\n\nLong") {
		t.Fatal("creation review flattened authored paragraphs")
	}
	m.Update(key.Replay("enter"))
	if calls != 0 || m.creationEditor == nil {
		t.Fatal("creation review did not default to cancellation")
	}
	_, cmd = m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	_, cmd = m.Update(key.Replay("y"))
	if calls != 0 || cmd == nil {
		t.Fatal("create executed on the update loop")
	}
	m.creation.summary = "later edits must not change the accepted request"
	applyCommand(m, cmd)
	if calls != 1 || string(sent.Fields["summary"]) != `"New literal summary"` || string(sent.Fields["labels"]) != `["literal,\"label"]` || m.query != query || !m.creation.locked || m.detailKey != "NEW-1" {
		t.Fatal("creation request or outcome lost its immutable binding")
	}
}

func TestUncertainCreationKeepsInspectableDraftWithoutResubmission(t *testing.T) {
	m := creationModel(t)
	calls := 0
	m.options.Actions.Create = func(jira.IssueWriteRequest) (envelope.IssueCreateOutput, error) {
		calls++
		return envelope.IssueCreateOutput{Attempted: true}, errors.New("response lost")
	}
	startCreation(t, m)
	m.Update(tea.PasteMsg{Content: "Inspect uncertain draft"})
	m.Update(key.Replay("tab"))
	m.Update(tea.PasteMsg{Content: "first paragraph\n\n  second paragraph"})
	m.creation.assignee = jira.NullableOf("account-7")
	m.creation.labelsSet = true
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	_, cmd = m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if calls != 1 || !m.creation.locked || !strings.Contains(m.operationText(100), "unconfirmed") {
		t.Fatal("uncertain creation was replayable or lost its receipt")
	}
	press(m, 'n')
	chooseCreation(m, "View previous draft")
	if !strings.Contains(m.View().Content, "Inspect uncertain draft") {
		t.Fatal("uncertain authored draft was not inspectable")
	}
	inspection := m.dialogs.Top().(*creationOutcomeDialog).Content(200)
	if !strings.Contains(inspection, "first paragraph\n\n  second paragraph") || !strings.Contains(inspection, "Assignee: account-7") || !strings.Contains(inspection, "explicit empty array") {
		t.Fatal("inspection lost authored line structure or native optional-field facts")
	}
	m.Update(key.Replay("ctrl+s"))
	if calls != 1 {
		t.Fatal("inspection replayed creation")
	}
	m.Update(key.Replay("esc"))
	press(m, 'n')
	chooseCreation(m, "Start a different")
	if m.creation.summary != "" || calls != 1 {
		t.Fatal("new-draft route reused uncertain authored content")
	}
}

func TestCreationDraftSurvivesCancelAndStaleReviewCannotOpen(t *testing.T) {
	m := creationModel(t)
	startCreation(t, m)
	m.Update(tea.PasteMsg{Content: "Draft title"})
	m.Update(key.Replay("esc"))
	press(m, 'n')
	if m.creationEditor == nil || m.creationEditor.editor.Results()[0].Title != "Draft title" {
		t.Fatal("cancel discarded creation draft")
	}
	_, cmd := m.Update(key.Replay("ctrl+s"))
	press(m, 'x')
	applyCommand(m, cmd)
	if m.dialogs.Active() || !strings.Contains(m.creation.notice, "superseded") {
		t.Fatal("old preparation opened review over new input")
	}
	m.options.Actions.ReadOnly = true
	m.creationEditor = nil
	if cmd := press(m, 'n'); cmd != nil || m.creationEditor != nil {
		t.Fatal("read-only mode opened creation")
	}
}

func TestCreationLabelDefaultsRemainDistinctFromAnExplicitEmptyList(t *testing.T) {
	m := creationModel(t)
	startCreation(t, m)
	if m.creation.labelsSet {
		t.Fatal("untouched labels became authored")
	}
	m.Update(key.Replay("f2"))
	m.Update(key.Replay("esc"))
	if m.creation.labelsSet {
		t.Fatal("canceled unchanged labels suppressed Jira defaults")
	}
	m.Update(key.Replay("f2"))
	m.Update(key.Replay("enter"))
	if !m.creation.labelsSet || len(m.creation.labels) != 0 {
		t.Fatal("explicitly applying an empty list lost its authored meaning")
	}
	m.Update(tea.PasteMsg{Content: "Explicit empty labels"})
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	prepared := m.dialogs.Top().(*creationReview).prepared
	if !prepared.input.Labels.Set || prepared.input.Labels.Null || string(prepared.request.Fields["labels"]) != "[]" {
		t.Fatal("empty labels reached review as omitted or null")
	}
}

func TestCreationCanChooseAnotherProjectAfterTypeFailureOrCancellation(t *testing.T) {
	m := creationModel(t)
	read := m.options.Actions.CreateTypes
	m.options.Actions.CreateTypes = func(string) (map[string]json.RawMessage, error) { return nil, errors.New("type metadata unavailable") }
	applyCommand(m, press(m, 'n'))
	chooseCreation(m, "DEMO")
	if m.creation.project.ID != "" || m.creationEditor != nil {
		t.Fatal("type failure trapped creation in an unusable project")
	}
	applyCommand(m, press(m, 'n'))
	if m.dialogs.Top().(*pickerDialog).kind != "create-project" {
		t.Fatal("draft did not recover to the project chooser")
	}
	m.options.Actions.CreateTypes = read
	chooseCreation(m, "DEMO")
	m.Update(key.Replay("esc"))
	applyCommand(m, press(m, 'n'))
	if m.dialogs.Top().(*pickerDialog).kind != "create-project" {
		t.Fatal("canceling type selection prevented changing project")
	}
}

func TestCreationCanDeliberatelyDiscardAnUnsubmittedDraft(t *testing.T) {
	m := creationModel(t)
	startCreation(t, m)
	m.Update(tea.PasteMsg{Content: "Keep until confirmed"})
	m.Update(key.Replay("f5"))
	m.Update(key.Replay("enter"))
	if m.creationEditor == nil || m.creation.summary != "Keep until confirmed" {
		t.Fatal("discard did not default to keeping the draft")
	}
	m.Update(key.Replay("f5"))
	_, cmd := m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if m.creationEditor != nil || m.creation.summary != "" || m.dialogs.Top().(*pickerDialog).kind != "create-project" {
		t.Fatal("explicit discard did not start a clean draft")
	}
}

func TestCreationEditorGeometryAndCursorAtSupportedSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m := creationModel(t)
		m.Update(size)
		startCreation(t, m)
		m.Update(tea.PasteMsg{Content: "Wide 界 title"})
		view := m.View()
		again := m.View()
		if view.Cursor == nil || again.Cursor == nil || *view.Cursor != *again.Cursor || view.Cursor.X < 2 || view.Cursor.X >= size.Width || view.Cursor.Y >= size.Height {
			t.Fatalf("editor caret drifted or escaped %dx%d", size.Width, size.Height)
		}
		if !strings.Contains(view.Content, "ctrl+s review · esc") {
			t.Fatal("creation review/back controls were clipped")
		}
		for _, line := range strings.Split(view.Content, "\n") {
			if xansi.WcWidth.StringWidth(line) > size.Width {
				t.Fatal("creation editor widened frame")
			}
		}
		m.Update(key.Replay("tab"))
		m.Update(tea.PasteMsg{Content: "Body"})
		m.Update(key.Replay("f2"))
		if !strings.Contains(m.View().Content, "enter keep") {
			t.Fatal("creation labels lost controls")
		}
	}
}
