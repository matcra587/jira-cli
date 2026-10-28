package dashboard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func editModel(t *testing.T) *Model {
	t.Helper()
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	m.options.Actions.Editable = func(key string) (*jira.IssueBean, error) {
		id := "101"
		if key == "DEMO-2" {
			id = "102"
		}
		bean := issue(key, "Current summary")
		bean.ID = jira.NullableOf(id)
		bean.Fields.Value.Labels = jira.NullableOf([]string{"literal,comma"})
		return &bean, nil
	}
	m.options.Actions.Write = func(string, string, string) (core.IssueActionReceipt, error) {
		return core.IssueActionReceipt{Applied: true, Attempted: true}, nil
	}
	return m
}

func TestSummaryReviewDefaultsCancelAndBindsImmutableTarget(t *testing.T) {
	m := editModel(t)
	var target, value string
	m.options.Actions.Write = func(kind, id, text string) (core.IssueActionReceipt, error) {
		if kind != "summary" {
			t.Fatal("wrong edit field")
		}
		target, value = id, text
		return core.IssueActionReceipt{Applied: true, Attempted: true}, nil
	}
	press(m, 'e')
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	m.Update(key.Replay("ctrl+u"))
	m.Update(tea.PasteMsg{Content: "New, literal summary"})
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("enter"))
	if target != "" || m.dialogs.Active() {
		t.Fatal("field replacement did not default to cancellation")
	}
	press(m, 'e')
	_, cmd = m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.dialogs.Top().(*summaryDialog).value() != "New, literal summary" {
		t.Fatal("canceled review lost the session draft")
	}
	m.Update(key.Replay("enter"))
	_, cmd = m.Update(key.Replay("y"))
	if target != "" || cmd == nil {
		t.Fatal("write ran before scheduling accepted review")
	}
	press(m, 'j')
	applyCommand(m, cmd)
	if target != "101" || value != "New, literal summary" {
		t.Fatal("navigation redirected the summary replacement")
	}
	if _, exists := m.fieldDrafts["101:summary"]; exists {
		t.Fatal("acknowledged summary retained a replayable draft")
	}
}

func TestLabelsKeepLiteralValuesClearExplicitlyAndRetainUncertainty(t *testing.T) {
	m := editModel(t)
	var target string
	var labels []string
	m.options.Actions.Write = func(kind, id, value string) (core.IssueActionReceipt, error) {
		if kind != "labels" {
			t.Fatal("wrong field")
		}
		target = id
		if err := json.Unmarshal([]byte(value), &labels); err != nil {
			t.Fatal(err)
		}
		return core.IssueActionReceipt{Attempted: true}, errors.New("response lost")
	}
	applyCommand(m, press(m, 'l'))
	m.Update(tea.PasteMsg{Content: `quote"and,comma`})
	m.Update(key.Replay("tab"))
	m.Update(key.Replay("enter"))
	_, cmd := m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if target != "101" || len(labels) != 2 || labels[0] != "literal,comma" || labels[1] != `quote"and,comma` {
		t.Fatal("labels were split or retargeted")
	}
	if len(m.labelDrafts["101"].labels) != 2 || !strings.Contains(m.notice, "Request attempted") {
		t.Fatal("uncertain label edit lost its draft or receipt")
	}
	applyCommand(m, press(m, 'l'))
	m.Update(key.Replay("tab"))
	press(m, 'x')
	press(m, 'x')
	m.Update(key.Replay("tab"))
	m.Update(key.Replay("enter"))
	if !strings.Contains(m.View().Content, "All labels will be removed") {
		t.Fatal("empty-label review hid its replacement effect")
	}
	_, cmd = m.Update(key.Replay("y"))
	applyCommand(m, cmd)
	if labels == nil || len(labels) != 0 {
		t.Fatal("clear labels did not send an explicit empty native array")
	}
}

