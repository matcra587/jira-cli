package config

import (
	"fmt"
	"testing"
)

func TestRefreshPreferencesAreGlobalTypedAndEnvironmentAware(t *testing.T) {
	const authored = "format_version=1\ndefault_profile='work'\n" +
		"[profiles.work]\nbase_url='https://example.invalid'\n" +
		"[profiles.other]\nbase_url='https://other.invalid'\n"
	path := aliasFixture(t, authored)
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.TUIRefreshInterval != 60 || !loaded.Config.TUIRefreshOnFocus || !loaded.Config.TUIChangeMarkers {
		t.Fatal("refresh defaults were not resolved", err)
	}
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSettings(map[string]string{"tui_refresh_interval": "0", "tui_refresh_on_focus": "false", "tui_change_markers": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, err := Load(Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || other.Config.TUIRefreshInterval != 0 || other.Config.TUIRefreshOnFocus || other.Config.TUIChangeMarkers || other.Source("tui_refresh_interval") != LayerFile {
		t.Fatal("refresh settings lost native types or were scoped to one profile", err)
	}
	overridden, err := Load(Options{Path: path, Environ: func() []string {
		return []string{"JIRA_TUI_REFRESH_INTERVAL=86400", "JIRA_TUI_REFRESH_ON_FOCUS=true", "JIRA_TUI_CHANGE_MARKERS=true"}
	}})
	if err != nil || overridden.Config.TUIRefreshInterval != 86400 || !overridden.Config.TUIRefreshOnFocus || !overridden.Config.TUIChangeMarkers || overridden.Source("tui_refresh_interval") != LayerEnv {
		t.Fatal("refresh environment did not override the file", err)
	}
}

func TestRefreshIntervalRejectsInvalidNativeFilesBeforeOverrides(t *testing.T) {
	for _, value := range []string{"-1", "86401", "45.5", "'45'", "true"} {
		t.Run(value, func(t *testing.T) {
			path := aliasFixture(t, "tui_refresh_interval="+value+"\n")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_REFRESH_INTERVAL=60"} }})
			if err == nil {
				t.Fatal("environment concealed an invalid authored interval")
			}
		})
	}
	for _, value := range []string{"-1", "86401", "1.5", "true", "9223372036854775808"} {
		t.Run(fmt.Sprintf("environment/%s", value), func(t *testing.T) {
			path := aliasFixture(t, "")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_REFRESH_INTERVAL=" + value} }})
			if err == nil || ValidateSettingValue("tui_refresh_interval", value) == nil {
				t.Fatal("invalid interval admitted through environment or scalar setting")
			}
		})
	}
}

func TestAutomaticPagingPreferenceIsGlobalAndHonorsEnvironment(t *testing.T) {
	const authored = "format_version=1\ndefault_profile='work'\n" +
		"[profiles.work]\nbase_url='https://example.invalid'\n" +
		"[profiles.other]\nbase_url='https://other.invalid'\n"
	path := aliasFixture(t, authored)
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || !loaded.Config.TUIAutoPage || loaded.Source("tui_auto_page") != LayerDefault {
		t.Fatal("default automatic paging not resolved", err)
	}
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSetting("tui_auto_page", "false")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, err := Load(Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || other.Config.TUIAutoPage || other.Source("tui_auto_page") != LayerFile ||
		other.Config.BaseURL != "https://other.invalid" {
		t.Fatal("automatic paging was scoped to one profile", err)
	}
	overridden, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_AUTO_PAGE=true"} }})
	if err != nil || !overridden.Config.TUIAutoPage || overridden.Source("tui_auto_page") != LayerEnv {
		t.Fatal("environment override was not retained", err)
	}
	if ValidateSettingValue("tui_auto_page", "invalid") == nil {
		t.Fatal("invalid paging preference accepted")
	}
}

func TestPreviewPreferencesRoundTripGlobalAndEnvironment(t *testing.T) {
	const authored = "format_version=1\ndefault_profile='work'\n" +
		"[profiles.work]\nbase_url='https://example.invalid'\n" +
		"[profiles.other]\nbase_url='https://other.invalid'\n"
	path := aliasFixture(t, authored)
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSettings(map[string]string{"tui_preview_dock": "bottom", "tui_preview_size": "55"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, err := Load(Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || other.Config.TUIPreviewDock != PreviewDockBottom || other.Config.TUIPreviewSize != 55 || other.Source("tui_preview_size") != LayerFile {
		t.Fatal("layout was not global or lost its native type", err)
	}
	overridden, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_PREVIEW_DOCK=RIGHT", "JIRA_TUI_PREVIEW_SIZE=75"} }})
	if err != nil || overridden.Config.TUIPreviewDock != PreviewDockRight || overridden.Config.TUIPreviewSize != 75 || overridden.Source("tui_preview_dock") != LayerEnv {
		t.Fatal("layout environment did not override the file", err)
	}
}

func TestPreviewPreferenceAdmissionRejectsInvalidFileBeforeEnvironment(t *testing.T) {
	for _, declaration := range []string{
		"tui_preview_size=24", "tui_preview_size=76", "tui_preview_size=40.5", "tui_preview_size='40'", "tui_preview_size=true",
		"tui_preview_dock='left'", "tui_preview_dock=''", "tui_preview_dock=1", "tui_preview_dock=true",
	} {
		t.Run(declaration, func(t *testing.T) {
			path := aliasFixture(t, declaration+"\n")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_PREVIEW_DOCK=auto", "JIRA_TUI_PREVIEW_SIZE=40"} }})
			if err == nil {
				t.Fatal("environment hid an invalid authored layout")
			}
		})
	}
	for _, value := range []string{"0", "76", "40.5", "true", "9223372036854775808"} {
		t.Run("env/"+value, func(t *testing.T) {
			path := aliasFixture(t, "")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_PREVIEW_SIZE=" + value} }})
			if err == nil {
				t.Fatal("invalid percentage environment accepted")
			}
		})
	}
}
