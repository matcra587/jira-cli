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
