//go:build unix

package config

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A blocked open cannot be canceled through context. Run each native FIFO case
// in a bounded child that the parent kills and joins if the guard regresses.
func TestConfigFileInputRefusesFIFO(t *testing.T) {
	if dir := os.Getenv("JIRA_TEST_CONFIG_INPUT_DIRECTORY"); dir != "" {
		path := filepath.Join(dir, "settings.toml")
		var err error
		switch os.Getenv("JIRA_TEST_CONFIG_INPUT_OPERATION") {
		case "load":
			_, err = Load(Options{Path: path, Environ: func() []string { return nil }})
		case "open":
			_, err = OpenFile(t.Context(), path)
		case "save":
			file, openErr := OpenFile(t.Context(), path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if err := os.Remove(file.Path()); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(file.Path(), 0o600); err != nil {
				t.Fatal(err)
			}
			var changed bool
			changed, err = file.Save(t.Context(), []byte("read_only=true\n"))
			if changed {
				t.Fatal("save replaced a substituted FIFO")
			}
		default:
			t.Fatal("unknown owned input operation")
		}
		if !errors.Is(err, ErrFileInvalid) {
			t.Fatalf("FIFO must be refused as invalid configuration input: %v", err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("refusal changed the FIFO: info=%v err=%v", info, statErr)
		}
		return
	}
	for _, operation := range []string{"load", "open", "save"} {
		for _, linked := range []bool{false, true} {
			name := operation + "/direct"
			if linked {
				name = operation + "/symlink"
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
				if operation == "save" {
					if err := os.WriteFile(target, []byte("read_only=false\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, exe, "-test.run=^TestConfigFileInputRefusesFIFO$", "-test.count=1")
				cmd.Env = []string{"JIRA_TEST_CONFIG_INPUT_DIRECTORY=" + dir, "JIRA_TEST_CONFIG_INPUT_OPERATION=" + operation}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("configuration input blocked or failed: %v; context=%v\n%s", err, ctx.Err(), out)
				}
			})
		}
	}
}

func TestConfigLayeredSelectionCleansConfiguredPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	chosen := filepath.Join(dir, "chosen")
	other := filepath.Join(dir, "other")
	for _, path := range []string{chosen, filepath.Join(other, "nested")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(chosen, "settings.toml"), []byte("read_only=false\n[aliases]\n'my.inbox'='version'\n"), 0o600); err != nil {
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
			resolved, err := Load(Options{Path: path, Environ: func() []string { return nil }})
			if err != nil {
				t.Fatalf("historically cleaned path refused: %v", err)
			}
			if resolved.Config.ReadOnly || resolved.Path != path || !resolved.FileExists || resolved.Source("read_only") != LayerFile || resolved.Config.Aliases["my.inbox"] != "version" {
				t.Fatalf("layered selection changed file, authored path, aliases or provenance: %+v", resolved)
			}
			file, err := ReadFile(t.Context(), path)
			if err != nil || file.Path() != filepath.Join(chosen, "settings.toml") {
				t.Fatalf("snapshot selection disagrees with layered selection: file=%v err=%v", file, err)
			}
		})
	}
}
