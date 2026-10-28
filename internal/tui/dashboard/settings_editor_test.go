package dashboard

import (
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
)

func selectSetting(t *testing.T, m *Model, name string) {
	t.Helper()
	m.settings.input.SetValue(name)
	m.rebuildSettings()
	if len(m.settings.visible) != 1 || m.settings.visible[0].key != name {
		t.Fatalf("setting search is ambiguous for %s: %+v", name, m.settings.visible)
	}
}

func TestSettingsCatalogCoversEveryConfigKeyOnce(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range allDashboardSettings() {
		if seen[s.key] || s.name == "" || s.group == "" || s.description == "" {
			t.Fatalf("missing or duplicate setting metadata: %+v", s)
		}
		seen[s.key] = true
	}
	if len(seen) != len(config.Keys()) {
		t.Fatal("settings catalog does not cover the config schema")
	}
	for _, key := range config.Keys() {
		if !seen[key] {
			t.Fatalf("missing setting %s", key)
		}
	}
}

func TestScalarSettingsValidateRetainDraftAndPublishOnlyChanges(t *testing.T) {
	m := model(t)
	m.options.SettingValues = map[string]string{"default_project": "OLD", "timeout": "0s"}
	calls := 0
	m.options.SaveSettings = func(values map[string]string) error {
		calls++
		if !maps.Equal(values, map[string]string{"default_project": "NEW", "timeout": "30s"}) {
			t.Fatalf("save included untouched settings: %v", values)
		}
		return nil
	}
	m.openSettings()
	selectSetting(t, m, "timeout")
	m.Update(key.Replay("enter"))
	m.settings.editor.line.SetValue("bad-duration")
	m.Update(key.Replay("enter"))
	if m.settings.editor == nil || m.settingsChanged() || !strings.Contains(m.settings.notice, "duration") {
		t.Fatal("invalid duration was accepted or lost its editor")
	}
	m.settings.editor.line.SetValue("30s")
	m.Update(key.Replay("enter"))
	selectSetting(t, m, "default_project")
	m.Update(key.Replay("enter"))
	m.settings.editor.line.SetValue("NEW")
	m.Update(key.Replay("enter"))
	if calls != 0 || !m.settingsChanged() {
		t.Fatal("draft editing published early")
	}
	_, cmd := m.Update(key.Replay("ctrl+s"))
	applyCommand(m, cmd)
	if calls != 1 || m.settingsChanged() || !strings.Contains(m.settings.notice, "Restart") {
		t.Fatal("save did not advance baseline or explain startup settings")
	}
	m.Update(key.Replay("esc"))
	m.openSettings()
	if m.settings.values["default_project"] != "NEW" {
		t.Fatal("reopening settings lost the saved baseline")
	}
	selectSetting(t, m, "default_project")
	m.Update(key.Replay("enter"))
	m.settings.editor.line.SetValue("discard")
	m.Update(key.Replay("esc"))
	if m.settings.values["default_project"] != "NEW" || m.settingsChanged() {
		t.Fatal("canceling a field changed the draft")
	}
	m.Update(key.Replay("d"))
	if m.settings.values["default_project"] != "" {
		t.Fatal("default action did not clear a string setting")
	}
	m.Update(key.Replay("esc"))
	m.openSettings()
	if m.settings.values["default_project"] != "NEW" {
		t.Fatal("canceling settings lost the saved scalar")
	}
}

func TestSettingsScalarLocksAndCredentialOwnerBoundaries(t *testing.T) {
	m := model(t)
	m.options.SettingValues = map[string]string{"api_token": "synthetic-hidden", "aliases": "synthetic-hidden", "default_project": "ENV"}
	m.options.SettingSources = map[string]string{"default_project": "env"}
	m.openSettings()
	if _, exists := m.settings.values["api_token"]; exists {
		t.Fatal("secret entered settings draft")
	}
	selectSetting(t, m, "default_project")
	m.Update(key.Replay("enter"))
	if m.settings.editor != nil || !strings.Contains(m.settings.notice, "environment") {
		t.Fatal("environment-owned scalar was editable")
	}
	for _, name := range []string{"api_token", "aliases"} {
		// Exact catalog selection avoids the token-variable row matching token search.
		m.settings.input.SetValue("")
		m.rebuildSettings()
		for i, s := range m.settings.visible {
			if s.key == name {
				m.settings.rows.SetCursor(i)
			}
		}
		m.Update(key.Replay("enter"))
		if m.settings.editor != nil || m.settingsChanged() || strings.Contains(m.View().Content, "synthetic-hidden") {
			t.Fatal("specialized owner was treated as a scalar")
		}
	}
}

func TestSettingsEnumInputClearingAndFrameBounds(t *testing.T) {
	m := model(t)
	m.openSettings()
	selectSetting(t, m, "glyphs")
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("down"))
	m.Update(key.Replay("enter"))
	if m.settings.values["glyphs"] != "nerd" {
		t.Fatal("enum selection was not accepted")
	}
	selectSetting(t, m, "queries_path")
	m.Update(key.Replay("enter"))
	m.settings.editor.line.SetValue("")
	for _, text := range []string{"unsafe\npath", "unsafe\u0085path", "unsafe\ufffdpath"} {
		m.Update(tea.PasteMsg{Content: text})
		if m.settings.editor.line.Value() != "" || !strings.Contains(m.settings.notice, "single-line") {
			t.Fatal("paste silently normalized configuration input")
		}
	}
	m.Update(tea.PasteMsg{Content: "local queries"})
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 30}, {Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		m.Update(size)
		view := m.View().Content
		if len(strings.Split(view, "\n")) != size.Height || !strings.Contains(xansi.Strip(view), "cancel edit") {
			t.Fatal("editor lost its bounded frame or cancel control")
		}
		if !strings.Contains(xansi.Strip(view), "local queries") {
			t.Fatal("editor hid its typed value")
		}
		for _, line := range strings.Split(view, "\n") {
			if xansi.WcWidth.StringWidth(line) != size.Width {
				t.Fatal("editor exceeded terminal width")
			}
		}
	}
	m.Update(key.Replay("enter"))
	if m.settings.values["queries_path"] != "local queries" {
		t.Fatal("space-containing path changed")
	}
}

func TestSettingsSwitchesAndSourcesStayInColumns(t *testing.T) {
	m := model(t)
	m.options.SettingSources = map[string]string{"tui_preview": "env", "tui_status": "file"}
	for _, width := range []int{40, 80, 120} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m.openSettings()
		m.preferences["tui_status"] = false
		m.rebuildSettings()
		lines := strings.Split(xansi.Strip(m.settings.rows.View()), "\n")
		valueColumn, sourceColumn := -1, -1
		for _, line := range lines {
			marker := strings.Index(line, "[x]")
			if marker < 0 {
				marker = strings.Index(line, "[ ]")
			}
			if marker < 0 {
				continue
			}
			column := xansi.WcWidth.StringWidth(line[:marker])
			if valueColumn >= 0 && valueColumn != column {
				t.Fatalf("switches differ at width %d: %q", width, lines)
			}
			valueColumn = column
			for _, source := range []string{"env", "unsaved", "default"} {
				if at := strings.LastIndex(line, source); at >= 0 {
					column = xansi.WcWidth.StringWidth(line[:at])
					if sourceColumn >= 0 && sourceColumn != column {
						t.Fatalf("sources differ at width %d: %q", width, lines)
					}
					sourceColumn = column
				}
			}
		}
		if valueColumn < 0 || sourceColumn < 0 {
			t.Fatal("fixture did not render switches and sources")
		}
		m.Update(key.Replay("esc"))
	}
}
