package config

import "testing"

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
