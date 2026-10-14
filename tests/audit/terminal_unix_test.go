//go:build outputaudit && (linux || darwin)

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/terminal"
	"golang.org/x/sys/unix"
)

const auditUnixFixtureOutput = "independent Unix stdout 古い\n\x1b[0m\n"

type auditUnixFixtureReady struct {
	PID  int
	Mode string
}

func runAuditTerminalFixture(args []string) error {
	if len(args) != 2 || !terminal.Is(os.Stdin) || !terminal.Is(os.Stderr) || terminal.Is(os.Stdout) {
		return errors.New("Unix fixture requires native terminal input/diagnostics and separate stdout")
	}
	if _, err := os.Stdout.Write([]byte(auditUnixFixtureOutput)); err != nil {
		return err
	}
	switch args[0] {
	case "quiet", "wait":
	case "invisible":
		if _, err := os.Stderr.Write([]byte("\x1b[0m")); err != nil {
			return err
		}
	case "unicode":
		if _, err := os.Stderr.Write([]byte("independent diagnostic 古い")); err != nil {
			return err
		}
	case "oversize":
		if _, err := os.Stderr.Write(bytes.Repeat([]byte("x"), (1<<20)+1)); err != nil {
			return err
		}
	default:
		return errors.New("invalid Unix terminal fixture mode")
	}
	data, err := json.Marshal(auditUnixFixtureReady{PID: os.Getpid(), Mode: args[0]})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if args[0] == "wait" {
		time.Sleep(time.Minute)
		return errors.New("owned Unix fixture was not terminated")
	}
	return nil
}

func auditUnixCommand(t *testing.T, ctx context.Context, mode string) (*exec.Cmd, string, *bytes.Buffer) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	ready := filepath.Join(directory, "ready literal 古い ; &.json")
	cmd := exec.CommandContext(ctx, self, "--audit-terminal-fixture", mode, ready)
	cmd.Dir, cmd.Env = directory, scenarioEnv(directory, Scenario{}, "", ModeJSON)
	stdout := new(bytes.Buffer)
	cmd.Stdout = stdout
	return cmd, ready, stdout
}

func TestAuditUnixTerminalBytes(t *testing.T) {
	for _, test := range []struct{ mode, diagnostics string }{
		{mode: "quiet"},
		{mode: "invisible", diagnostics: "\x1b[0m"},
		{mode: "unicode", diagnostics: "independent diagnostic 古い"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd, ready, stdout := auditUnixCommand(t, ctx, test.mode)
			var diagnostics bytes.Buffer
			code, err := runAuditCommand(ctx, cmd, true, &diagnostics)
			if err != nil || code != 0 || stdout.String() != auditUnixFixtureOutput || diagnostics.String() != test.diagnostics {
				t.Fatalf("native Unix capture lost independent bytes: exit=%d err=%v stdout=%q diagnostics=%q", code, err, stdout.String(), diagnostics.String())
			}
			data, err := os.ReadFile(ready)
			var receipt auditUnixFixtureReady
			if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Mode != test.mode || receipt.PID != cmd.ProcessState.Pid() {
				t.Fatalf("native Unix helper did not actually complete: %v; %q", err, data)
			}
		})
	}
}

func TestAuditUnixTerminalDiagnosticBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd, _, stdout := auditUnixCommand(t, ctx, "oversize")
	var diagnostics bytes.Buffer
	_, err := runAuditCommand(ctx, cmd, true, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "terminal diagnostics exceed their byte bound") || ctx.Err() != nil || stdout.String() != auditUnixFixtureOutput || diagnostics.Len() > 1<<20 || cmd.ProcessState == nil {
		t.Fatalf("Unix diagnostic bound did not enter and join its refusal: %v; captured=%d stdout=%q", err, diagnostics.Len(), stdout.String())
	}
}

type auditRetainedPTYWriter struct {
	command   *exec.Cmd
	published chan *os.File
}

