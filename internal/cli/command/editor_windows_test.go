//go:build windows && nativeconsole

package command_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// Each case runs in a fresh hidden console, never the caller's terminal. The
// outer process retains redirected stdout so editor output cannot earn a pass
// by escaping the shared supervisor's diagnostics boundary.
func TestNativeWindowsEditorConsole(t *testing.T) {
	for _, mode := range []string{"success", "failure", "canceled", "launch_failure", "capture_failure"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestWindowsEditorConsoleProcess$")
			process.Env = append(os.Environ(), "JIRA_TEST_CONSOLE_CASE="+mode)
			process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			process.WaitDelay = 2 * time.Second
			output, err := process.CombinedOutput()
			if err != nil {
				t.Fatalf("native console case did not execute: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "console-owner-verified:"+mode) ||
				!strings.Contains(string(output), "--- PASS: TestWindowsEditorConsoleProcess") ||
				strings.Contains(string(output), "editor-screen-sentinel") {
				t.Fatalf("missing native acceptance or editor polluted stdout: %s", output)
			}
		})
	}
}

func TestWindowsEditorConsoleProcess(t *testing.T) {
	mode := os.Getenv("JIRA_TEST_CONSOLE_CASE")
	if mode == "" {
		return
	}
	input := windowsEditorTerminal(t, "CONIN$")
	diagnostics := windowsEditorTerminal(t, "CONOUT$")
	beforeInput, beforeOutput := windowsEditorMode(t, input), windowsEditorMode(t, diagnostics)
	// Only this isolated subprocess replaces its borrowed streams. Its stdout
	// remains the outer driver's pipe and is not supplied to the editor.
	oldInput, oldDiagnostics := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = input, diagnostics
	defer func() { os.Stdin, os.Stderr = oldInput, oldDiagnostics }()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready.json"), filepath.Join(dir, "release")
	scratch := filepath.Join(dir, "draft 古い $literal & value.txt")
	const literal = "literal $value & `quoted` ; no shell"
	editor := []string{executable, "-test.run=^TestWindowsEditorProcess$", "--", mode, ready, release, literal}
	configured := append(editor, "spare argv sentinel")
	editor = configured[:len(editor)]
	original := slices.Clone(editor)
	t.Setenv("JIRA_TEST_CONSOLE_EDITOR", "1")
	switch mode {
	case "launch_failure":
		editor = []string{filepath.Join(dir, "missing editor.exe")}
		err = command.RunEditor(t.Context(), editor, scratch)
		if cli.MapError(err).Code != cli.CodeEditorFailed {
			t.Fatalf("native launch failure lost classification: %v", err)
		}
	case "capture_failure":
		pipe, writer, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		defer func() { _ = pipe.Close(); _ = writer.Close() }()
		os.Stderr = pipe
		err = command.RunEditor(t.Context(), editor, scratch)
		os.Stderr = diagnostics
		if cli.MapError(err).Code != cli.CodeTerminalRequired {
			t.Fatalf("native capture failure reached launch: %v", err)
		}
		if _, err := os.Stat(ready); !os.IsNotExist(err) {
			t.Fatalf("editor started after capture refusal: %v", err)
		}
	default:
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		done := make(chan struct{})
		var runErr error
		go func() {
			runErr = command.RunEditor(ctx, editor, scratch)
			close(done)
		}()
		defer func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("native editor supervisor did not join after cancellation")
			}
		}()
		var observation struct {
			PID  uint32
			Args []string
		}
		for {
			if raw, err := os.ReadFile(ready); err == nil && json.Unmarshal(raw, &observation) == nil {
				break
			}
			select {
			case <-done:
				t.Fatalf("native editor exited before readiness: %v", runErr)
			case <-ctx.Done():
				t.Fatal("native editor did not reach readiness")
			case <-time.After(5 * time.Millisecond):
			}
		}
		if !slices.Equal(observation.Args, []string{mode, ready, release, literal, scratch}) {
			t.Fatalf("native editor changed literal argv: %q", observation.Args)
		}
		if windowsEditorMode(t, input) == beforeInput || windowsEditorMode(t, diagnostics) == beforeOutput {
			t.Fatal("editor did not change both actual native console modes")
		}
		child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, observation.PID)
		if err != nil {
			t.Fatalf("could not independently observe live editor process: %v", err)
		}
		defer func() { _ = windows.CloseHandle(child) }()
		if mode == "canceled" {
			cancel()
		} else if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("native editor did not finish after release or cancellation")
		}
		state, err := windows.WaitForSingleObject(child, 0)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("supervisor returned with editor still alive: %v, state %d", err, state)
		}
		switch mode {
		case "success":
			if runErr != nil {
				t.Fatalf("native editor failed: %v", runErr)
			}
		case "failure":
			if cli.MapError(runErr).Code != cli.CodeEditorFailed {
				t.Fatalf("native exit failure lost classification: %v", runErr)
			}
		case "canceled":
			if !errors.Is(runErr, context.Canceled) || cli.MapError(runErr).Code != cli.CodeCanceled {
				t.Fatalf("native cancellation lost cause or classification: %v", runErr)
			}
		}
		if !slices.Equal(editor, original) || configured[len(original)] != "spare argv sentinel" {
			t.Fatal("native supervisor changed configured argv")
		}
	}
	if windowsEditorMode(t, input) != beforeInput || windowsEditorMode(t, diagnostics) != beforeOutput {
		t.Fatal("native supervisor did not restore both borrowed console modes")
	}
	t.Log("console-owner-verified:" + mode)
}

func TestWindowsEditorProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_CONSOLE_EDITOR") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 5 {
		os.Exit(30)
	}
	args := os.Args[index+1:]
	// Capture refusal must detect entry even when the inherited diagnostics
	// cannot supply console modes. Publish this marker before terminal access.
	if args[0] == "capture_failure" {
		if err := os.WriteFile(args[1], []byte("launched before capture"), 0o600); err != nil {
			os.Exit(37)
		}
		os.Exit(0)
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		os.Exit(31)
	}
	output := windows.Handle(os.Stderr.Fd())
	var outputMode uint32
	if err := windows.GetConsoleMode(output, &outputMode); err != nil {
		os.Exit(32)
	}
	if err := windows.SetConsoleMode(output, outputMode^windows.ENABLE_WRAP_AT_EOL_OUTPUT); err != nil {
		os.Exit(33)
	}
	_, _ = fmt.Fprintln(os.Stdout, "editor-screen-sentinel")
	raw, err := json.Marshal(struct {
		PID  uint32
		Args []string
	}{uint32(os.Getpid()), args})
	if err != nil || os.WriteFile(args[1], raw, 0o600) != nil {
		os.Exit(34)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(args[2]); err == nil {
			if args[0] == "failure" {
				os.Exit(35)
			}
			os.Exit(0)
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(36)
}

func windowsEditorTerminal(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("native console allocation was not usable: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func windowsEditorMode(t *testing.T, file *os.File) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(file.Fd()), &mode); err != nil {
		t.Fatalf("native console mode read failed: %v", err)
	}
	return mode
}
