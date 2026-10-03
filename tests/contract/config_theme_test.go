package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func decodeConfigTheme(t *testing.T, stdout, mode string) envelope.ConfigThemeOutput {
	t.Helper()
	data := []byte(stdout)
	if mode != "compact" {
		var frame struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &frame); err != nil || !frame.OK {
			t.Fatalf("invalid theme success envelope: %q %v", stdout, err)
		}
		data = frame.Data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"dry_run", "path", "file_exists", "name", "name_present", "theme_path", "path_present", "would_change", "changed", "created"} {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			t.Fatalf("theme receipt lost required %s: %q", key, stdout)
		}
	}
	var result envelope.ConfigThemeOutput
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConfigThemeActualPreviewPublicationInspectionAndBuiltinSwitch(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			settings := filepath.Join(dir, "missing", "settings.toml")
			palette := filepath.Join(dir, "palette.toml")
			if err := os.WriteFile(palette, []byte("base='light'\n[colors]\nhelp_command='#123456'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_KEYRING=false", "JIRA_READ_ONLY=true", "JIRA_THEME=monokai", "JIRA_EMAIL=env@example.invalid"}
			args := []string{"config", "theme", "--name=dracula", "--path", palette, "--config", settings, "--no-input"}
			wireMode := mode
			if mode == "agent" {
				env = append(env, "AGENT=1")
				wireMode = "compact"
			} else {
				args = append(args, "--output="+mode)
			}
			preview, stderr, exit := runBinaryEnv(t, env, append(args, "--dry-run")...)
			if exit != 0 || stderr != "" {
				t.Fatalf("paired theme preview failed: exit=%d stdout=%q stderr=%q", exit, preview, stderr)
			}
			if mode != "human" {
				out := decodeConfigTheme(t, preview, wireMode)
				if !out.DryRun || !out.WouldChange || out.Changed || out.Created || out.FileExists || !out.NamePresent || !out.PathPresent || out.Name != "dracula" || out.ThemePath != palette || out.Path != settings {
					t.Fatalf("theme preview confused candidate and publication: %+v", out)
				}
			} else if !strings.Contains(preview, "would select theme") {
				t.Fatal("human preview fabricated a stored selection")
			}
			if _, err := os.Stat(filepath.Dir(settings)); !os.IsNotExist(err) {
				t.Fatal("theme preview created settings or directories")
			}
			stored, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("theme pair publication failed: exit=%d stdout=%q stderr=%q", exit, stored, stderr)
			}
			if mode != "human" {
				out := decodeConfigTheme(t, stored, wireMode)
				if out.DryRun || !out.Changed || !out.Created || !out.FileExists || out.Name != "dracula" || out.ThemePath != palette {
					t.Fatalf("paired publication lost its facts: %+v", out)
				}
			}
			contents, err := os.ReadFile(settings)
			if err != nil || !strings.Contains(string(contents), "dracula") || !strings.Contains(string(contents), "palette.toml") || strings.Contains(string(contents), "env@example.invalid") || strings.Contains(string(contents), "read_only") {
				t.Fatal("theme publication lost authored values or persisted effective layers")
			}
			info, err := os.Stat(settings)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("theme settings were not published privately")
			}
			inspected, stderr, exit := runBinaryEnv(t, env, "config", "theme", "--config", settings, "--output=json", "--no-input")
			out := decodeConfigTheme(t, inspected, "json")
			if exit != 0 || stderr != "" || out.Name != "dracula" || out.ThemePath != palette || !out.NamePresent || !out.PathPresent || out.WouldChange || out.Changed || out.Created {
				t.Fatal("theme inspection used environment values or claimed publication")
			}
			inspected, stderr, exit = runBinaryEnv(t, env, "config", "theme", "--name=nord", "--config", settings, "--output=json", "--no-input")
			out = decodeConfigTheme(t, inspected, "json")
			if exit != 0 || stderr != "" || !out.Changed || out.Name != "nord" || out.ThemePath != "" || !out.PathPresent || out.Created {
				t.Fatal("preset selection retained a custom palette that would override it")
			}
		})
	}
}

