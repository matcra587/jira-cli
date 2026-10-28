package config

import "testing"

func TestWorkspaceBindingsValidateDefaultsConflictsAndAtomicSwaps(t *testing.T) {
	defaults := make(map[string][]string)
	for _, action := range WorkspaceActions() {
		if _, ok := defaults[action.ID]; ok {
			t.Fatal("duplicate action ID")
		}
		defaults[action.ID] = action.Keys
	}
	if err := ValidateWorkspaceKeys(defaults); err != nil {
		t.Fatal("default catalog is not valid", err)
	}
	for _, test := range []struct {
		name string
		keys map[string][]string
	}{
		{name: "unknown", keys: map[string][]string{"unknown": {"f6"}}},
		{name: "collision", keys: map[string][]string{"create": {"e"}}},
		{name: "cancel", keys: map[string][]string{"quit": {"esc"}}},
		{name: "exit", keys: map[string][]string{"quit": {"ctrl+c"}}},
		{name: "unsupported", keys: map[string][]string{"quit": {"not-a-key"}}},
		{name: "shifted letter", keys: map[string][]string{"quit": {"shift+a"}}},
		{name: "shifted punctuation", keys: map[string][]string{"quit": {"shift+/"}}},
		{name: "empty", keys: map[string][]string{"quit": {}}},
		{name: "duplicate alias", keys: map[string][]string{"quit": {"f6", "f6"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if ValidateWorkspaceKeys(test.keys) == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
	if err := ValidateWorkspaceKeys(map[string][]string{"create": {"e"}, "edit": {"n"}}); err != nil {
		t.Fatal("valid atomic key swap rejected", err)
	}
}

func TestWorkspaceBindingsRoundTripAndClone(t *testing.T) {
	path := aliasFixture(t, "")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSetting("tui_workspace", `{"keys":{"view-next":["f6"],"settings":["f7"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.TUIWorkspace.Keys["view-next"][0] != "f6" {
		t.Fatal("binding did not retain native string array")
	}
	clone := loaded.Config.TUIWorkspace.Clone()
	clone.Keys["view-next"][0] = "f8"
	if loaded.Config.TUIWorkspace.Keys["view-next"][0] != "f6" {
		t.Fatal("settings binding draft aliased active configuration")
	}
}
