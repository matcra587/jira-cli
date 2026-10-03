//go:build unix

package attachmentfiles

import (
	"os"

	"golang.org/x/sys/unix"
)

// A stat-before-open check alone cannot prevent a path becoming a FIFO.
// O_NONBLOCK lets Open obtain a descriptor and reject its actual type.
// The pinned x/os file probes expose metadata, not a nonblocking open.
func openFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) //nolint:gosec // explicit caller-named upload input
}
