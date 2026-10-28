package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

func chooseWorkspace(t *testing.T, m *Model, label string) {
	t.Helper()
	m.Update(tea.PasteMsg{Content: label})
	_, command := m.Update(key.Replay("enter"))
	applyCommand(m, command)
}

func addWorkspaceView(t *testing.T, m *Model, title, jql string) {
	t.Helper()
	chooseWorkspace(t, m, "Views")
	chooseWorkspace(t, m, "Add view")
	m.Update(tea.PasteMsg{Content: title})
	m.Update(key.Replay("enter"))
	m.Update(tea.PasteMsg{Content: jql})
	m.Update(key.Replay("ctrl+s"))
	if _, ok := m.dialogs.Top().(*workspaceDialog); !ok {
		t.Fatal("valid view did not return to workspace menu")
	}
}

func TestWorkspaceSettingsSaveRenameAndReopenLanding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	client := fixtureClient{search: func(context.Context, string, string, int) (core.IssueSearchRead, error) {
		return page(nil, true, ""), nil
	}}
	m := New(t.Context(), client, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	applyCommand(m, m.Init())
	m.options.SaveSettings = func(values map[string]string) error {
		update, err := file.PrepareSettings(values)
		if err != nil {
			return err
		}
		_, err = update.Save(t.Context())
		return err
	}
	m.options.ReloadSettings = func() (*Model, error) {
		loaded, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
		if err != nil {
			return nil, err
		}
		return New(t.Context(), client, Options{Workspace: loaded.Config.TUIWorkspace, SettingValues: loaded.Config.Values()}), nil
	}
	m.openSettings()
	m.openWorkspaceSettings()
	const query = `project = DEMO OR labels = urgent ORDER BY created DESC`
	addWorkspaceView(t, m, "Triage", query)
	menu := m.dialogs.Top().(*workspaceDialog)
	id := menu.working.Sections[0].ID
	chooseWorkspace(t, m, "Triage")
	chooseWorkspace(t, m, "Rename")
	m.Update(key.Replay("ctrl+u"))
	m.Update(tea.PasteMsg{Content: "Review queue"})
	m.Update(key.Replay("enter"))
	if menu.working.Sections[0].ID != id {
		t.Fatal("rename changed stable view identity")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	chooseWorkspace(t, m, "Startup view")
	chooseWorkspace(t, m, "Review queue")
	if menu.working.LandingView != id {
		t.Fatal("startup choice lost its stable reference")
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "# original\n" {
		t.Fatal("workspace editing published before Settings save", err)
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	_, save := m.Update(key.Replay("ctrl+s"))
	if save == nil {
		t.Fatal("settings did not admit workspace save")
	}
	got, load := m.Update(save())
	reloaded, ok := got.(*Model)
	if !ok {
		t.Fatal("save did not return reloaded runtime")
	}
	applyCommand(reloaded, load)
	if reloaded.activeViewID() != "my-work" || reloaded.workspace.Sections[0].Title != "Review queue" {
		t.Fatal("hot reload lost renamed view or forced a startup navigation")
	}
	stored, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	fresh := New(t.Context(), client, Options{Workspace: stored.Config.TUIWorkspace})
	if fresh.activeViewID() != id || fresh.query != query {
		t.Fatal("reopened runtime did not land on the saved authored query")
	}
}

func TestWorkspaceSettingsCancelAndInvalidLensRetainDraft(t *testing.T) {
	m := model(t)
	m.openSettings()
	m.openWorkspaceSettings()
	chooseWorkspace(t, m, "Lenses")
	chooseWorkspace(t, m, "Add lens")
	m.Update(tea.PasteMsg{Content: "Mine"})
	m.Update(key.Replay("enter"))
	m.Update(tea.PasteMsg{Content: "assignee = currentUser() ORDER BY key"})
	m.Update(key.Replay("ctrl+s"))
	field, ok := m.dialogs.Top().(*workspaceFieldDialog)
	if !ok || field.notice == "" || m.settingsChanged() {
		t.Fatal("invalid lens lost its editor or changed the settings draft")
	}
	field.area.SetValue("assignee = currentUser()")
	m.Update(key.Replay("ctrl+s"))
	if !m.settingsChanged() {
		t.Fatal("valid lens was not kept in the settings draft")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	if m.settings != nil || len(m.workspace.Lenses) != 0 {
		t.Fatal("canceling Settings changed the active workspace")
	}
}

func TestWorkspaceSettingsReorderRemoveAndLiteralAddID(t *testing.T) {
	w := config.Workspace{Sections: []config.WorkspaceSection{{ID: "add", Title: "Existing view", JQL: "project = ONE"}, {ID: "two", Title: "Second", JQL: "project = TWO"}}, Lenses: []config.WorkspaceSection{{ID: "add", Title: "Existing lens", JQL: "status = Open"}}, Tabs: []string{"my-work", "add", "two"}, LandingView: "add", LandingLens: "add"}
	m := model(t)
	m.options.SettingValues = map[string]string{"tui_workspace": w.JSON()}
	m.openSettings()
	m.openWorkspaceSettings()
	chooseWorkspace(t, m, "Views")
	chooseWorkspace(t, m, "Existing view")
	chooseWorkspace(t, m, "Rename")
	field, ok := m.dialogs.Top().(*workspaceFieldDialog)
	if !ok || field.field != "title" {
		t.Fatal("literal add ID collided with the Add action")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	chooseWorkspace(t, m, "Tabs")
	chooseWorkspace(t, m, "Existing view")
	chooseWorkspace(t, m, "Move earlier")
	menu := m.dialogs.Top().(*workspaceDialog)
	if menu.working.TabIDs()[0] != "add" {
		t.Fatal("tab reorder did not use stable references")
	}
	m.Update(key.Replay("esc"))
	m.Update(key.Replay("esc"))
	chooseWorkspace(t, m, "Views")
	chooseWorkspace(t, m, "Existing view")
	chooseWorkspace(t, m, "Remove")
	if len(menu.working.Sections) != 1 || menu.working.LandingView != "" || menu.working.TabIDs()[0] == "add" {
		t.Fatal("removing a view left dangling tab or startup references")
	}
	m.Update(key.Replay("esc"))
	chooseWorkspace(t, m, "Lenses")
	chooseWorkspace(t, m, "Existing lens")
	chooseWorkspace(t, m, "Remove")
	if len(menu.working.Lenses) != 0 || menu.working.LandingLens != "" {
		t.Fatal("removing a lens left its startup reference")
	}
}
