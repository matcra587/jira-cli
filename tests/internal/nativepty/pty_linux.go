//go:build linux

package nativepty

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Open returns an owned nonblocking master and a blocking slave. The caller
// closes both. gechr/x/terminal detects terminals but does not allocate PTYs.
func Open() (*os.File, *os.File, error) {
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	master := os.NewFile(uintptr(descriptor), "acceptance-terminal")
	if err := unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0); err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	return master, slave, nil
}
