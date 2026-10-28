package dashboard

import (
	"strings"
	"testing"

	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestSelectionMarksRangeAllAndInvertWithoutMovingTheQuery(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"), issue("DEMO-3", "Third"))
	query := m.query
	m.Update(key.Replay("space"))
	if !strings.Contains(m.View().Content, "1 selected") {
		t.Fatal("Space did not visibly mark an issue")
	}
	press(m, 'j')
	press(m, 'j')
	press(m, 'x')
	if !strings.Contains(m.View().Content, "3 selected") {
		t.Fatal("range selection did not retain its anchor")
	}
	press(m, 'X')
	if strings.Contains(m.View().Content, "selected") {
		t.Fatal("invert did not clear a fully selected list")
	}
	m.Update(key.Replay("ctrl+a"))
	if !strings.Contains(m.View().Content, "3 selected") || m.query != query {
		t.Fatal("select all changed the search or missed rows")
	}
	m.Update(key.Replay("ctrl+a"))
	if strings.Contains(m.View().Content, "selected") {
		t.Fatal("select all did not toggle back to none")
	}
}

func TestSelectionRefreshRejectsDuplicatesAndPreservesNativeKeys(t *testing.T) {
	m := model(t, issue("DEMO-1", "First"), issue("DEMO-2", "Second"))
	m.Update(key.Replay("space"))
	original := m.issues[0]
	m.acceptList(task.FinishedMsg{Result: envelope.IssueListOutput{Issues: []envelope.IssueView{original, original}}})
	if m.err == nil || len(m.issues) != 2 || !m.marks["DEMO-1"] {
		t.Fatal("invalid replacement damaged accepted rows or marks")
	}
	_ = m.View()
	m.acceptList(task.FinishedMsg{Result: envelope.IssueListOutput{Issues: []envelope.IssueView{m.issues[1], original}}})
	if !m.marks["DEMO-1"] || m.selectedKey() != "DEMO-1" {
		t.Fatal("reordering lost native selection identity")
	}
	m.acceptList(task.FinishedMsg{Result: envelope.IssueListOutput{Issues: []envelope.IssueView{m.issues[0]}}})
	if len(m.markedKeys()) != 0 {
		t.Fatal("replacement retained a mark absent from the accepted list")
	}
	_ = m.View()
}
