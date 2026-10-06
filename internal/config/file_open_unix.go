//go:build unix

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// Nonblocking open permits descriptor validation without waiting on a FIFO,
// including one substituted after a pathname inspection. The selected x/os
// package exposes metadata probes, but no regular-file/nonblocking-open API.
func openFileInput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) //nolint:gosec // selected local input; the caller validates the descriptor
}