func TestConfigThemeActualInvalidInputRefusesBothDeclarationsBeforePublication(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "bad.toml")
	const original = "# authored\ntheme='monokai'\ntheme_path=''\n"
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(palette, []byte("[colors]\nred='#broken'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		field   string
		message string
	}{
		{name: "bad name before bad path", args: []string{"--name=unknown-preset", "--path", palette + ".missing"}, field: "name", message: "unknown theme"},
		{name: "invalid custom file", args: []string{"--name=light", "--path", palette}, field: "path", message: "invalid custom theme file"},
	} {
		for _, preview := range []bool{true, false} {
			args := append([]string{"config", "theme", "--config", settings, "--output=json", "--no-input"}, tc.args...)
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var result apiFrame
			if exit != 3 || !strings.Contains(stderr, tc.message) || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "flag_value_invalid" || result.Errors[0].Field != tc.field {
				t.Fatalf("invalid theme input lost refusal: %s exit=%d stdout=%q stderr=%q", tc.name, exit, stdout, stderr)
			}
		}
	}
	contents, err := os.ReadFile(settings)
	if err != nil || string(contents) != original {
		t.Fatal("refused selection published part of its pair")
	}
}

func TestConfigThemeActualNoopEmptyClearAndRelativePaletteSelection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "palette.toml")
	const original = "# keep comments\ntheme='dark'\ntheme_path=''\n"
	if err := os.WriteFile(settings, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "theme", "--name=dark", "--config", settings, "--output=json", "--no-input")
	out := decodeConfigTheme(t, stdout, "json")
	if exit != 0 || stderr != "" || out.WouldChange || out.Changed || out.Created {
		t.Fatal("equal theme selection fabricated publication")
	}
	after, err := os.Stat(settings)
	contents, readErr := os.ReadFile(settings)
	if err != nil || readErr != nil || string(contents) != original || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Fatal("equal theme selection rewrote bytes or metadata")
	}
	if err := os.WriteFile(palette, []byte("base='light'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit = runBinaryIOInDirStarted(t, dir, []string{"JIRA_KEYRING=false"}, nil, nil, nil, "config", "theme", "--path=palette.toml", "--config", settings, "--output=json", "--no-input")
	out = decodeConfigTheme(t, stdout, "json")
	if exit != 0 || stderr != "" || out.Name != "dark" || out.ThemePath != palette || !out.Changed {
		t.Fatal("relative palette selection was not validated and stored absolute")
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "theme", "--name=", "--config", settings, "--output=json", "--no-input")
	out = decodeConfigTheme(t, stdout, "json")
	if exit != 0 || stderr != "" || out.Name != "" || out.ThemePath != "" || !out.NamePresent || !out.PathPresent || !out.Changed {
		t.Fatal("explicit empty theme reset collapsed stored empty declarations")
	}
}

func TestConfigThemeDiscoveryAndSuggestedPresetRecoveryExecute(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "schema", "--path=config theme", "--output=json")
	for _, required := range []string{`"config.theme"`, `"theme_path"`, `"name_present"`, `"path_present"`, `"would_change"`, `"dracula"`, `"path"`} {
		if exit != 0 || stderr != "" || !strings.Contains(stdout, required) {
			t.Fatalf("theme discovery omitted %s: exit=%d stdout=%q stderr=%q", required, exit, stdout, stderr)
		}
	}
	path := filepath.Join(t.TempDir(), "missing", "settings.toml")
	stdout, _, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "theme", "--name=monokaias", "--config", path, "--dry-run", "--output=json")
	var failure apiFrame
	if exit != 3 || json.Unmarshal([]byte(stdout), &failure) != nil || failure.OK || len(failure.Errors) != 1 || failure.Errors[0].Code != "flag_value_invalid" {
		t.Fatal("theme typo did not return typed recovery")
	}
	var recovery struct {
		Errors []struct {
			Suggestions []string `json:"suggestions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &recovery); err != nil || len(recovery.Errors) != 1 || len(recovery.Errors[0].Suggestions) == 0 {
		t.Fatal("near preset typo offered no correction")
	}
	for _, suggestion := range recovery.Errors[0].Suggestions {
		stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "theme", "--name="+suggestion, "--config", path, "--dry-run", "--output=json")
		out := decodeConfigTheme(t, stdout, "json")
		if exit != 0 || stderr != "" || out.Name != suggestion || !out.DryRun || !out.WouldChange || out.Changed || out.Created {
			t.Fatalf("suggested theme recovery did not execute: %s exit=%d stdout=%q stderr=%q", suggestion, exit, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("theme discovery, refusal or corrected preview created local state")
	}
}
