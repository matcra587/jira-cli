//go:build unix

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigCommandsRefuseFIFOWithStructuredErrors(t *testing.T) {
	t.Parallel()
	for _, linked := range []bool{false, true} {
		name := "direct"
		if linked {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			target := path
			if linked {
				target = filepath.Join(dir, "target.toml")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := unix.Mkfifo(target, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name string
				args []string
			}{
				{name: "get", args: []string{"config", "get", "read_only"}},
				{name: "list", args: []string{"config", "list"}},
				{name: "set_preview", args: []string{"config", "set", "read_only", "true", "--dry-run"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := append(tc.args, "--config", path, "--output=json", "--timeout=100ms")
					stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
					var frame apiFrame
					if exit != 3 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "config_invalid" {
						t.Fatalf("FIFO refusal lost its configuration error: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
					}
					info, err := os.Stat(target)
					if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
						t.Fatalf("refusal changed the selected FIFO: %v", err)
					}
				})
			}
		})
	}
}

func TestConfigCommandsAgreeOnCleanedFileSelection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	chosen := filepath.Join(dir, "chosen")
	other := filepath.Join(dir, "other")
	for _, path := range []string{chosen, filepath.Join(other, "nested")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(chosen, "settings.toml"), []byte("read_only=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "settings.toml"), []byte("read_only=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "nested"), filepath.Join(chosen, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, middle string }{
		{name: "symlink_parent", middle: "link"},
		{name: "redundant_missing_parent", middle: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := chosen + "/" + tc.middle + "/../settings.toml"
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "list", "--config", path, "--output=json")
			var frame apiFrame
			var out envelope.ConfigListOutput
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || json.Unmarshal(frame.Data, &out) != nil || out.Path != path || !out.FileExists {
				t.Fatalf("effective read changed selected-path semantics: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			found := false
			for _, setting := range out.Settings {
				if setting.Key == "read_only" {
					found = true
					if setting.Value != "false" || setting.Source != "file" {
						t.Fatalf("effective read selected the symlink traversal target: %+v", setting)
					}
				}
			}
			if !found {
				t.Fatal("effective read omitted read_only")
			}
			stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "get", "read_only", "--config", path, "--output=json")
			stored := decodeConfigGet(t, "json", stdout)
			if exit != 0 || !stored.Present || stored.Value != "false" || stored.Path != filepath.Join(chosen, "settings.toml") {
				t.Fatalf("stored read disagrees with effective selection: exit=%d stored=%+v stderr=%q", exit, stored, stderr)
			}
		})
	}
}
