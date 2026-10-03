package issue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// writeAttachmentOverwrite keeps the old entry until a complete, synced
// download can replace it. The pinned x/os.AtomicWrite accepts buffered bytes
// and path strings; this writer requires streaming and one retained os.Root
// so download size and directory/symlink changes cannot escape that boundary.
func writeAttachmentOverwrite(ctx context.Context, client core.JiraClient, id, dir, name string) (int64, bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, false, createInDirError(dir, name, true, err)
	}
	defer root.Close() //nolint:errcheck // root owns path resolution, not buffered file contents
	info, err := root.Stat(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, false, createInDirError(dir, name, true, err)
	}
	existed := err == nil
	if existed && !info.Mode().IsRegular() {
		return 0, false, createInDirError(dir, name, true, fmt.Errorf("overwrite target is not a regular file"))
	}
	stage := ".jira-download-" + output.NewRequestID() + ".tmp"
	file, err := root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, false, createInDirError(dir, name, true, err)
	}
	defer root.Remove(stage) //nolint:errcheck // removes only staging; after rename this name is absent
	defer file.Close()       //nolint:errcheck // successful close is checked before publication
	path := filepath.Join(dir, name)
	written, err := copyAttachment(ctx, client, id, file, path)
	if err != nil {
		return 0, false, err
	}
	if err := file.Close(); err != nil {
		return 0, false, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("closing download for %s: %v", path, err))
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if err := root.Rename(stage, name); err != nil {
		return 0, false, createInDirError(dir, name, true, err)
	}
	// Match x/os.AtomicWrite's Unix durability policy through the same root.
	// A sync error follows publication: leave the new file and report that fact.
	if xos.IsUnix() {
		directory, err := root.Open(".")
		if err != nil {
			return 0, false, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("download saved at %s; directory sync unavailable: %v; inspect the file before retrying", path, err))
		}
		defer directory.Close() //nolint:errcheck // no buffered writes on this directory descriptor
		if err := directory.Sync(); err != nil {
			return 0, false, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("download saved at %s; directory sync failed: %v; inspect the file before retrying", path, err))
		}
	}
	return written, existed, nil
}
