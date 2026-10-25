//go:build unix

package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestUpdateProcessCancelsDescendants(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIRA_OWNED_UPDATE_HELPER", "1")
	t.Setenv("JIRA_OWNED_UPDATE_PROCESS_ROOT", root)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, _, err := runUpdateProcess(ctx, program, "-test.run=^TestUpdateProcessHelper$", "--", "descendant parent")
		finished <- err
	}()
	// Always join the owned runner, including a readiness assertion failure.
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Logf("update runner cleanup: %v", err)
		}
	}()
	var child *os.Process
	for child == nil {
		pidData, readErr := os.ReadFile(filepath.Join(root, "pid"))
		if readErr == nil {
			pid, parseErr := strconv.Atoi(string(pidData))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			child, err = os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned descendant did not start")
		case <-time.After(time.Millisecond):
		}
	}
	defer func() {
		// A compiling mutation must not leave the intentionally orphaned child.
		_ = child.Kill()
		_ = child.Release()
	}()
	cancel()
	var result error
	select {
	case result = <-finished:
		// Put the terminal result back for the unconditional join above.
		finished <- result
	case <-time.After(3 * time.Second):
		t.Fatal("owned runner did not stop")
	}
	if !errors.Is(result, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", result)
	}
	before, err := os.ReadFile(filepath.Join(root, "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	after, err := os.ReadFile(filepath.Join(root, "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("installer descendant continued after cancellation: heartbeat %q became %q", before, after)
	}
}
