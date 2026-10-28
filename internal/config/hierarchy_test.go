package config

import "testing"

func TestHierarchyQueryGlobalRoundTripAndEnvironment(t *testing.T) {
	const authored = "format_version=1\ndefault_profile='work'\n" +
		"[profiles.work]\nbase_url='https://example.invalid'\n" +
		"[profiles.other]\nbase_url='https://other.invalid'\n"
	path := aliasFixture(t, authored)
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || string(loaded.Config.TUIHierarchyQuery) != DefaultTUIHierarchyQuery || loaded.Source("tui_hierarchy_query") != LayerDefault {
		t.Fatal("default hierarchy roots missing", err)
	}
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	const query = `issuetype = "300" ORDER BY updated DESC`
	update, err := file.PrepareSetting("tui_hierarchy_query", query)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatal("roots did not publish", err)
	}
	other, err := Load(Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || string(other.Config.TUIHierarchyQuery) != query || other.Source("tui_hierarchy_query") != LayerFile {
		t.Fatal("hierarchy query became profile-local", err)
	}
	overridden, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_HIERARCHY_QUERY=project = DEMO"} }})
	if err != nil || string(overridden.Config.TUIHierarchyQuery) != "project = DEMO" || overridden.Source("tui_hierarchy_query") != LayerEnv {
		t.Fatal("roots environment precedence lost", err)
	}
}

func TestHierarchyQueryRejectsInvalidAuthoredShapeBeforeOverride(t *testing.T) {
	for _, declaration := range []string{"tui_hierarchy_query=''", "tui_hierarchy_query=42", "tui_hierarchy_query=true", "tui_hierarchy_query='   '", `tui_hierarchy_query="line\nnext"`} {
		t.Run(declaration, func(t *testing.T) {
			path := aliasFixture(t, declaration+"\n")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{"JIRA_TUI_HIERARCHY_QUERY=project = DEMO"} }})
			if err == nil {
				t.Fatal("environment hid invalid authored roots")
			}
		})
	}
	for _, value := range []string{"", " ", "one\nnext", "nul\x00"} {
		if ValidateSettingValue("tui_hierarchy_query", value) == nil {
			t.Fatalf("invalid root shape accepted: %q", value)
		}
	}
}
