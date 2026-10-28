package dashboard

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/jira"
)

func selectAuditSetting(t *testing.T, m *Model, name string) {
	t.Helper()
	m.settings.input.SetValue("^" + name + " ")
	m.rebuildSettings()
	if len(m.settings.visible) != 1 || m.settings.visible[0].key != name {
		t.Fatalf("audit did not select exactly %s", name)
	}
}

func TestEverySettingHonorsEnvironmentAndFlagOwnership(t *testing.T) {
	for _, source := range []string{"env", "flag"} {
		for _, setting := range allDashboardSettings() {
			t.Run(source+"/"+setting.key, func(t *testing.T) {
				m := model(t)
				m.options.SettingSources = map[string]string{setting.key: source}
				m.openSettings()
				before := maps.Clone(m.settings.values)
				selectAuditSetting(t, m, setting.key)
				for _, action := range []string{"enter", "space", "d"} {
					m.Update(key.Replay(action))
					if m.settings.editor != nil || m.dialogs.Top() != nil || m.settingsChanged() || !maps.Equal(before, m.settings.values) || !strings.Contains(m.settings.notice, "Controlled by") {
						t.Fatalf("%s bypassed %s ownership", action, source)
					}
				}
			})
		}
	}
}

func TestEveryDisplayPreferenceChangesItsRenderedFeatureAndCancelRestores(t *testing.T) {
	markers := map[string]string{
		"tui_status": "STATUS", "tui_assignee": "ASSIGNEE", "tui_updated": "UPDATED",
		"tui_labels": "audit-label", "tui_priority": "Audit priority", "tui_issue_type": "Audit type",
		"tui_preview": "Description", "tui_compact": "",
	}
	for setting, marker := range markers {
		t.Run(setting, func(t *testing.T) {
			var bean jira.IssueBean
			if err := json.Unmarshal([]byte(`{"key":"DEMO-1","fields":{"summary":"Audit issue","status":{"name":"In progress"},"assignee":{"displayName":"Audit person"},"updated":"2026-01-01","labels":["audit-label"],"priority":{"name":"Audit priority"},"issuetype":{"name":"Audit type"},"description":null}}`), &bean); err != nil {
				t.Fatal(err)
			}
			m := model(t, bean, issue("DEMO-2", "Second issue"))
			m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
			m.tabs.SetActive(1) // Search exposes the assignee column.
			m.detailIssue, m.detailKey, m.detailLoading = &bean, "DEMO-1", false
			m.resize()
			before := xansi.Strip(m.View().Content)
			if marker != "" && !strings.Contains(before, marker) {
				t.Fatalf("baseline did not display %s", marker)
			}
			m.openSettings()
			selectAuditSetting(t, m, setting)
			m.Update(key.Replay("enter"))
			m.Update(key.Replay("p"))
			after := xansi.Strip(m.View().Content)
			if marker != "" && strings.Contains(after, marker) {
				t.Fatalf("disabled %s still appears in the actual preview", setting)
			}
			if setting == "tui_compact" {
				row := func(view, needle string) int { return strings.Count(strings.SplitN(view, needle, 2)[0], "\n") }
				if row(after, "DEMO-2  Second issue")-row(after, "DEMO-1  Audit issue") != 2 || row(before, "DEMO-2  Second issue")-row(before, "DEMO-1  Audit issue") != 1 {
					t.Fatal("compact preference did not change rendered row spacing")
				}
			}
			m.Update(key.Replay("esc")) // Leave preview, preserving the draft.
			m.Update(key.Replay("esc")) // Discard the draft.
			if restored := xansi.Strip(m.View().Content); restored != before {
				t.Fatal("cancel did not restore the original rendered feature")
			}
		})
	}
}

func TestEveryScalarEnumChoiceAndInvalidValueUsesSharedAdmission(t *testing.T) {
	for _, name := range []string{"glyphs", "secret_backend"} {
		for _, value := range config.SettingValueChoices(name) {
			t.Run(name+"/"+value, func(t *testing.T) {
				m := model(t)
				m.openSettings()
				selectAuditSetting(t, m, name)
				m.Update(key.Replay("enter"))
				menu := m.dialogs.Top().(*settingMenu)
				for range len(config.SettingValueChoices(name)) {
					if menu.value() == value {
						break
					}
					m.Update(key.Replay("down"))
				}
				m.Update(key.Replay("enter"))
				if m.settings.editor != nil || m.settings.values[name] != value {
					t.Fatal("advertised enum choice was not admitted")
				}
			})
		}
	}
	invalid := map[string]string{
		"base_url": "file:///bad", "cloud_id": "bad/path", "credential_id": "bad-slot",
		"account_id": " ", "account_id_site": "file:///bad", "account_id_email": " ",
		"account_id_cloud_id": "bad/path", "api_token_env": "INVALID_NAME",
		"refresh_interval": "-1", "team_account_ids": "not-json", "workday_seconds": "0",
		"timeout": "-1s",
	}
	for name, value := range invalid {
		t.Run(name+"/invalid", func(t *testing.T) {
			m := model(t)
			m.openSettings()
			selectAuditSetting(t, m, name)
			before := maps.Clone(m.settings.values)
			m.Update(key.Replay("enter"))
			m.settings.editor.line.SetValue(value)
			m.Update(key.Replay("enter"))
			if m.settings.editor == nil || m.settings.notice == "" || !maps.Equal(before, m.settings.values) || m.settingsChanged() {
				t.Fatal("invalid scalar was admitted or changed the draft")
			}
			m.Update(key.Replay("esc"))
			if m.settings.editor != nil || m.settingsChanged() {
				t.Fatal("canceling invalid input changed the draft")
			}
		})
	}
}
