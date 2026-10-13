//go:build !windows

package issueedit

func directoryRenameLocked(error) bool { return false }
