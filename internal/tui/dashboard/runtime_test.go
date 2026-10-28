package dashboard

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestSettingsSaveReplacesRuntimeAndRejectsOldMessages(t *testing.T) {
	old := model(t, issue("DEMO-1", "Retained"))
	old.options.RuntimeScope = core.JiraScope{Site: old.site, Account: "person-a"}
	old.drafts["DEMO-1"] = "local comment"
	old.fieldDrafts["101:summary"] = "local summary"
	old.creation = &creationDraft{summary: "unsubmitted issue"}
	stopped, applied, saves := false, false, 0
	old.options.StopRuntime = func() { stopped = true }
	old.options.SaveSettings = func(map[string]string) error { saves++; return nil }
	next := model(t)
	next.options.RuntimeScope = old.options.RuntimeScope
	next.options.ApplyRuntime = func() { applied = true }
	old.options.ReloadSettings = func() (*Model, error) {
		if stopped {
			t.Fatal("old runtime canceled before a replacement was ready")
		}
		return next, nil
	}
	oldMessage := runtimeCommand(old, old.tasks.Start(task.Spec{Scope: listScope, Run: func() (any, error) {
		return nil, errors.New("stale old connection")
	}}))()
	session := WithRuntime(old).(*runtimeModel)
	old.openSettings()
	old.preferences["tui_status"] = false
	_, cmd := session.Update(key.Replay("ctrl+s"))
	_, load := session.Update(cmd())
	if session.current != next || load == nil || saves != 1 || !stopped || !applied {
		t.Fatal("successful save did not adopt and initialize one replacement")
	}
	if next.drafts["DEMO-1"] != "local comment" || next.fieldDrafts["101:summary"] != "local summary" || next.creation.summary != "unsubmitted issue" {
		t.Fatal("same-scope reload lost local work")
	}
	session.Update(oldMessage)
	if next.err != nil || !next.loading {
		t.Fatal("an old runtime result crossed the replacement boundary")
	}
	// Batches must retain the origin tag for each child; terminal controls
	// remain native Tea messages rather than becoming ordinary model input.
	batch := runtimeCommand(old, tea.Batch(func() tea.Msg { return task.FinishedMsg{} }, func() tea.Msg { return detailDue{key: "DEMO-1"} }))().(tea.BatchMsg)
	for _, child := range batch {
		if child().(runtimeMessage).origin != old {
			t.Fatal("batch lost its originating runtime")
		}
	}
	if _, ok := runtimeCommand(old, tea.Quit)().(tea.QuitMsg); !ok {
		t.Fatal("runtime wrapper swallowed terminal control")
	}
}

func TestSavedReloadFailureBlocksWritesPersistsAndRetriesWithoutSaving(t *testing.T) {
	m := model(t)
	saves, reloads := 0, 0
	m.options.SaveSettings = func(map[string]string) error { saves++; return nil }
	m.options.ReloadSettings = func() (*Model, error) { reloads++; return nil, errors.New("replacement credentials unavailable") }
	m.openSettings()
	m.preferences["tui_status"] = false
	_, cmd := m.Update(key.Replay("ctrl+s"))
	m.Update(cmd())
	if saves != 1 || reloads != 1 || m.settingsChanged() || m.reloadFailure == "" || !m.options.Actions.ReadOnly {
		t.Fatal("saved publication was confused with failed adoption")
	}
	m.Update(key.Replay("esc"))
	m.notice = "ordinary navigation notice"
	if !strings.Contains(m.View().Content, "writes blocked") {
		t.Fatal("reload failure disappeared outside settings")
	}
	next := model(t)
	m.options.ReloadSettings = func() (*Model, error) { reloads++; return next, nil }
	m.openSettings()
	_, cmd = m.Update(key.Replay("ctrl+r"))
	replacement, _ := m.Update(cmd())
	if replacement != next || saves != 1 || reloads != 2 || next.reloadFailure != "" {
		t.Fatal("reload retry replayed publication or retained a stale failure")
	}
}

