package dashboard

import (
	"strings"
	"testing"

	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestWorkspaceBindingCaptureConflictAlternativeResetAndCancel(t *testing.T) {
	m := model(t)
	m.openSettings()
	m.openWorkspaceSettings()
	chooseWorkspace(t, m, "Keybindings")
	chooseWorkspace(t, m, "Next view")
	chooseWorkspace(t, m, "Replace shortcut")
	m.Update(key.Replay("e"))
	capture, ok := m.dialogs.Top().(*bindingCapture)
	if !ok || !strings.Contains(capture.notice, "Edit issue") || m.settingsChanged() {
		t.Fatal("conflicting capture partially changed the draft or lost its explanation")
	}
	m.Update(key.Replay("f6"))
	owner, ok := m.dialogs.Top().(*workspaceDialog)
	if !ok || owner.working.Keys["view-next"][0] != "f6" {
		t.Fatal("valid capture was not kept in settings draft")
	}
	chooseWorkspace(t, m, "Add alternative")
	m.Update(key.Replay("f7"))
	if len(owner.working.Keys["view-next"]) != 2 {
		t.Fatal("adding an alternative replaced the existing binding")
	}
	chooseWorkspace(t, m, "Reset default")
	if _, ok := owner.working.Keys["view-next"]; ok {
		t.Fatal("reset did not remove the override")
	}
	chooseWorkspace(t, m, "Replace shortcut")
	m.Update(key.Replay("esc"))
	if m.dialogs.Top() != owner {
		t.Fatal("canceling capture left the input owner open")
	}
	workspace, err := config.ParseWorkspace(m.settings.values["tui_workspace"])
	if err != nil || len(workspace.Keys) != 0 {
		t.Fatal("canceled capture changed settings", err)
	}
}