func (w *auditRetainedPTYWriter) Write(data []byte) (int, error) {
	file, ok := w.command.Stderr.(*os.File)
	if !ok || !terminal.Is(file) {
		return 0, errors.New("native retained-writer fixture did not receive a terminal")
	}
	descriptor, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	duplicate := os.NewFile(uintptr(descriptor), "owned retained PTY writer")
	select {
	case w.published <- duplicate:
	default:
		if err := duplicate.Close(); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func TestAuditUnixCollectorBoundedAfterCommand(t *testing.T) {
	for _, callerDeadline := range []bool{true, false} {
		name := "cleanup-timeout"
		limit := 8 * time.Second
		if callerDeadline {
			name, limit = "caller-deadline", 2*time.Second
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), limit)
			defer cancel()
			cmd, _, _ := auditUnixCommand(t, ctx, "quiet")
			published := make(chan *os.File, 1)
			cmd.Stdout = &auditRetainedPTYWriter{command: cmd, published: published}
			done := make(chan error, 1)
			var diagnostics bytes.Buffer
			go func() { _, err := runAuditCommand(ctx, cmd, true, &diagnostics); done <- err }()
			joined := false
			var duplicate *os.File
			defer func() {
				cancel()
				if duplicate != nil {
					if err := duplicate.Close(); err != nil {
						t.Error(err)
					}
				}
				if !joined {
					<-done
				}
			}()
			select {
			case duplicate = <-published:
			case err := <-done:
				joined = true
				t.Fatal("native helper never published its retained writer", err)
			case <-ctx.Done():
				t.Fatal("native helper never entered", context.Cause(ctx))
			}
			if callerDeadline {
				<-ctx.Done()
			}
			var err error
			wait := limit + 500*time.Millisecond
			if callerDeadline {
				wait = 500 * time.Millisecond
			}
			select {
			case err = <-done:
				joined = true
			case <-time.After(wait):
				if closeErr := duplicate.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				duplicate = nil
				<-done
				joined = true
				if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
					t.Fatal("native retained-writer helper did not actually exit successfully", cmd.ProcessState)
				}
				t.Fatal("terminal collector ignored its bound while an owned duplicate retained its writer")
			}
			if cmd.ProcessState == nil || !cmd.ProcessState.Success() || diagnostics.Len() != 0 {
				t.Fatalf("native quiet helper did not actually exit silently: %v; %v", cmd.ProcessState, err)
			}
			if callerDeadline {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("collector dropped the caller deadline", err)
				}
			} else if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "terminal diagnostic collector did not reach EOF") {
				t.Fatal("collector did not retain its independent drain timeout", err)
			}
		})
	}
}

func TestAuditUnixTerminalStartFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	directory := t.TempDir()
	cmd := exec.CommandContext(ctx, filepath.Join(directory, "missing-owned-executable"))
	cmd.Dir, cmd.Env, cmd.Stdout = directory, scenarioEnv(directory, Scenario{}, "", ModeJSON), new(bytes.Buffer)
	var diagnostics bytes.Buffer
	_, err := runAuditCommand(ctx, cmd, true, &diagnostics)
	if !errors.Is(err, os.ErrNotExist) || cmd.Process != nil || ctx.Err() != nil || diagnostics.Len() != 0 {
		t.Fatalf("Unix capture lost its actual launch refusal or failed to join: %v", err)
	}
}

func TestAuditUnixTerminalRunningCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cmd, ready, stdout := auditUnixCommand(t, ctx, "wait")
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() { _, err := runAuditCommand(ctx, cmd, true, &diagnostics); done <- err }()
	joined := false
	defer func() {
		cancel(nil)
		if !joined {
			<-done
		}
	}()
	waitContext, stopWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopWait()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(ready)
		var receipt auditUnixFixtureReady
		if err == nil && json.Unmarshal(data, &receipt) == nil && receipt.Mode == "wait" && receipt.PID > 0 {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			joined = true
			t.Fatal("native fixture exited before cancellation", err)
		case <-waitContext.Done():
			t.Fatal("native fixture never became ready", context.Cause(waitContext))
		case <-ticker.C:
		}
	}
	cause := errors.New("owned cancellation after Unix terminal fixture entry")
	cancel(cause)
	err := <-done
	joined = true
	if !errors.Is(err, cause) || cmd.ProcessState == nil || cmd.ProcessState.Success() || diagnostics.Len() != 0 || stdout.String() != auditUnixFixtureOutput {
		t.Fatalf("native running cancellation lost its cause, bytes or joined process: %v; state=%v stdout=%q diagnostics=%q", err, cmd.ProcessState, stdout.String(), diagnostics.String())
	}
}
