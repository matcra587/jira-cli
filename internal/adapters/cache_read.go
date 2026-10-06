package adapters

import (
	"errors"
	"io"
	"os"
)

var errUnsafeCacheEntry = errors.New("cache entry is not a regular cache file")

// cacheReadRoot isolates inspection and opening at the actual filesystem boundary.
// Both production consumers use their identity-checked os.Root; tests can replace
// an entry between inspection and opening without changing global state.
type cacheReadRoot interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (*os.File, error)
}

// readCacheEntry checks the opened descriptor before reading, rather than trusting
// a pathname inspection made before open. Unlike x/os.IsFile (v0.6.0), this policy
// spans inspection, native opening and reading within the retained namespace.
func readCacheEntry(root cacheReadRoot, name string) ([]byte, error) {
	// Native descriptor-relative opens must receive one validated opaque leaf.
	if !validCacheEntryID(name) {
		return nil, errUnsafeCacheEntry
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errUnsafeCacheEntry
	}
	file, err := openCacheEntry(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // Read-only descriptor cleanup does not change inspected bytes.
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, errUnsafeCacheEntry
	}
	return io.ReadAll(file)
}
