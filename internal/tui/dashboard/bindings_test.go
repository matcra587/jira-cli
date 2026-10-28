package dashboard

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

func boundModel(t *testing.T, keys map[string][]string) *Model {
	t.Helper()
	m := New(t.Context(), fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(nil, true, ""), nil
	}}, Options{Workspace: config.Workspace{Keys: keys}})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	applyCommand(m, m.Init())
	return m
}

func TestWorkspaceBindingsRouteEffectiveKeysAndPreserveTextOwnership(t *testing.T) {
	m := boundModel(t, map[string][]string{"view-next": {"u"}, "settings": {"f7"}, "quit": {"f8"}})
	m.Update(key.Replay("tab"))
	if m.activeViewID() != "my-work" {
		t.Fatal("old shortcut remained active after rebind")
	}
	press(m, '/')
	m.input.SetValue("")
	press(m, 'u')
	if m.input.Value() != "u" || m.activeViewID() != "my-work" {
		t.Fatal("main-view binding stole query text input")
	}
	m.Update(key.Replay("esc"))
	press(m, 'u')
	if !m.emptySearch() {
		t.Fatal("configured shortcut did not switch views")
	}
	m.Update(key.Replay("f7"))
	if m.settings == nil {
		t.Fatal("configured settings shortcut did not open settings")
	}
	_, quit := m.Update(key.Replay("ctrl+c"))
	if quit == nil {
		t.Fatal("reserved terminal exit was lost inside settings")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("terminal exit changed type")
	}
}

func TestWorkspaceBindingsDriveMenuHelpAndVisibleHints(t *testing.T) {
	m := boundModel(t, map[string][]string{"settings": {"f7"}, "commands": {"f9"}, "quit": {"f8"}, "search": {"f6"}, "details": {"f10"}})
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if !strings.Contains(m.hints(), "f8") || strings.Contains(m.hints(), "q quit") {
		t.Fatal("footer displayed stale exit binding")
	}
	m.Update(key.Replay("tab"))
	if !strings.Contains(m.View().Content, "f6 or f10") {
		t.Fatal("idle Search displayed stale activation keys")
	}
	m.Update(key.Replay("f9"))
	m.Update(tea.PasteMsg{Content: "Settings"})
	menu := m.dialogs.Top().(*commandDialog)
	if menu.menu.Query() != "Settings" {
		t.Fatal("command menu ignored pasted filter text")
	}
	entry, ok := menu.menu.Selected()
	if !ok || entry.Key != "f7" {
		t.Fatal("command menu displayed a stale binding")
	}
	_, command := m.Update(key.Replay("enter"))
	applyCommand(m, command)
	if m.settings == nil {
		t.Fatal("command menu replay ignored the effective binding")
	}
	m.Update(key.Replay("esc"))
	press(m, '?')
	help, ok := m.dialogs.Top().(*keyHelpDialog)
	if !ok {
		t.Fatal("help did not use the searchable shared overlay")
	}
	m.Update(tea.PasteMsg{Content: "Settings"})
	entry, ok = help.menu.Selected()
	if !ok || entry.Key != "f7" {
		t.Fatal("help did not use the registry binding")
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != help {
		t.Fatal("help unexpectedly executed a highlighted action")
	}
}

func TestWorkspaceQuitBindingWaitsForAnOperation(t *testing.T) {
	m := boundModel(t, map[string][]string{"quit": {"f8"}})
	m.actionBusy = true
	if _, command := m.Update(key.Replay("f8")); command != nil || !strings.Contains(m.notice, "Wait") && !strings.Contains(m.notice, "wait") {
		t.Fatal("rebound quit bypassed operation admission")
	}
	if command := m.runKey(key.Replay("q")); command != nil {
		t.Fatal("palette quit bypassed operation admission")
	}
}

func TestWorkspaceQuitBindingRetainsCreationEditorTextDuringPublication(t *testing.T) {
	m := boundModel(t, map[string][]string{"quit": {"u"}})
	m.creation = &creationDraft{}
	m.showCreationEditor()
	m.localSaveBusy = true
	m.Update(key.Replay("u"))
	if got := m.creationEditor.editor.Results()[0].Title; got != "u" {
		t.Fatalf("quit admission stole creation text: got %q", got)
	}
	_, command := m.Update(key.Replay("ctrl+c"))
	if command == nil {
		t.Fatal("reserved exit was lost inside creation editor")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatal("reserved exit changed type")
	}
}

func TestWorkspaceBindingsHandlePrintedShiftedTextAndPreviewEndpoints(t *testing.T) {
	m := boundModel(t, map[string][]string{"view-next": {"A"}, "top": {"f6"}, "bottom": {"f7"}})
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModShift, Text: "A"})
	if !m.emptySearch() {
		t.Fatal("printed shifted character did not match its canonical binding")
	}
	m.previewFocused = true
	m.preview.SetContent(strings.Repeat("Scrollable line\n", 100))
	m.preview.SetYOffset(20)
	m.Update(key.Replay("f6"))
	if m.preview.YOffset() != 0 {
		t.Fatal("rebound preview top action did nothing")
	}
	m.Update(key.Replay("f7"))
	if !m.preview.AtBottom() {
		t.Fatal("rebound preview bottom action did nothing")
	}
}

func TestWorkspaceHierarchyGuidanceUsesEffectiveBindings(t *testing.T) {
	m := boundModel(t, map[string][]string{"children": {"f5"}, "parent": {"f6"}, "hierarchy-back": {"f7"}, "hierarchy": {"f8"}, "view-next": {"f9"}})
	m.hierarchyHome()
	if !strings.Contains(m.notice, "f5 children") || !strings.Contains(m.hierarchyBreadcrumb(), "f6 parent") {
		t.Fatal("hierarchy roots advertised stale shortcuts")
	}
	m.hierarchy.back = nil
	m.hierarchyBack()
	if !strings.Contains(m.notice, "f9 to switch views") {
		t.Fatal("hierarchy history advertised a stale view shortcut")
	}
	m.loading = false
	m.hierarchy.location = &hierarchyLocation{read: core.IssueHierarchyRead{Nodes: []core.IssueHierarchyNode{{ID: "1", Key: "EPIC-1", Type: "Epic"}}}}
	m.rebuildRows()
	view := m.listView()
	for _, hint := range []string{"f6 parent", "f7 back", "f8 roots"} {
		if !strings.Contains(view, hint) {
			t.Fatal("empty hierarchy advertised a stale shortcut", hint)
		}
	}
}
