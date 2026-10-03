package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigEditorStoredPreferencePreviewAndOverride(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			binary := jiraBinary(t)
			command := shell.Quote(binary) + " --version 'literal ; $(never-run)'"
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_EDITOR=environment-editor", "JIRA_KEYRING=false"}, "config", "set", "editor", command, "--config", path, "--no-input", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("cannot store editor command: %d %q %q", exit, stdout, stderr)
			}
			if mode != "human" {
				out := decodeConfigSet(t, mode, stdout)
				if out.Value != command || !out.Created || !out.Changed {
					t.Fatalf("stored receipt lost command: %+v", out)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name, override string
				want           []string
			}{
				{"stored", "", []string{binary, "--version", "literal ; $(never-run)"}},
				{"override", shell.Quote(binary) + " --help", []string{binary, "--help"}},
			} {
				env := []string{"JIRA_EDITOR=" + tc.override, "VISUAL=unavailable-visual", "EDITOR=unavailable-editor", "JIRA_KEYRING=false"}
				stdout, stderr, exit = runBinaryEnv(t, env, "config", "edit", "--config", path, "--dry-run", "--output="+mode)
				if exit != 0 || stderr != "" {
					t.Fatalf("%s preview failed: %d %q %q", tc.name, exit, stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, path) || !strings.Contains(stdout, strings.Join(tc.want, " ")) {
						t.Fatalf("human preview lost command: %q", stdout)
					}
				} else {
					var out envelope.ConfigEditOutput
					if json.Unmarshal(adfData(t, stdout, mode, "config.edit", true), &out) != nil || !out.DryRun || out.Changed || out.Created || out.Path != path || !slices.Equal(out.Editor, tc.want) {
						t.Fatalf("preview lost selection: %+v", out)
					}
				}
			}
			stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_EDITOR=environment-editor", "JIRA_KEYRING=false"}, "config", "get", "editor", "--config", path, "--output=json")
			if exit != 0 || decodeConfigGet(t, "json", stdout).Value != command {
				t.Fatalf("stored editor was replaced by environment: %d %q %q", exit, stdout, stderr)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("preview or inspection changed configuration")
			}
		})
	}
}

func TestConfigEditorInvalidCommandRefusesInsteadOfFallingThrough(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"'unclosed", "   ", "unavailable-configured-editor --wait"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "set", "editor", value, "--config", path, "--no-input", "--output=json")
		if exit != 0 {
			t.Fatalf("command must be stored without requiring a local editor: %d %q %q", exit, stdout, stderr)
		}
		stdout, _, exit = runBinaryEnv(t, []string{"JIRA_EDITOR=", "VISUAL=" + shell.Quote(jiraBinary(t)), "JIRA_KEYRING=false"}, "config", "edit", "--config", path, "--dry-run", "--output=json")
		var frame apiFrame
		if exit != 3 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "editor_missing" || !strings.Contains(stdout, `"field":"editor"`) {
			t.Fatalf("invalid stored editor was silently replaced: %d %q", exit, stdout)
		}
	}
}

func TestConfigEditorExplicitOverrideCanPreviewInvalidTOML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const original = "not valid toml ["
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, override := range []string{"", shell.Quote(jiraBinary(t))} {
		stdout, _, exit := runBinaryEnv(t, []string{"JIRA_EDITOR=" + override, "VISUAL=" + shell.Quote(jiraBinary(t)), "JIRA_KEYRING=false"}, "config", "edit", "--config", path, "--dry-run", "--output=json")
		var frame apiFrame
		if json.Unmarshal([]byte(stdout), &frame) != nil {
			t.Fatal(stdout)
		}
		if override == "" {
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "config_invalid" || !strings.Contains(stdout, "JIRA_EDITOR") {
				t.Fatalf("invalid file lacks repair advice: %d %q", exit, stdout)
			}
		} else if exit != 0 || !frame.OK {
			t.Fatalf("explicit override cannot repair invalid file: %d %q", exit, stdout)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != original {
		t.Fatal("preview modified invalid file")
	}
}

func TestConfigEditorInspectionProvenanceAndEmptyFallback(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{"", "editor=''\n"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_EDITOR=environment-editor --wait", "JIRA_KEYRING=false"}, "config", "list", "--config", path, "--output=json")
		var out envelope.ConfigListOutput
		if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "config.list", true), &out) != nil {
			t.Fatalf("effective inspection failed: %d %q %q", exit, stdout, stderr)
		}
		index := slices.IndexFunc(out.Settings, func(setting envelope.ConfigSetting) bool { return setting.Key == "editor" })
		if index < 0 || out.Settings[index].Value != "environment-editor --wait" || out.Settings[index].Source != "env" || out.Settings[index].Secret {
			t.Fatalf("inspection lost editor provenance: %+v", out)
		}
		stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_EDITOR=environment-editor --wait", "JIRA_KEYRING=false"}, "config", "get", "editor", "--config", path, "--output=json")
		stored := decodeConfigGet(t, "json", stdout)
		if exit != 0 || stored.Value != "" || stored.Present != (declaration != "") {
			t.Fatalf("stored editor collapsed absence: %d %q %q", exit, stdout, stderr)
		}
		binary := jiraBinary(t)
		stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_EDITOR=", "VISUAL=" + shell.Quote(binary), "EDITOR=unavailable-editor", "JIRA_KEYRING=false"}, "config", "edit", "--config", path, "--dry-run", "--output=json")
		var preview envelope.ConfigEditOutput
		if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "config.edit", true), &preview) != nil || !slices.Equal(preview.Editor, []string{binary}) {
			t.Fatalf("empty preference prevented environment fallback: %d %q %q", exit, stdout, stderr)
		}
	}
}
