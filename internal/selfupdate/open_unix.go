//go:build unix

package selfupdate

import (
	"os"

	"golang.org/x/sys/unix"
)

// Match the attachment owner: reject a substituted FIFO without blocking.
// x/os in 0.6.0 does not provide a nonblocking descriptor open.
func executableReadFlags() int { return os.O_RDONLY | unix.O_NONBLOCK }