func TestUnmodifiedEditorsDoNotOverrideFreshReadsAndPendingLabelsSurvive(t *testing.T) {
	m := editModel(t)
	read := m.options.Actions.Editable
	remoteChanged := false
	m.options.Actions.Editable = func(key string) (*jira.IssueBean, error) {
		bean, err := read(key)
		if remoteChanged && bean != nil {
			bean.Fields.Value.Summary = jira.NullableOf("Remote summary")
			bean.Fields.Value.Labels = jira.NullableOf([]string{"remote,label"})
		}
		return bean, err
	}
	press(m, 'e')
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	m.Update(key.Replay("enter"))
	applyCommand(m, press(m, 'l'))
	m.Update(key.Replay("tab"))
	m.Update(key.Replay("enter"))
	if len(m.fieldDrafts) != 0 || len(m.labelDrafts) != 0 {
		t.Fatal("unchanged editor created a stale replacement draft")
	}
	applyCommand(m, press(m, 'l'))
	m.Update(tea.PasteMsg{Content: `pending,"literal`})
	m.Update(key.Replay("esc"))
	remoteChanged = true
	press(m, 'e')
	_, cmd = m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.dialogs.Top().(*summaryDialog).value() != "Remote summary" {
		t.Fatal("unmodified summary hid a fresh value")
	}
	m.Update(key.Replay("esc"))
	applyCommand(m, press(m, 'l'))
	d := m.dialogs.Top().(*labelsDialog)
	if d.line.Value() != `pending,"literal` || len(d.labels) != 1 || d.labels[0] != "remote,label" {
		t.Fatal("pending label was lost or silently froze the unmodified old label set")
	}
	m.Update(key.Replay("tab"))
	m.Update(key.Replay("enter"))
	var labels []string
	if err := json.Unmarshal([]byte(m.dialogs.Top().(*writeReview).value), &labels); err != nil || len(labels) != 2 || labels[1] != `pending,"literal` {
		t.Fatal("review dropped pending input when list focus was active")
	}
}

func TestEditReadsAndDraftsDoNotCrossTargets(t *testing.T) {
	m := editModel(t)
	cmd := press(m, 'l')
	press(m, 'j')
	applyCommand(m, cmd)
	if m.dialogs.Active() || strings.Contains(m.notice, "Loading editor") {
		t.Fatal("stale edit read opened an old target")
	}
	applyCommand(m, press(m, 'l'))
	m.Update(tea.PasteMsg{Content: "second draft"})
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("esc"))
	press(m, 'k')
	applyCommand(m, press(m, 'l'))
	if len(m.dialogs.Top().(*labelsDialog).labels) != 1 {
		t.Fatal("labels draft crossed immutable issue identity")
	}
}

func TestEditFormsFitSupportedTerminalSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m := editModel(t)
		m.Update(size)
		applyCommand(m, press(m, 'l'))
		if view := m.View().Content; strings.Contains(view, "terminal too narrow") || !strings.Contains(view, "enter next") {
			t.Fatalf("label controls missing at %dx%d", size.Width, size.Height)
		}
		m.Update(tea.PasteMsg{Content: "new"})
		m.Update(key.Replay("tab"))
		m.Update(key.Replay("enter"))
		if view := m.View().Content; strings.Contains(view, "terminal too narrow") || !strings.Contains(view, "enter confirm") {
			t.Fatalf("review controls missing at %dx%d", size.Width, size.Height)
		}
	}
}

func TestLabelNavigationAndUnsupportedSummaryNeverChangeAnotherValue(t *testing.T) {
	m := editModel(t)
	read := m.options.Actions.Editable
	m.options.Actions.Editable = func(key string) (*jira.IssueBean, error) {
		bean, err := read(key)
		if bean != nil {
			bean.Fields.Value.Labels = jira.NullableOf([]string{"first,label", "second"})
			bean.Fields.Value.Summary = jira.NullableOf("native\nsummary")
		}
		return bean, err
	}
	applyCommand(m, press(m, 'l'))
	m.Update(key.Replay("tab"))
	m.Update(key.Replay("down"))
	press(m, 'x')
	d := m.dialogs.Top().(*labelsDialog)
	if len(d.labels) != 1 || d.labels[0] != "first,label" {
		t.Fatal("list navigation removed an unselected label")
	}
	m.Update(key.Replay("esc"))
	press(m, 'e')
	_, cmd := m.Update(key.Replay("enter"))
	applyCommand(m, cmd)
	if m.dialogs.Active() || !strings.Contains(m.notice, "cannot preserve") {
		t.Fatal("summary editor silently normalized native text")
	}
	m.options.Actions.ReadOnly = true
	if cmd := press(m, 'l'); cmd != nil || m.dialogs.Active() {
		t.Fatal("read-only mode opened an editable issue form")
	}
}
