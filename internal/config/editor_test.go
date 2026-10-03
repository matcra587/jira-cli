package config_test

import (
	"os"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestEditorPreferenceHasIndependentLayerProvenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, override, want string
		source                     config.Layer
	}{
		{"default", "", "", "", config.LayerDefault},
		{"file", "editor='saved-editor --wait'\n", "", "saved-editor --wait", config.LayerFile},
		{"environment", "editor='saved-editor --wait'\n", "environment-editor", "environment-editor", config.LayerEnv},
		{"empty declaration", "editor=''\n", "", "", config.LayerFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, tc.file)
			resolved, err := config.Load(config.Options{Path: path, Environ: env("JIRA_EDITOR=" + tc.override)})
			if err != nil || resolved.Config.Editor != tc.want || resolved.Config.Values()["editor"] != tc.want || resolved.Source("editor") != tc.source {
				t.Fatalf("editor value or provenance lost: %+v %v", resolved, err)
			}
		})
	}
}

func TestEditorPreferenceRejectsNonStringFileBeforeEnvironmentOverlay(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"true", "12", "[]", "{}"} {
		path := writeConfig(t, "editor="+value+"\n")
		if resolved, err := config.Load(config.Options{Path: path, Environ: env("JIRA_EDITOR=valid-editor")}); err == nil || resolved != nil {
			t.Fatalf("environment masked invalid editor type %s", value)
		}
	}
}

func TestEditorSettingSaveNeverPersistsEnvironment(t *testing.T) {
	t.Setenv("JIRA_EDITOR", "environment-editor --wait")
	path := writeConfig(t, "# authored\neditor='saved-editor --wait'\n")
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSetting("read_only", "true")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	setting, err := stored.Setting("editor")
	if err != nil || !setting.Present || setting.Value != "saved-editor --wait" {
		t.Fatalf("transient editor persisted: %+v %v", setting, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Load(config.Options{Path: path})
	if err != nil || resolved.Config.Editor != "environment-editor --wait" || resolved.Source("editor") != config.LayerEnv {
		t.Fatalf("effective editor did not retain override: %+v %v", resolved, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("effective inspection rewrote the file")
	}
}
