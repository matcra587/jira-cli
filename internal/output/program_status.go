package output

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/gechr/clog"
)

type programStatusKey struct{}

type programStatus struct {
	mu       sync.Mutex
	output   *clog.Output
	active   bool
	finished bool
	waiting  int
}

// WithProgramStatus binds terminal status to one invocation. Reports remain
// dormant until StartProgramStatus, after the final output mode is resolved.
// The returned function completes an active report and ignores later updates.
func WithProgramStatus(ctx context.Context, destination io.Writer) (context.Context, func(clog.ProgramState)) {
	var status *programStatus
	if os.Getenv("TERM") != "dumb" {
		out := clog.NewOutput(destination, clog.ColorNever)
		// The process logger owns live stderr animations. Reuse its exact
		// output for the same file, while retaining injected destinations.
		if file, ok := destination.(*os.File); ok {
			shared := clog.Ctx(ctx).Output()
			if writer, ok := shared.Writer().(*os.File); ok && writer == file {
				out = shared
			}
		}
		if out.IsTTY() {
			status = &programStatus{output: out}
		}
	}
	return context.WithValue(ctx, programStatusKey{}, status), func(state clog.ProgramState) {
		if status == nil {
			return
		}
		status.mu.Lock()
		defer status.mu.Unlock()
		if status.active && !status.finished {
			status.report(state)
		}
		status.finished = true
	}
}

// StartProgramStatus reports work only when the runtime has selected human
// output for a person. Machine modes and completion callbacks never call it.
func StartProgramStatus(ctx context.Context) {
	status, _ := ctx.Value(programStatusKey{}).(*programStatus)
	if status == nil {
		return
	}
	status.mu.Lock()
	defer status.mu.Unlock()
	if !status.active && !status.finished {
		status.active = true
		status.report(clog.ProgramStateWorking)
	}
}

// WaitForProgramInput marks a shared form's input scope. Its idempotent cleanup
// restores working only after every nested input scope has returned.
func WaitForProgramInput(ctx context.Context) func() {
	status, _ := ctx.Value(programStatusKey{}).(*programStatus)
	if status == nil {
		return func() {}
	}
	status.mu.Lock()
	if !status.active || status.finished {
		status.mu.Unlock()
		return func() {}
	}
	status.waiting++
	if status.waiting == 1 {
		status.report(clog.ProgramStateBlocked)
	}
	status.mu.Unlock()
	return sync.OnceFunc(func() {
		status.mu.Lock()
		defer status.mu.Unlock()
		status.waiting--
		if status.waiting == 0 && !status.finished && ctx.Err() == nil {
			status.report(clog.ProgramStateWorking)
		}
	})
}

// Keep reports independent of Jira content, credentials and error prose.
// Clog owns encoding and serialization with its live terminal output.
func (s *programStatus) report(state clog.ProgramState) {
	report := clog.ProgramStatus{State: state, App: "jira"}
	if state == clog.ProgramStateBlocked {
		report.Kind = clog.ProgramStatusKindQuestion
	}
	s.output.SetProgramStatus(report)
}