func TestRuntimeScopeChangesParkAndRestoreLocalWork(t *testing.T) {
	a := model(t)
	a.options.RuntimeScope = core.JiraScope{Site: "https://a.invalid", Account: "person-a"}
	a.drafts["DEMO-1"] = "private to a"
	a.openSettings()
	b := model(t)
	b.options.RuntimeScope = core.JiraScope{Site: "https://a.invalid", Account: "person-b"}
	a.adoptRuntime(b)
	if len(b.drafts) != 0 {
		t.Fatal("account change carried a foreign draft into the new workspace")
	}
	b.drafts["DEMO-1"] = "private to b"
	again := model(t)
	again.options.RuntimeScope = a.options.RuntimeScope
	b.adoptRuntime(again)
	if again.drafts["DEMO-1"] != "private to a" {
		t.Fatal("returning to a scope did not restore its original work")
	}
}

func TestRuntimeChangesWaitForWritesAndRejectUnsavedReload(t *testing.T) {
	m := model(t)
	m.options.ReloadSettings = func() (*Model, error) { t.Fatal("reload admitted at an unsafe boundary"); return nil, nil }
	m.options.SaveSettings = func(map[string]string) error { t.Fatal("save admitted while writing"); return nil }
	m.openSettings()
	m.preferences["tui_status"] = false
	m.actionBusy = true
	if _, cmd := m.Update(key.Replay("ctrl+s")); cmd != nil || !strings.Contains(m.settings.notice, "Wait") {
		t.Fatal("saving was not blocked during a Jira write")
	}
	m.Update(key.Replay("ctrl+r"))
	m.actionBusy = false
	if _, cmd := m.Update(key.Replay("ctrl+r")); cmd != nil || !strings.Contains(m.settings.notice, "draft") {
		t.Fatal("reload silently discarded a settings draft")
	}
}

func TestReloadChangedProjectStartsWithoutOldRowsOrMarks(t *testing.T) {
	m := model(t, issue("DEMO-1", "Old project"))
	m.options.RuntimeScope = core.JiraScope{Site: m.site}
	m.tabs.SetItems([]string{"My work", "Project", "Search"})
	m.tabs.SetActive(1)
	m.query = "project = OLD"
	m.marks = map[string]bool{"DEMO-1": true}
	m.localFilter = "Old"
	m.openSettings()
	next := model(t)
	next.options.RuntimeScope = m.options.RuntimeScope
	next.options.ProjectQuery = "project = NEW"
	next.tabs.SetItems([]string{"My work", "Project", "Search"})
	m.adoptRuntime(next)
	if next.query != "project = NEW" || len(next.issues) != 0 || len(next.marks) != 0 || next.localFilter != "" {
		t.Fatal("changed project reused old results, filters or bulk targets")
	}
	next.Update(task.FinishedMsg{Scope: listScope, Gen: next.tasks.Generation(listScope), Err: errors.New("new project read refused")})
	if len(next.issues) != 0 || len(next.marks) != 0 {
		t.Fatal("failed new-query load exposed old-query targets")
	}
}

func TestSettingsReloadWaitsForSavedViewPublicationReceipt(t *testing.T) {
	m := model(t)
	m.options.SaveView = func(string, string) error { return nil }
	m.options.ReloadSettings = func() (*Model, error) { t.Fatal("reload overlapped local publication"); return nil, nil }
	m.text("Save view", "save-view", m.query, "")
	d := m.dialogs.Top().(*textDialog)
	d.line.SetValue("kept")
	_, publish := m.Update(key.Replay("enter"))
	if !m.localSaveBusy || publish == nil {
		t.Fatal("local publication did not retain a pending receipt")
	}
	m.openSettings()
	if _, cmd := m.Update(key.Replay("ctrl+r")); cmd != nil {
		t.Fatal("reload was admitted before local publication completed")
	}
	applyCommand(m, publish)
	if m.localSaveBusy || !strings.Contains(m.notice, "Saved view") {
		t.Fatal("local publication outcome was dropped")
	}
}
