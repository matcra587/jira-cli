//go:build unix

package issueedit

import (
	"os"

	"golang.org/x/sys/unix"
)

// The selected x/os has no rooted, regular-file nonblocking opener. Refuse a
// substituted FIFO without waiting; readMember validates the opened descriptor.
func openMember(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
