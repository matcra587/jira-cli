//go:build unix

package config

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

func TestThemeInputOpenDoesNotWaitForFIFO(t *testing.T) {
	if path := os.Getenv("JIRA_TEST_THEME_FIFO"); path != "" {
		file, err := openThemeInput(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadThemeFile(t.Context(), path); !errors.Is(err, ErrThemeFileInvalid) {
			t.Fatalf("FIFO accepted as a theme file: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "palette.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestThemeInputOpenDoesNotWaitForFIFO$", "-test.count=1")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "JIRA_TEST_THEME_FIFO=" + path}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("theme input blocked or failed on a FIFO: %v; context=%v\n%s", err, ctx.Err(), out)
	}
}
