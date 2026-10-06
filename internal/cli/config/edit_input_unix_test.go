//go:build unix

package config

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
)

// A scratch FIFO left by an editor must not strand the command, even after
// cancellation. A bounded child lets the parent kill and join a blocked read.
func TestEditRefusesNonregularScratchWithoutLosingCauses(t *testing.T) {
	if dir := os.Getenv("JIRA_TEST_EDITOR_INPUT_DIRECTORY"); dir != "" {
		path := filepath.Join(dir, "selected.toml")
		const original = "read_only=false\n"
		const earlier = "# earlier rejected edit\nread_only=true\n"
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		editorErr := cli.NewInputError(cli.CodeEditorFailed, "editor left unreadable input")
		outcome := os.Getenv("JIRA_TEST_EDITOR_INPUT_OUTCOME")
		kind := os.Getenv("JIRA_TEST_EDITOR_INPUT_KIND")
		linked := os.Getenv("JIRA_TEST_EDITOR_INPUT_LINKED") == "true"
		var scratchDir string
		out, err := editFile(ctx, path, func(scratch string) error {
			scratchDir = filepath.Dir(scratch)
			if err := os.Remove(scratch); err != nil {
				t.Fatal(err)
			}
			target := scratch
			if linked {
				target = filepath.Join(scratchDir, "target")
				if err := os.Symlink(target, scratch); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "fifo" {
				if err := unix.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if outcome == "canceled_success" || outcome == "canceled_failure" {
				cancel()
			}
			if outcome == "failure" || outcome == "canceled_failure" {
				return editorErr
			}
			return nil
		})
		wantCode := cli.CodeConfigInvalid
		if outcome == "failure" {
			wantCode = cli.CodeEditorFailed
		}
		if outcome == "canceled_success" || outcome == "canceled_failure" {
			wantCode = cli.CodeCanceled
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("scratch refusal lost cancellation: %v", err)
			}
		}
		if outcome == "failure" || outcome == "canceled_failure" {
			if !errors.Is(err, editorErr) {
				t.Fatalf("scratch refusal lost editor failure: %v", err)
			}
		}
		if !errors.Is(err, cfg.ErrFileInvalid) || cli.MapError(err).Code != wantCode || out.Changed || out.Created {
			t.Fatalf("scratch refusal lost its causes or fabricated publication: out=%+v err=%v", out, err)
		}
		for _, tc := range []struct{ path, want string }{{path: path, want: original}, {path: path + ".rejected", want: earlier}} {
			got, err := os.ReadFile(tc.path)
			if err != nil || string(got) != tc.want {
				t.Fatalf("scratch refusal changed stored work: %q, %v", got, err)
			}
		}
		if _, err := os.Stat(scratchDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("scratch directory was not cleaned up: %v", err)
		}
		return
	}
	for _, kind := range []string{"fifo", "directory"} {
		for _, outcome := range []string{"success", "failure", "canceled_success", "canceled_failure"} {
			for _, linked := range []bool{false, true} {
				route := "direct"
				if linked {
					route = "symlink"
				}
				t.Run(kind+"/"+outcome+"/"+route, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					path := filepath.Join(dir, "selected.toml")
					for _, tc := range []struct{ path, body string }{{path: path, body: "read_only=false\n"}, {path: path + ".rejected", body: "# earlier rejected edit\nread_only=true\n"}} {
						if err := os.WriteFile(tc.path, []byte(tc.body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					exe, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
					defer cancel()
					process := exec.CommandContext(ctx, exe, "-test.run=^TestEditRefusesNonregularScratchWithoutLosingCauses$", "-test.count=1")
					process.Env = []string{"TMPDIR=" + dir, "JIRA_TEST_EDITOR_INPUT_DIRECTORY=" + dir, "JIRA_TEST_EDITOR_INPUT_OUTCOME=" + outcome, "JIRA_TEST_EDITOR_INPUT_KIND=" + kind, "JIRA_TEST_EDITOR_INPUT_LINKED=" + strconv.FormatBool(linked)}
					output, err := process.CombinedOutput()
					if err != nil {
						t.Fatalf("scratch read blocked or failed: %v; context=%v\n%s", err, ctx.Err(), output)
					}
				})
			}
		}
	}
}

func TestEditAcceptsRegularScratchSymlinkAndKeepsAuthoredBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const edited = "# saved through a scratch symlink\nread_only=true\n"
	out, err := editFile(t.Context(), path, func(scratch string) error {
		target := filepath.Join(filepath.Dir(scratch), "regular.toml")
		if err := os.WriteFile(target, []byte(edited), 0o600); err != nil {
			return err
		}
		if err := os.Remove(scratch); err != nil {
			return err
		}
		return os.Symlink(target, scratch)
	})
	if err != nil || !out.Created || !out.Changed {
		t.Fatalf("regular scratch symlink refused: out=%+v err=%v", out, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != edited {
		t.Fatalf("scratch symlink lost authored bytes: %q, %v", got, err)
	}
}
