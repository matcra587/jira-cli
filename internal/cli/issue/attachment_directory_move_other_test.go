//go:build !windows

package issue

func downloadDirectoryRenameLocked(error) bool { return false }
