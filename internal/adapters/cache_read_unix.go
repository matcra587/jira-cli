//go:build unix

package adapters

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openCacheEntry refuses final symlinks and opens FIFOs without waiting for a
// writer. Go 1.27.1 Root.OpenFile follows in-root symlinks even when its caller
// supplies O_NOFOLLOW, so the leaf open must use native openat. The directory
// comes from the retained root, never from its replaceable Name pathname.
func openCacheEntry(root cacheReadRoot, name string) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close() //nolint:errcheck // Read-only directory cleanup does not change the opened entry.
	conn, err := dir.SyscallConn()
	if err != nil {
		return nil, err
	}
	var opened int
	var openErr error
	if err := conn.Control(func(fd uintptr) {
		opened, openErr = unix.Openat(int(fd), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}); err != nil {
		return nil, err
	}
	if openErr != nil {
		if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.EMLINK) {
			openErr = errors.Join(openErr, errUnsafeCacheEntry)
		} else if info, inspectionErr := root.Lstat(name); inspectionErr == nil && !info.Mode().IsRegular() {
			// Other Unix kernels can use different no-follow errors (EFTYPE on
			// NetBSD, EINVAL on DragonFly). A fresh nonregular observation only
			// classifies refusal; it never authorizes an open or loses its cause.
			openErr = errors.Join(openErr, errUnsafeCacheEntry)
		}
		return nil, &os.PathError{Op: "openat", Path: name, Err: openErr}
	}
	// NewFile owns the newly opened descriptor; the borrowed directory descriptor
	// stays with dir. A valid native open always yields a usable nonnegative fd.
	return os.NewFile(uintptr(opened), name), nil
}
