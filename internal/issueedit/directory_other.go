//go:build !windows

package issueedit

import (
	"context"
	"os"
)

func draftEntry(parent *os.Root, name, _ string) (os.FileInfo, error) {
	return parent.Lstat(name)
}

func createDraftDirectory(ctx context.Context, parent *os.Root, name, _ string) (*os.Root, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := parent.Mkdir(name, 0o700); err != nil {
		return nil, false, err
	}
	root, err := parent.OpenRoot(name)
	return root, true, err
}
