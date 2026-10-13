package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Recovery uses removal only for a destination absent in its verified preimage.
// Preserve snapshot checks and the acknowledged removal if directory sync fails.
func (f *File) remove(ctx context.Context) (bool, error) {
	return f.removeWith(ctx, os.Remove, syncCreationDirectory)
}

func (f *File) removeWith(ctx context.Context, remove, syncDirectory func(string) error) (bool, error) {
	if err := f.checkSnapshot(ctx); err != nil {
		return false, err
	}
	if !f.exists {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := remove(f.path); err != nil {
		return false, fmt.Errorf("remove migrated config: %w", err)
	}
	if err := syncDirectory(filepath.Dir(f.path)); err != nil {
		return true, fmt.Errorf("sync directory after config removal: %w", err)
	}
	return true, nil
}
