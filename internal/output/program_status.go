package output

import (
	"context"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sync"

	"github.com/gechr/clog"
	"github.com/gechr/x/human"

	"github.com/matcra587/jira-cli/internal/core"
)

type programStatusKey struct{}

type programStatus struct {
	mu       sync.Mutex
	output   *clog.Output
	active   bool
	finished bool
	waiting  int
	progress core.Progress
	last     clog.ProgramStatus
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
	ctx = context.WithValue(ctx, programStatusKey{}, status)
	var observer func(core.Progress)
	if status != nil {
		observer = status.observe
	}
	return core.WithProgressObserver(ctx, observer), func(state clog.ProgramState) {
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
	applyProgramProgress(&report, s.progress)
	if state == clog.ProgramStateBlocked {
		report.Kind = clog.ProgramStatusKindQuestion
	}
	// Coalesce large walks to percentage/stage changes. The newest counts
	// remain stored for the final summary, including partial failures.
	if state == clog.ProgramStateWorking && s.last.State == state &&
		report.Title == s.last.Title && (report.HasProgress || report.Message == s.last.Message) && report.HasProgress == s.last.HasProgress &&
		report.Progress == s.last.Progress {
		return
	}
	s.last = report
	s.output.SetProgramStatus(report)
}

func (s *programStatus) observe(progress core.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || s.finished {
		return
	}
	s.progress = progress
	// Huh owns terminal rendering during input. Retain concurrent observations
	// for resume/finalization without inserting controls into its frames.
	if s.waiting > 0 {
		return
	}
	s.report(clog.ProgramStateWorking)
}

func applyProgramProgress(report *clog.ProgramStatus, progress core.Progress) {
	switch progress.Phase {
	case core.ProgressBulkResults:
		report.Title = "Bulk results"
		report.Message = fmt.Sprintf("%d/%d results, %d failed", progress.Completed, progress.Total, progress.Failed)
	case core.ProgressUpdateDiscover:
		report.Title, report.Message = "Update", "Checking releases"
	case core.ProgressUpdateChecksums:
		report.Title, report.Message = "Update", "Reading signed checksums"
	case core.ProgressUpdateVerifyManifest:
		report.Title, report.Message = "Update", "Verifying release signature"
	case core.ProgressUpdateDownload:
		report.Title = "Update download"
		report.Message = fmt.Sprintf("%s/%s received", human.FormatIECBytes(float64(progress.Completed)), human.FormatIECBytes(float64(progress.Total)))
	case core.ProgressUpdateVerifyArchive:
		report.Title, report.Message = "Update", "Verifying downloaded archive"
	case core.ProgressUpdateExtract:
		report.Title, report.Message = "Update", "Extracting verified executable"
	case core.ProgressUpdateInstall:
		report.Title, report.Message = "Update", "Installing executable"
	case core.ProgressUpdateInstalled:
		report.Title, report.Message = "Update", "Update installed"
	case core.ProgressUpdateBrew:
		report.Title, report.Message = "Update", "Updating with Homebrew"
	case core.ProgressCacheResources:
		report.Title = "Cache refresh"
		report.Message = fmt.Sprintf("%d/%d resource results, %d failed", progress.Completed, progress.Total, progress.Failed)
	case core.ProgressCacheInspect:
		report.Title, report.Message = "Cache", "Inspecting cached metadata"
	case core.ProgressCacheRead:
		report.Title, report.Message = "Cache", "Reading metadata"
	case core.ProgressCacheSave:
		report.Title, report.Message = "Cache", "Saving metadata"
	case core.ProgressCacheSaved:
		report.Title, report.Message = "Cache", "Metadata cached"
	case core.ProgressCacheReused:
		report.Title, report.Message = "Cache", "Reusing fresh metadata"
	case core.ProgressCachePreview:
		report.Title, report.Message = "Cache", "Cache preview complete"
	default:
		return
	}
	if progress.Total <= 0 {
		return
	}
	done := min(max(progress.Completed, 0), progress.Total)
	// Multiply without overflow or float rounding to premature completion.
	high, low := bits.Mul64(uint64(done), 100)
	percent, _ := bits.Div64(high, low, uint64(progress.Total))
	report.Progress, report.HasProgress = int(percent), true
	if !progress.Complete {
		report.Progress = min(report.Progress, 99)
	}
}
