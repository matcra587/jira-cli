//go:build linux

package output

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"golang.org/x/sys/unix"
)

func TestProgramStatusTerminalLifecycleAndNestedInput(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	master, terminal := documentTestTerminal(t)
	ctx, finish := WithProgramStatus(t.Context(), terminal)
	StartProgramStatus(ctx)
	StartProgramStatus(ctx)
	outer := WaitForProgramInput(ctx)
	inner := WaitForProgramInput(ctx)
	outer()
	outer()
	inner()
	finish(clog.ProgramStateDone)
	StartProgramStatus(ctx)
	WaitForProgramInput(ctx)()
	finish(clog.ProgramStateError)
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(master)
	if err != nil && !errors.Is(err, unix.EIO) {
		t.Fatal(err)
	}
	want := "\x1b]7501;state=working:app=jira\a" +
		"\x1b]7501;state=blocked:kind=question:app=jira\a" +
		"\x1b]7501;state=working:app=jira\a" +
		"\x1b]7501;state=done:app=jira\a"
	if string(raw) != want {
		t.Fatalf("terminal lifecycle = %q, want %q", raw, want)
	}
}

func TestProgramStatusCanceledInputDoesNotResumeWork(t *testing.T) {
	t.Setenv("TERM", "xterm")
	master, terminal := documentTestTerminal(t)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx, finish := WithProgramStatus(parent, terminal)
	StartProgramStatus(ctx)
	resume := WaitForProgramInput(ctx)
	cancel()
	resume()
	finish(clog.ProgramStateIdle)
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(master)
	if err != nil && !errors.Is(err, unix.EIO) {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "state=working") != 1 || !strings.HasSuffix(string(raw), "\x1b]7501;state=idle:app=jira\a") {
		t.Fatalf("canceled input resumed or completed work: %q", raw)
	}
}
