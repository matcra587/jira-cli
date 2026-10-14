//go:build outputaudit

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestAuditNativeTerminalCaptureBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized-control-stream")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), terminalCaptureLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var capture boundedAuditBuffer
	written, copyErr := io.Copy(&capture, file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if copyErr == nil || !capture.failed || written > terminalCaptureLimit || capture.Len() > terminalCaptureLimit {
		t.Fatalf("native control stream escaped its byte bound through io.Copy: written=%d kept=%d failed=%v err=%v", written, capture.Len(), capture.failed, copyErr)
	}
}

func TestAuditNativeTerminalRequiresAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd, ready, _ := nativeTerminalCommand(t, ctx, "quiet")
	control := t.TempDir()
	args := []string{"--audit-terminal", control, strconv.FormatInt(time.Now().Add(2*time.Second).UnixNano(), 10)}
	args = append(args, cmd.Args...)
	helper := exec.CommandContext(ctx, cmd.Path, args...)
	helper.Dir, helper.Env = cmd.Dir, cmd.Env
	helper.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	helper.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	helper.Stdout, helper.Stderr = &stdout, &stderr
	err := helper.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "context deadline exceeded") {
		t.Fatalf("unadmitted helper did not enter its exact deadline refusal: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(control, "console.ready")); err != nil {
		t.Fatal("native console did not actually become ready", err)
	}
	if _, err := os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unadmitted helper entered the CLI fixture: %v", err)
	}
}

const auditTerminalFixtureOutput = "independent native stdout 古い\n\x1b[0m\n"

type terminalFixtureReady struct {
	PID    uint32
	Parent uint32
}

func runAuditTerminalFixture(args []string) error {
	if len(args) != 2 {
		return errors.New("native terminal fixture requires a mode and owned receipt")
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode); err != nil {
		return err
	}
	if err := windows.GetConsoleMode(windows.Handle(os.Stderr.Fd()), &mode); err != nil {
		return err
	}
	if _, err := os.Stdout.Write([]byte(auditTerminalFixtureOutput)); err != nil {
		return err
	}
	switch args[0] {
	case "quiet", "wait":
	case "invisible":
		if _, err := os.Stderr.Write([]byte("\x1b[0m")); err != nil {
			return err
		}
	case "unicode":
		if _, err := os.Stderr.Write([]byte("independent diagnostic 古い\n")); err != nil {
			return err
		}
	default:
		return errors.New("invalid native terminal fixture mode")
	}
	encoded, err := json.Marshal(terminalFixtureReady{PID: uint32(os.Getpid()), Parent: uint32(os.Getppid())})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if args[0] == "wait" {
		time.Sleep(time.Minute)
		return errors.New("native fixture was not terminated by its owner")
	}
	return nil
}

func nativeTerminalCommand(t *testing.T, ctx context.Context, mode string) (*exec.Cmd, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "ready.json")
	cmd := exec.CommandContext(ctx, self, "--audit-terminal-fixture", mode, ready)
	cmd.Dir, cmd.Env = dir, scenarioEnv(dir, Scenario{}, "", ModeJSON)
	stdout := new(bytes.Buffer)
	cmd.Stdout = stdout
	return cmd, ready, stdout
}

func TestAuditNativeTerminalDiagnostics(t *testing.T) {
	for _, mode := range []string{"quiet", "invisible", "unicode"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd, ready, stdout := nativeTerminalCommand(t, ctx, mode)
			var stderr bytes.Buffer
			code, err := runAuditCommand(ctx, cmd, true, &stderr)
			raw, readErr := os.ReadFile(ready)
			var receipt terminalFixtureReady
			if readErr != nil || json.Unmarshal(raw, &receipt) != nil || receipt.PID == 0 || receipt.Parent == 0 || receipt.PID == receipt.Parent {
				t.Fatalf("native writer fixture did not actually complete its writes: %v; %q", readErr, raw)
			}
			if mode == "quiet" {
				if err != nil || code != 0 || stdout.String() != auditTerminalFixtureOutput || stderr.Len() != 0 {
					t.Fatalf("native quiet capture lost exact stdout: %v; exit=%d stdout=%q stderr=%q", err, code, stdout.String(), stderr.String())
				}
				return
			}
			if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "native audit silence refused:") || !strings.Contains(err.Error(), "nonzero diagnostic write attempt:") || stdout.Len() != 0 {
				t.Fatalf("native diagnostic output was incorrectly admitted: %v; stdout=%q", err, stdout.String())
			}
		})
	}
}

type cancelingAuditWriter struct {
	buffer bytes.Buffer
	cancel context.CancelCauseFunc
	cause  error
}

func (w *cancelingAuditWriter) Write(data []byte) (int, error) {
	w.cancel(w.cause)
	return w.buffer.Write(data)
}

func TestAuditNativeTerminalPublicationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("owned cancellation during native stdout publication")
	cmd, ready, _ := nativeTerminalCommand(t, ctx, "quiet")
	writer := &cancelingAuditWriter{cancel: cancel, cause: cause}
	cmd.Stdout = writer
	var stderr bytes.Buffer
	code, err := runAuditCommand(ctx, cmd, true, &stderr)
	if code != 0 || !errors.Is(err, cause) || err.Error() != cause.Error() || writer.buffer.String() != auditTerminalFixtureOutput || stderr.Len() != 0 {
		t.Fatalf("native stdout publication lost cancellation or acknowledged bytes: exit=%d err=%v stdout=%q stderr=%q", code, err, writer.buffer.String(), stderr.String())
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("native fixture did not actually execute", err)
	}
}

func TestAuditNativeTerminalParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, ready, _ := nativeTerminalCommand(t, ctx, "wait")
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := runAuditCommand(ctx, cmd, true, &stderr)
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	waitContext, stopWait := context.WithTimeout(ctx, 10*time.Second)
	defer stopWait()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var receipt terminalFixtureReady
	for receipt.PID == 0 || receipt.Parent == 0 {
		raw, err := os.ReadFile(ready)
		if err == nil {
			var candidate terminalFixtureReady
			if json.Unmarshal(raw, &candidate) == nil {
				receipt = candidate
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-waitContext.Done():
			t.Fatal("native fixture never became live", context.Cause(waitContext))
		case err := <-done:
			joined = true
			t.Fatal("native capture exited before cancellation", err)
		case <-ticker.C:
		}
	}
	var handles []windows.Handle
	defer func() {
		for _, handle := range handles {
			// Only the independently reported owned fixture and supervisor are
			// targeted; this also cleans up a future failed ownership mutation.
			if state, err := windows.WaitForSingleObject(handle, 0); err == nil && state == uint32(windows.WAIT_TIMEOUT) {
				if err := windows.TerminateProcess(handle, 1); err != nil {
					t.Error(err)
				}
			}
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, pid := range []uint32{receipt.PID, receipt.Parent} {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
		if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("owned process was not live before cancellation: %d; %v", state, err)
		}
	}
	cancel()
	err := <-done
	joined = true
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
		t.Fatalf("parent cancellation lost its outcome or cleanup failed: %v", err)
	}
	for _, handle := range handles {
		if state, err := windows.WaitForSingleObject(handle, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("owned supervisor or CLI survived parent cancellation: %d; %v", state, err)
		}
	}
}
