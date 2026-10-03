package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/core"
)

var (
	_ core.ManagedCache = (*DiskCache)(nil)
	_ core.ManagedCache = (*ScopedCache)(nil)
)

// Entries inspects only hashed entry names in this disk namespace. Symlinks
// and nonregular entries are marked malformed without following them. A root
// descriptor keeps reads within the opened directory during path replacement.
func (c *DiskCache) Entries(ctx context.Context) ([]core.CacheEntry, error) {
	result := []core.CacheEntry{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := c.openRoot()
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer root.Close() //nolint:errcheck // Read-only descriptor cleanup does not change inspected entries.
	dir, err := root.Open(".")
	if err != nil {
		return result, err
	}
	defer dir.Close() //nolint:errcheck // Read-only directory cleanup does not change inspected entries.
	files, err := dir.ReadDir(-1)
	if err != nil {
		return result, err
	}
	slices.SortFunc(files, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !validCacheEntryID(file.Name()) {
			continue
		}
		entry := core.CacheEntry{ID: file.Name(), Malformed: true}
		info, err := root.Lstat(file.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue // a concurrent clear removed the entry
		}
		if err != nil {
			return result, err
		}
		if info.Mode().IsRegular() {
			raw, err := root.ReadFile(file.Name())
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return result, err
			}
			var stored diskEntry
			if json.Unmarshal(raw, &stored) == nil && stored.Key != "" &&
				filepath.Base(c.path(stored.Key)) == file.Name() && !stored.Written.IsZero() {
				entry.Key, entry.Written, entry.SizeBytes = stored.Key, stored.Written, int64(len(stored.Value))
				entry.Malformed = false
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

// DeleteEntry removes one validated opaque filename from this namespace.
// It never traverses a caller-supplied path or recursively removes directories.
// A symlink is removed as a link, preserving its target.
func (c *DiskCache) DeleteEntry(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validCacheEntryID(id) {
		return false, fmt.Errorf("invalid cache entry identity")
	}
	root, err := c.openRoot()
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close() //nolint:errcheck // Descriptor cleanup cannot reverse an acknowledged removal.
	info, err := root.Lstat(id)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("cache entry is a directory; refusing to remove it")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := root.Remove(id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func validCacheEntryID(id string) bool {
	const suffix = ".json"
	if len(id) != sha256.Size*2+len(suffix) || !strings.HasSuffix(id, suffix) {
		return false
	}
	stem := strings.TrimSuffix(id, suffix)
	return stem == strings.ToLower(stem) && xstrings.IsHex(stem)
}

// openRoot refuses a symlink at the namespace leaf. Comparing identities after
// open also refuses a directory replacement between inspection and open.
func (c *DiskCache) openRoot() (*os.Root, error) {
	info, err := os.Lstat(c.dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cache namespace is not a directory")
	}
	root, err := os.OpenRoot(c.dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil {
		_ = root.Close() //nolint:errcheck // Preserve the original inspection failure during cleanup.
		return nil, err
	}
	if !os.SameFile(info, opened) {
		_ = root.Close() //nolint:errcheck // Preserve the identity failure during cleanup.
		return nil, fmt.Errorf("cache namespace changed while opening")
	}
	return root, nil
}
