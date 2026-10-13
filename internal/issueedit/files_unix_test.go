//go:build unix

package issueedit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestDraftNativeFIFOIsRefusedWithoutOpeningOrRemovingIt(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(path, MarkdownName), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	if data, err := readMember(t.Context(), root, MarkdownName, 1024); data != nil || !errors.Is(err, core.ErrIssueEditDraftInvalid) {
		t.Fatal("FIFO became draft data", err)
	}
	info, err := root.Lstat(MarkdownName)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("refusal removed the changed member", err)
	}
	// Exercise the separate opener directly: it must not wait for a writer even
	// when a regular-file check raced with substitution immediately before open.
	file, err := openMember(root, MarkdownName)
	if err != nil {
		t.Fatal(err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("nonblocking opener did not expose the substituted descriptor", statErr, closeErr)
	}
}
