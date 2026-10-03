//go:build unix

package attachmentfiles

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenRefusesFIFOWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unopened.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestOpenFIFOHelper$")
	cmd.Env = []string{"JIRA_ATTACHMENT_FIFO_HELPER=" + path}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("FIFO handling blocked or failed its type refusal: %v %s", err, out)
	}
}

func TestOpenFIFOHelper(t *testing.T) {
	path := os.Getenv("JIRA_ATTACHMENT_FIFO_HELPER")
	if path == "" {
		t.Skip("subprocess-only FIFO probe")
	}
	files, err := Open(t.Context(), []string{path})
	Close(files)
	if !errors.Is(err, ErrInvalid) || len(files) != 0 {
		t.Fatalf("FIFO was accepted as an upload file: %v", err)
	}
}
