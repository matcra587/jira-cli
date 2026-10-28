package dashboard

import (
	"errors"
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestThemePickerHighlightsPreviewWithoutAccepting(t *testing.T) {
	styled := cli.StyleEnabled()
	cli.SetStyleEnabled(true)
	t.Cleanup(func() { cli.SetStyleEnabled(styled) })
	m := model(t)
	restore, err := cli.PreviewTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	original := cli.ResolvedTheme()
	m.openSettings()
	name := m.themeName
	m.pickTheme()
	p := m.dialogs.Top().(*pickerDialog)
	m.Update(tea.PasteMsg{Content: "light"})
	preview := cli.ResolvedTheme()
	if preview == original || p.previewedTheme != p.value() || p.menu.Query() != "light" {
		t.Fatal("filter highlight did not preview a palette while retaining the query")
	}
	if !strings.Contains(p.menu.View(), preview.Blue.Render("/ light")) {
		t.Fatal("the open picker retained its old query color")
	}
	if m.themeName != name || m.settingsChanged() || m.dialogs.Top() != p {
		t.Fatal("highlight accepted a theme or replaced the open picker")
	}
	m.Update(key.Replay("down"))
	if p.menu.CursorLine() <= 2 || cli.ResolvedTheme() == preview || p.previewedTheme != p.value() {
		t.Fatal("arrow navigation did not preview the selected theme or retain its cursor")
	}
	m.Update(tea.PasteMsg{Content: "doesnotexist"})
	if _, ok := p.menu.Selected(); ok {
		t.Fatal("fixture should have no matching theme")
	}
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != original || m.themeName != name || m.settingsChanged() {
		t.Fatal("canceling the picker did not restore the exact prior palette")
	}
}

func TestEnvironmentThemeCannotOpenPreviewPicker(t *testing.T) {
	m := model(t)
	m.options.SettingSources = map[string]string{"theme": "env"}
	m.openSettings()
	m.settings.input.SetValue("Theme")
	m.rebuildSettings()
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != nil || m.settingsChanged() || !strings.Contains(m.settings.notice, "environment") {
		t.Fatal("theme preview bypassed environment ownership")
	}
}

func TestThemePickerAcceptAndNestedCancel(t *testing.T) {
	m := model(t)
	restore, err := cli.PreviewTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	original := cli.ResolvedTheme()
	m.openSettings()
	m.pickTheme()
	m.Update(tea.PasteMsg{Content: "light"})
	p := m.dialogs.Top().(*pickerDialog)
	selected := p.value()
	m.Update(key.Replay("enter"))
	accepted := cli.ResolvedTheme()
	if m.themeName != selected || !m.settings.themeDirty || m.dialogs.Top() != nil {
		t.Fatal("Enter did not accept the highlighted theme into the settings draft")
	}
	m.pickTheme()
	m.Update(tea.PasteMsg{Content: "dark"})
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != accepted || m.themeName != selected || !m.settings.themeDirty {
		t.Fatal("picker cancellation discarded the previously accepted settings draft")
	}
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != original {
		t.Fatal("settings cancellation did not restore its original palette")
	}
}

func TestSettingsPreviewCancelAndSearchOwnInput(t *testing.T) {
	m := model(t, issue("DEMO-1", "Retained selection"))
	query, selected := m.query, m.selectedKey()
	if !m.split() {
		t.Fatal("fixture should start with a split preview")
	}
	press(m, ',')
	m.Update(key.Replay("enter"))
	if m.split() || !m.settingsChanged() {
		t.Fatal("setting did not preview on the dashboard")
	}
	press(m, 'p')
	if !m.settings.preview || !strings.Contains(xansi.Strip(m.View().Content), "Previewing display settings") {
		t.Fatal("draft display settings could not be previewed on the issue page")
	}
	m.Update(key.Replay("esc"))
	if m.settings == nil || m.settings.preview || !m.settingsChanged() {
		t.Fatal("returning from preview discarded the settings draft")
	}
	press(m, '/')
	m.Update(tea.PasteMsg{Content: "priority"})
	if len(m.settings.visible) != 1 || m.settings.visible[0].key != "tui_priority" {
		t.Fatal("search did not narrow settings")
	}
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("space"))
	if m.enabled("tui_priority") {
		t.Fatal("filtered toggle targeted a different setting")
	}
	press(m, 'q')
	if m.settings == nil || m.query != query || m.selectedKey() != selected {
		t.Fatal("settings input leaked into dashboard navigation")
	}
	m.Update(key.Replay("esc"))
	if m.settings != nil || !m.split() || !m.enabled("tui_priority") {
		t.Fatal("escape failed to discard appearance previews")
	}
}

