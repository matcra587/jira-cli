package dashboard

import (
	"errors"
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
)

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
	m.Update(key.Replay("down"))
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
		if !strings.Contains(plain, "ctrl+s") || !strings.Contains(plain, "esc") || !strings.Contains(plain, "Settings") || !strings.Contains(plain, "› Side-by-side") {
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
