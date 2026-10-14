//go:build !windows

package nativepath

import "path/filepath"

// Abs returns an absolute path without expanding home or environment selectors.
func Abs(path string) (string, error) { return filepath.Abs(path) }

// ResolveExisting follows symlinks in an existing path and retains errors.
func ResolveExisting(path string) (string, error) { return filepath.EvalSymlinks(path) }

// JoinLeaf appends the selected final component to its resolved parent.
func JoinLeaf(parent, path string) string { return filepath.Join(parent, filepath.Base(path)) }
