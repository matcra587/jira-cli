package config

import (
	"strings"
	"testing"
)

func TestWorkspaceRoundTripIsGlobalAtomicAndTyped(t *testing.T) {
	const initial = "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://one.invalid'\n[profiles.other]\nbase_url='https://two.invalid'\n"
	path := aliasFixture(t, initial)
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	const value = `{"sections":[{"id":"triage","title":"Triage","jql":"project = DEMO ORDER BY updated DESC"}],"lenses":[{"id":"mine","title":"Assigned to me","jql":"assignee = currentUser()"}],"tabs":["triage","search"],"landing_view":"triage","landing_lens":"mine"}`
	update, err := file.PrepareSetting("tui_workspace", value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || loaded.Config.TUIWorkspace.LandingView != "triage" || loaded.Config.TUIWorkspace.Sections[0].Title != "Triage" {
		t.Fatal("workspace did not round trip globally", err)
	}
	loaded, err = Load(Options{Path: path, Environ: func() []string { return []string{`JIRA_TUI_WORKSPACE={"tabs":["search"]}`} }})
	if err != nil || len(loaded.Config.TUIWorkspace.TabIDs()) != 1 || loaded.Config.TUIWorkspace.TabIDs()[0] != "search" || loaded.Config.TUIWorkspace.LandingView != "" || len(loaded.Config.TUIWorkspace.Sections) != 0 {
		t.Fatal("workspace environment did not replace the complete object", err)
	}
}

func TestWorkspaceRejectsInvalidReferencesAndShapes(t *testing.T) {
	for _, value := range []string{
		`[]`, `null`, `{} {}`, `{"unknown":1}`, `{"tabs":[]}`, `{"tabs":["missing"]}`, `{"tabs":["search","search"]}`, `{"landing_view":"missing"}`, `{"landing_lens":"missing"}`,
		`{"sections":[{"id":"search","title":"Bad","jql":"project = DEMO"}]}`,
		`{"sections":[{"id":"one","title":"Same","jql":"project = DEMO"},{"id":"two","title":"Same","jql":"project = DEMO"}]}`,
		`{"lenses":[{"id":"mine","title":"Bad order","jql":"status = Open ORDER BY key"}]}`,
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseWorkspace(value); err == nil {
				t.Fatal("invalid workspace admitted")
			}
		})
	}
	for _, declaration := range []string{"tui_workspace='{}'", "tui_workspace=true", "[tui_workspace]\ntabs='search'", "[tui_workspace]\nunknown=1", "[[tui_workspace.sections]]\nid='a'\ntitle='A'\njql=''"} {
		t.Run(declaration, func(t *testing.T) {
			path := aliasFixture(t, declaration+"\n")
			_, err := Load(Options{Path: path, Environ: func() []string { return []string{`JIRA_TUI_WORKSPACE={}`} }})
			if err == nil {
				t.Fatal("override concealed malformed workspace")
			}
		})
	}
}

func TestWorkspaceCloneDoesNotAliasDraftCollections(t *testing.T) {
	w, err := ParseWorkspace(`{"sections":[{"id":"one","title":"One","jql":"project = DEMO"}],"tabs":["one"]}`)
	if err != nil {
		t.Fatal(err)
	}
	clone := w.Clone()
	clone.Sections[0].Title = "Changed"
	clone.Tabs[0] = "search"
	if strings.Contains(w.JSON(), "Changed") || w.Tabs[0] != "one" {
		t.Fatal("draft mutated source workspace")
	}
}
