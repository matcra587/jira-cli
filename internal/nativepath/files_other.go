//go:build !windows

package nativepath

import (
	"os"
	"path/filepath"
)

// FilePath returns the public path of a file selected inside a directory.
func FilePath(directory, name string) string { return filepath.Join(directory, name) }

// CreateFile opens a rooted file exclusively, or truncates it when requested.
func CreateFile(root *os.Root, name string, overwrite bool, mode os.FileMode) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	return root.OpenFile(name, flags, mode)
}

// Stat returns rooted metadata, following links within the root.
func Stat(root *os.Root, name string) (os.FileInfo, error) { return root.Stat(name) }

// Lstat returns rooted metadata without following the final link.
func Lstat(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }

// Remove removes one rooted file entry.
func Remove(root *os.Root, name string) error { return root.Remove(name) }

// Rename replaces a rooted entry with another entry from the same root.
func Rename(root *os.Root, oldname, newname string) error { return root.Rename(oldname, newname) }
