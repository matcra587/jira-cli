//go:build unix

package config_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestSavedQueryNonregularInputKeepsSharedInvalidCause(t *testing.T) {
	if dir := os.Getenv("JIRA_TEST_QUERY_INPUT"); dir != "" {
		queries, err := config.LoadQueries(t.Context(), dir)
		if !errors.Is(err, config.ErrFileInvalid) || queries != nil {
			t.Fatalf("nonregular saved query lost its invalid-input cause or returned usable queries: %v, %v", queries, err)
		}
		return
	}
	t.Parallel()
	for _, link := range []bool{false, true} {
		name := "direct"
		if link {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "queries")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "inbox.jql")
			target := path
			if link {
				target = filepath.Join(root, "input")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := unix.Mkfifo(target, 0o600); err != nil {
				t.Fatal(err)
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, self, "-test.run=^TestSavedQueryNonregularInputKeepsSharedInvalidCause$")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "JIRA_TEST_QUERY_INPUT=") {
					child.Env = append(child.Env, value)
				}
			}
			child.Env = append(child.Env, "JIRA_TEST_QUERY_INPUT="+dir)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("nonregular query refusal failed or blocked; child was joined: %v\n%s", err, output)
			}
		})
	}
}

func TestSavedQuerySymlinkReadKeepsNativeCause(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.jql")
	if err := os.Symlink(filepath.Join(dir, "missing"), path); err != nil {
		t.Fatal(err)
	}
	queries, err := config.LoadQueries(t.Context(), dir)
	var native *os.PathError
	if !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &native) || native.Op != "open" || native.Path != path || queries != nil {
		t.Fatalf("saved-query read lost its native missing-target cause or returned usable queries: %v, %v", queries, err)
	}
}
