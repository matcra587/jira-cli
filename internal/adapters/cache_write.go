package adapters

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"

	xos "github.com/gechr/x/os"
)

// writeCacheEntry needs a retained root, which pinned x/os.AtomicWrite cannot
// accept. Its pathname-based temporary file and rename would reopen a replaced
// namespace. Keep the same publication and durability rules through os.Root.
func writeCacheEntry(root *os.Root, name string, raw []byte) (err error) {
	temporaryName := "." + name + "." + rand.Text()
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create cache temporary file: %w", err)
	}
	published := false
	defer func() {
		if temporary != nil {
			err = errors.Join(err, temporary.Close())
		}
		if !published {
			if removeErr := root.Remove(temporaryName); !errors.Is(removeErr, fs.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if _, err := temporary.Write(raw); err != nil {
		return fmt.Errorf("failed to write cache temporary file: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("failed to chmod cache temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync cache temporary file: %w", err)
	}
	closeErr := temporary.Close()
	temporary = nil
	if closeErr != nil {
		return fmt.Errorf("failed to close cache temporary file: %w", closeErr)
	}
	if err := root.Rename(temporaryName, name); err != nil {
		return fmt.Errorf("failed to rename cache temporary file: %w", err)
	}
	published = true
	if !xos.IsUnix() {
		return nil
	}
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("failed to open cache directory after rename: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("failed to sync cache directory after rename: %w", err)
	}
	return nil
}
