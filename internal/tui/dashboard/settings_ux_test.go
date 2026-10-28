package dashboard

import (
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
)

func TestSettingsChoiceMenuPreselectsCancelsAndKeepsOnlyValidDrafts(t *testing.T) {
	m := model(t)
	m.options.SettingValues = map[string]string{"secret_backend": "env"}
	m.openSettings()
	selectAuditSetting(t, m, "secret_backend")
	m.Update(key.Replay("enter"))
	menu := m.dialogs.Top().(*settingMenu)
	if menu.value() != "env" || m.settings.editor != nil {
		t.Fatal("enum did not open a menu with the current provider selected")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 159, Height: 45}, {Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		m.Update(size)
		plain := xansi.Strip(m.View().Content)
		if !strings.Contains(plain, "enter") || !strings.Contains(plain, "esc") {
			t.Fatal("choice menu hid its primary controls")
		}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if xansi.WcWidth.StringWidth(line) != size.Width {
				t.Fatal("choice menu escaped the terminal width")
			}
		}
	}
	m.Update(tea.PasteMsg{Content: "not-a-provider"})
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != menu || m.settingsChanged() {
		t.Fatal("an unmatched query became a provider value")
	}
	m.Update(key.Replay("esc"))
	if m.settings.values["secret_backend"] != "env" || m.settingsChanged() {
		t.Fatal("canceling a provider menu changed the draft")
	}
	m.Update(key.Replay("enter"))
	m.Update(key.Replay("down"))
	m.Update(key.Replay("enter"))
	if m.settings.values["secret_backend"] != "1password" || m.settingsChangeCount() != 1 || m.dialogs.Top() != nil {
		t.Fatal("choosing a provider did not keep exactly one draft change")
	}
}

func TestSettingsDetailsShowWholeValueAndCountDistinctChanges(t *testing.T) {
	m := model(t)
	path := "/example/" + strings.Repeat("wide-界/", 18) + "final-directory"
	m.options.SettingValues = map[string]string{"queries_path": path}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openSettings()
	selectAuditSetting(t, m, "queries_path")
	plain := xansi.Strip(m.View().Content)
	compact := strings.ReplaceAll(strings.ReplaceAll(plain, " ", ""), "\n", "")
	if !strings.Contains(compact, path) || !strings.Contains(plain, "Source:") {
		t.Fatal("selected path or source was truncated in the details area")
	}
	m.keepSetting("default_project", "NEW")
	m.preferences["tui_status"] = false
	if m.settingsChangeCount() != 2 || !strings.Contains(xansi.Strip(m.View().Content), "2 unsaved changes") {
		t.Fatal("change count did not combine scalar and switch drafts")
	}
	m.preferences["tui_status"] = true
	if m.settingsChangeCount() != 1 {
		t.Fatal("reverting a draft did not decrease its count")
	}
}

func TestSettingsExplainDisplayConstraintsOfCurrentView(t *testing.T) {
	m := model(t)
	m.openSettings()
	selectAuditSetting(t, m, "tui_assignee")
	if !strings.Contains(xansi.Strip(m.View().Content), "Hidden in My work") {
		t.Fatal("assignee switch did not explain My work suppression")
	}
	m.tabs.SetActive(1)
	if !strings.Contains(m.settingVisibility("tui_assignee"), "92 columns") || !strings.Contains(m.settingVisibility("tui_updated"), "110 columns") {
		t.Fatal("narrow split lists did not explain column thresholds")
	}
	m.preferences["tui_preview"] = false
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	if m.settingVisibility("tui_assignee") != "" || m.settingVisibility("tui_updated") != "" {
		t.Fatal("full-width visible columns retained a hidden warning")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m.preferences["tui_preview"] = true
	selectAuditSetting(t, m, "tui_preview")
	plain := xansi.Strip(m.View().Content)
	if !strings.Contains(plain, "114 × 24") || !strings.Contains(plain, "ctrl+s") || !strings.Contains(plain, "esc") {
		t.Fatal("small terminal did not preserve visibility guidance and controls")
	}
}

func TestSettingsFullDetailsKeepManagedAndURLSecretsRedacted(t *testing.T) {
	m := model(t)
	privateURL := url.URL{Scheme: "https", Host: "example.invalid", User: url.UserPassword("viewer", "concealed-marker")}
	m.options.SettingValues = map[string]string{
		"api_token": "managed-secret-marker", "aliases": "managed-secret-marker",
		"base_url": privateURL.String(),
	}
	m.openSettings()
	for _, name := range []string{"api_token", "aliases", "base_url"} {
		selectAuditSetting(t, m, name)
		plain := xansi.Strip(m.View().Content)
		if strings.Contains(plain, "managed-secret-marker") || strings.Contains(plain, "concealed-marker") {
			t.Fatal("expanded settings details exposed protected content")
		}
		if name == "api_token" && !strings.Contains(xansi.Strip(m.settings.rows.View()), "managed separately") {
			t.Fatal("managed token row implied a configured or revealed token")
		}
	}
}
