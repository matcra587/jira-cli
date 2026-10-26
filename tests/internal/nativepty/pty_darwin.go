//go:build darwin

package nativepty

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"unsafe"

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
	// XNU's TIOCPTYGNAME ABI is a bounded, NUL-terminated 128-byte name.
	var name [128]byte
	if (unix.TIOCPTYGNAME>>16)&0x1fff != len(name) {
		return nil, nil, errors.Join(errors.New("native PTY name ABI size differs"), master.Close())
	}
	if err := ptyName(descriptor, uintptr(unsafe.Pointer(&name[0]))); err != nil { //nolint:gosec // G103: fixed native PTY-name buffer; the escape boundary retains its storage.
		return nil, nil, errors.Join(err, master.Close())
	}
	end := bytes.IndexByte(name[:], 0)
	if end <= 0 || !strings.HasPrefix(string(name[:end]), "/dev/") {
		return nil, nil, errors.Join(errors.New("native PTY name is invalid or unbounded"), master.Close())
	}
	if err := unix.IoctlSetInt(descriptor, unix.TIOCPTYGRANT, 0); err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	if err := unix.IoctlSetInt(descriptor, unix.TIOCPTYUNLK, 0); err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, errors.Join(err, master.Close())
	}
	return master, slave, nil
}

// Keep the native output buffer off a movable stack across the opaque syscall.
//
//go:uintptrescapes
func ptyName(descriptor int, address uintptr) error {
	//nolint:staticcheck // SA1019: selected x/sys has no typed PTY-name getter; this raw ABI is test-only and requires native acceptance.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(descriptor), unix.TIOCPTYGNAME, address)
	if errno != 0 {
		return errno
	}
	return nil
}
