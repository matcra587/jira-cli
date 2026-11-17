//go:build linux

package output

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/core"
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

func TestProgramStatusBulkProgressAndFinalFailureSummary(t *testing.T) {
	t.Setenv("TERM", "xterm")
	master, terminal := documentTestTerminal(t)
	ctx, finish := WithProgramStatus(t.Context(), terminal)
	StartProgramStatus(ctx)
	for i := int64(0); i <= 2; i++ {
		core.ObserveProgress(ctx, core.Progress{Phase: core.ProgressBulkResults, Completed: i, Total: 2, Failed: i / 2, Complete: i == 2})
	}
	finish(clog.ProgramStateDone)
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(master)
	if err != nil && !errors.Is(err, unix.EIO) {
		t.Fatal(err)
	}
	reports := decodeProgramReports(t, raw)
	if len(reports) != 5 || reports[1]["progress"] != "0" || reports[2]["progress"] != "50" || reports[3]["progress"] != "100" {
		t.Fatalf("missing measured bulk progress: %+v", reports)
	}
	last := reports[4]
	if last["state"] != "done" || last["title"] != "Bulk results" || last["msg"] != "2/2 results, 1 failed" {
		t.Fatalf("completion lost partial failures: %+v", last)
	}
}

func decodeProgramReports(t *testing.T, raw []byte) []map[string]string {
	t.Helper()
	var reports []map[string]string
	for _, part := range strings.Split(string(raw), "\x1b]7501;")[1:] {
		body, _, found := strings.Cut(part, "\a")
		if !found {
			t.Fatal("unterminated status record")
		}
		report := make(map[string]string)
		for _, field := range strings.Split(body, ":") {
			key, value, found := strings.Cut(field, "=")
			if !found {
				t.Fatalf("malformed status field %q", field)
			}
			if key == "title" || key == "msg" {
				decoded, err := base64.StdEncoding.DecodeString(value)
				if err != nil {
					t.Fatal(err)
				}
				value = string(decoded)
			}
			report[key] = value
		}
		reports = append(reports, report)
	}
	return reports
}

func TestProgramStatusUpdatePhasesClearDownloadPercentage(t *testing.T) {
	t.Setenv("TERM", "xterm")
	master, terminal := documentTestTerminal(t)
	ctx, finish := WithProgramStatus(t.Context(), terminal)
	StartProgramStatus(ctx)
	for _, phase := range []core.ProgressPhase{core.ProgressUpdateChecksums, core.ProgressUpdateVerifyManifest, core.ProgressUpdateDownload, core.ProgressUpdateVerifyArchive, core.ProgressUpdateExtract, core.ProgressUpdateInstall, core.ProgressUpdateInstalled} {
		progress := core.Progress{Phase: phase}
		if phase == core.ProgressUpdateDownload {
			progress.Completed, progress.Total = 1, 2
		}
		core.ObserveProgress(ctx, progress)
	}
	finish(clog.ProgramStateDone)
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(master)
	if err != nil && !errors.Is(err, unix.EIO) {
		t.Fatal(err)
	}
	reports := decodeProgramReports(t, raw)
	if len(reports) != 9 || reports[2]["msg"] != "Verifying release signature" || reports[3]["progress"] != "50" {
		t.Fatalf("update phases disappeared: %+v", reports)
	}
	for _, report := range reports[4:] {
		if _, stale := report["progress"]; stale {
			t.Fatalf("download percentage survived verification/publication: %+v", reports)
		}
	}
	if reports[8]["state"] != "done" || reports[8]["msg"] != "Update installed" {
		t.Fatalf("missing final observed installation: %+v", reports[8])
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
