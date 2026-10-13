package issueedit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestDraftMemberRefusesNonregularSymlinkAndOversizedInputs(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"directory", "symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir()
			if err := os.WriteFile(filepath.Join(path, "regular"), []byte("too long"), 0o600); err != nil {
				t.Fatal(err)
			}
			name := "member"
			switch mode {
			case "directory":
				if err := os.Mkdir(filepath.Join(path, name), 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("regular", filepath.Join(path, name)); err != nil {
					t.Skip("native symlink creation unavailable: ", err)
				}
			case "oversized":
				name = "regular"
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
			if data, err := readMember(t.Context(), root, name, 3); data != nil || !errors.Is(err, core.ErrIssueEditDraftInvalid) {
				t.Fatal("nonregular/oversized input admitted", err)
			}
		})
	}
}

func TestDraftMemberPublicationNeverClobbersExistingName(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, SourceName), []byte("original"), 0o600); err != nil {
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
	if published, err := publishMember(t.Context(), root, SourceName, []byte("replacement"), 0o400, defaultPublicationOps()); published || !errors.Is(err, os.ErrExist) {
		t.Fatal("existing name was replaced", err)
	}
	data, err := root.ReadFile(SourceName)
	entries, listErr := os.ReadDir(path)
	if err != nil || listErr != nil || string(data) != "original" || len(entries) != 1 {
		t.Fatal("clobber refusal changed original or left stages", err, listErr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if published, err := publishMember(ctx, root, MarkdownName, nil, 0o600, defaultPublicationOps()); published || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication entered files", err)
	}
}
