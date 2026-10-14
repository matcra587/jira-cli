package releaseacceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/tests/internal/nativejob"
)

// The helper waits for admission before constructing a CLI child. Assignment
// to the owned job therefore happens before any descendant can escape it.
func waitPagingAdmission(t *testing.T) {
	t.Helper()
	path := os.Getenv("JIRA_TEST_PAGING_ADMISSION")
	if path == "" {
		t.Fatal("native paging helper has no admission boundary")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("native paging job admission timed out")
		case <-ticker.C:
		}
	}
}

func runPagingConsole(t *testing.T, self, binary, mode string) {
	t.Helper()
	job, err := nativejob.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := job.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	admission, ready := filepath.Join(dir, "admitted"), filepath.Join(dir, "cli-ready.json")
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestReleaseNotesWindowsProcess$")
	cmd.Env = append(pagingEnvironment(dir), pagingHelper+"=console", "JIRA_TEST_PAGING_BINARY="+binary, "JIRA_TEST_PAGING_MODE="+mode, "JIRA_TEST_PAGING_ADMISSION="+admission, "JIRA_TEST_PAGING_READY="+ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	cmd.Cancel = func() error { return job.Terminate(1) }
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-finished
		}
		if err := job.Terminate(1); err != nil {
			t.Error(err)
		}
		cleanupContext, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stopCleanup()
		if err := job.WaitEmpty(cleanupContext); err != nil {
			t.Error(err)
		}
	}()
	if err := job.AssignPID(uint32(cmd.Process.Pid)); err != nil {
		// Admission has not been published, so no CLI child exists yet.
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			t.Error(killErr)
		}
		t.Fatal(err)
	}

	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(admission, []byte("owned job assigned"), 0o600); err != nil {
		t.Fatal(err)
	}
	var descendant windows.Handle
	if mode == "parent canceled" {
		descendant = waitPagingChild(t, ctx, ready, finished, &joined)
		cancel()
	}
	runErr := <-finished
	joined = true
	if mode == "parent canceled" {
		state, waitErr := windows.WaitForSingleObject(descendant, 5000)
		closeErr := windows.CloseHandle(descendant)
		if err := errors.Join(waitErr, closeErr); err != nil || state != windows.WAIT_OBJECT_0 || runErr == nil || !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("parent cancellation did not join the actual CLI descendant", state, runErr, err)
		}
		return
	}
	if runErr != nil || !strings.Contains(output.String(), "windows-paging-verified:"+mode+"\n") {
		t.Fatalf("native paging helper failed: %v\n%s", runErr, output.String())
	}
}

func waitPagingChild(t *testing.T, ctx context.Context, ready string, finished <-chan error, joined *bool) windows.Handle {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(ready) //nolint:gosec // G304: owned helper's readiness receipt.
		if err == nil {
			var pid uint32
			if json.Unmarshal(data, &pid) == nil && pid != 0 {
				process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
				if err != nil {
					t.Fatal(err)
				}
				state, err := windows.WaitForSingleObject(process, 0)
				if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
					closeErr := windows.CloseHandle(process)
					t.Fatal("CLI was not live at parent cancellation", state, errors.Join(err, closeErr))
				}
				return process
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-finished:
			*joined = true
			t.Fatal("native CLI never reached builtin readiness", err)
		case <-ctx.Done():
			t.Fatal("native CLI readiness timed out", ctx.Err())
		case <-ticker.C:
		}
	}
}