func TestSettingsSaveSuccessFailureAndEnvironmentOverride(t *testing.T) {
	m := model(t)
	m.options.SettingSources = map[string]string{"tui_preview": "env"}
	calls := 0
	fail := true
	m.options.SaveSettings = func(values map[string]string) error {
		calls++
		if !maps.Equal(values, map[string]string{"tui_status": "false"}) {
			t.Fatalf("save invented declarations: %v", values)
		}
		if fail {
			return errors.New("snapshot changed")
		}
		return nil
	}
	press(m, ',')
	m.Update(key.Replay("enter"))
	if !m.enabled("tui_preview") || m.settingsChanged() || !strings.Contains(m.settings.notice, "environment") {
		t.Fatal("environment override was editable")
	}
	selectAuditSetting(t, m, "tui_status")
	m.Update(key.Replay("enter"))
	_, cmd := m.Update(key.Replay("ctrl+s"))
	if cmd == nil || calls != 0 {
		t.Fatal("save was not deferred to a Tea command")
	}
	m.Update(key.Replay("esc"))
	if m.settings == nil {
		t.Fatal("save lost its owner before completion")
	}
	m.Update(cmd())
	if calls != 1 || !m.settingsChanged() || !strings.Contains(m.settings.notice, "Could not save") {
		t.Fatal("failed save reported clean settings")
	}
	fail = false
	_, cmd = m.Update(key.Replay("ctrl+s"))
	m.Update(cmd())
	if calls != 2 || m.settingsChanged() || m.settings.sources["tui_status"] != "file" {
		t.Fatal("successful save did not advance baseline")
	}
	m.Update(key.Replay("esc"))
	if m.enabled("tui_status") {
		t.Fatal("closing settings reverted a saved value")
	}
}

func TestSettingsFramesKeepSaveAndBackHintsVisible(t *testing.T) {
	m := model(t)
	for _, size := range []tea.WindowSizeMsg{{Width: 159, Height: 45}, {Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		m.Update(size)
		m.openSettings()
		view := m.View().Content
		plain := xansi.Strip(view)
		if !strings.Contains(plain, "ctrl+s") || !strings.Contains(plain, "esc") || !strings.Contains(plain, "Settings") || !strings.Contains(plain, "› Show issue") {
			t.Fatalf("settings lost primary controls at %dx%d:\n%s", size.Width, size.Height, plain)
		}
		lines := strings.Split(view, "\n")
		if len(lines) != size.Height {
			t.Fatalf("settings height = %d, want %d", len(lines), size.Height)
		}
		for _, line := range lines {
			if xansi.WcWidth.StringWidth(line) != size.Width {
				t.Fatal("settings exceeded frame width")
			}
		}
	}
}

func TestPresetResetPreservesCachedCustomLabelAndRestoresPalette(t *testing.T) {
	m := model(t)
	restore, err := cli.PreviewTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	original := cli.ResolvedTheme()
	m.openSettings()
	const cachedLabel = "Custom label retained for advanced path edits"
	m.settings.themeLabel = cachedLabel
	m.settings.values["theme_path"] = "custom.toml"

	m.setPreviewTheme("light")
	resetSelection := m.themeName == "light" && m.settings.values["theme_path"] == ""
	if !resetSelection || !m.settings.themeDirty {
		t.Fatal("preset reset did not retain the existing base/path update")
	}
	if m.settings.themeLabel != cachedLabel {
		t.Fatal("preset reset changed the cached custom label")
	}
	accepted := cli.ResolvedTheme()
	m.setPreviewTheme("not-a-preset")
	retainedSelection := m.themeName == "light" && m.settings.themeLabel == cachedLabel
	if cli.ResolvedTheme() != accepted || !retainedSelection {
		t.Fatal("invalid preset changed the accepted state")
	}
	m.Update(key.Replay("esc"))
	if cli.ResolvedTheme() != original {
		t.Fatal("canceling the preset reset did not restore the original palette")
	}
}

func TestSettingsRefusalsPreserveDraftAndInvocationOwnership(t *testing.T) {
	m := model(t)
	m.options.SettingSources = map[string]string{"default_project": "flag"}
	m.openSettings()
	m.settings.input.SetValue("Default project")
	m.rebuildSettings()
	m.Update(key.Replay("d"))
	if m.settings.editor != nil || m.settingsChanged() || !strings.Contains(m.settings.notice, "invocation flag") {
		t.Fatal("reset bypassed the invocation-owned project")
	}
	m.Update(key.Replay("ctrl+s"))
	if m.settings.notice != "No unsaved changes." {
		t.Fatal("clean draft did not take precedence over unavailable saving")
	}
	m.preferences["tui_status"] = !m.preferences["tui_status"]
	m.Update(key.Replay("ctrl+s"))
	if m.settings.saving || m.settings.notice != "Saving is unavailable in this session." ||
		!m.settingsChanged() {
		t.Fatal("unavailable saving consumed or locked the draft")
	}
	m.Update(key.Replay("ctrl+r"))
	if !strings.Contains(m.settings.notice, "no runtime factory") || !m.settingsChanged() {
		t.Fatal("unavailable runtime did not retain the unsaved draft")
	}
}

func TestSettingsSearchWheelRetainsSearchUntilDownAccepts(t *testing.T) {
	m := model(t)
	m.openSettings()
	m.Update(key.Replay("/"))
	m.Update(tea.PasteMsg{Content: "tui"})
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if !m.settings.searching || m.settings.input.Value() != "tui" {
		t.Fatal("scrolling consumed the active settings search")
	}
	selected, ok := m.settings.rows.Selected()
	if !ok || selected == 0 {
		t.Fatal("search scrolling did not move selection")
	}
	m.Update(key.Replay("down"))
	next, ok := m.settings.rows.Selected()
	if m.settings.searching || !ok || next != selected {
		t.Fatal("accepting search also navigated the selected setting")
	}
}
