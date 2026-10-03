//go:build linux

package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestEditorTerminalHelperProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_TERMINAL_EDITOR") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 3 {
		os.Exit(30)
	}
	mode, ready, release := os.Args[index+1], os.Args[index+2], os.Args[index+3]
	if mode == "marker" {
		if err := os.WriteFile(ready, []byte("launched"), 0o600); err != nil {
			os.Exit(35)
		}
		os.Exit(0)
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		os.Exit(31)
	}
	if _, err := term.MakeRaw(int(os.Stderr.Fd())); err != nil {
		os.Exit(32)
	}
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		os.Exit(33)
	}
	for {
		if _, err := os.Stat(release); err == nil {
			if mode == "failure" {
				os.Exit(34)
			}
			os.Exit(0)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEditorTerminalCaptureFailureNeverLaunches(t *testing.T) {
	t.Setenv("JIRA_TEST_TERMINAL_EDITOR", "1")
	_, input := editorTestTerminal(t)
	before, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipe.Close(); _ = writer.Close() })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostics := range []*os.File{nil, pipe} {
		marker := filepath.Join(t.TempDir(), "editor-launched")
		err := runEditor(context.Background(), []string{executable, "-test.run=^TestEditorTerminalHelperProcess$", "--", "marker", marker}, "unused", input, diagnostics)
		if classification := cli.MapError(err); classification.Code != cli.CodeTerminalRequired {
			t.Fatalf("invalid second terminal reached launch: %v", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("editor launched after terminal capture failure: %v", err)
		}
		after, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
		if err != nil || *before != *after {
			t.Fatalf("partial terminal capture changed borrowed input: %v", err)
		}
	}
}

func TestEditorLaunchFailureRestoresBorrowedTerminalsAndKeepsArguments(t *testing.T) {
	t.Parallel()
	_, input := editorTestTerminal(t)
	_, diagnostics := editorTestTerminal(t)
	beforeInput, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	beforeDiagnostics, err := unix.IoctlGetTermios(int(diagnostics.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	editor := []string{filepath.Join(t.TempDir(), "missing editor"), "argument sentinel"}
	err = runEditor(context.Background(), editor[:1], "scratch path with spaces", input, diagnostics)
	if classification := cli.MapError(err); classification.Code != cli.CodeEditorFailed {
		t.Fatalf("editor launch failure lacks classification: %v", err)
	}
	if editor[1] != "argument sentinel" {
		t.Fatalf("editor arguments changed through spare slice capacity: %q", editor)
	}
	afterInput, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
	if err != nil || *beforeInput != *afterInput {
		t.Fatalf("launch failure changed borrowed input: %v", err)
	}
	afterDiagnostics, err := unix.IoctlGetTermios(int(diagnostics.Fd()), unix.TCGETS)
	if err != nil || *beforeDiagnostics != *afterDiagnostics {
		t.Fatalf("launch failure changed borrowed diagnostics: %v", err)
	}
}

func TestEditorRestoreFailureRemainsActionableWithExitOrCancellation(t *testing.T) {
	// The subprocess inherits this test-only setting. Do not run alongside
	// tests that depend on the process environment being unchanged.
	t.Setenv("JIRA_TEST_TERMINAL_EDITOR", "1")
	for _, mode := range []string{"success", "failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			_, input := editorTestTerminal(t)
			diagnosticsMaster, diagnostics := editorTestTerminal(t)
			before, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				finished <- runEditor(ctx, []string{executable, "-test.run=^TestEditorTerminalHelperProcess$", "--", mode, ready}, release, input, diagnostics)
			}()
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				select {
				case err := <-finished:
					t.Fatalf("editor exited before readiness: %v", err)
				case <-ctx.Done():
					t.Fatal("editor did not reach readiness")
				case <-time.After(time.Millisecond):
				}
			}
			// Hang up one terminal after the child inherits it. Closing its
			// master avoids racing os.File.Close with the owner's borrowed file.
			// The input terminal must still be restored, and the restoration
			// failure must survive the subprocess outcome.
			if err := diagnosticsMaster.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				cancel()
			} else if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				classification := cli.MapError(err)
				wantCode := cli.CodeTerminalRestoreFailed
				if mode == "canceled" {
					wantCode = cli.CodeCanceled
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("restoration failure lost context cause: %v", err)
					}
				}
				if classification.Code != wantCode || !strings.Contains(err.Error(), "restore editor terminal on stderr; restore the terminal before continuing") {
					t.Fatalf("restoration failure lacks actionable classification: %+v", classification)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("editor did not finish after release or cancellation")
			}
			after, err := unix.IoctlGetTermios(int(input.Fd()), unix.TCGETS)
			if err != nil || *before != *after {
				t.Fatalf("one restoration failure prevented restoring the other terminal: %v", err)
			}
		})
	}
}

func editorTestTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(descriptor), "editor-test-terminal")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return master, terminal
}
