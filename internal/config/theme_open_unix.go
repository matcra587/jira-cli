//go:build unix

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// A custom file can be replaced after stat. Nonblocking open permits descriptor
// validation without waiting on a substituted FIFO. The selected x/os package
// exposes metadata probes, but no regular-file/nonblocking-open primitive.
func openThemeInput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) //nolint:gosec // explicit custom-theme input; the caller validates the descriptor
}
