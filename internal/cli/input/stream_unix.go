//go:build unix

package input

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// PrepareStream gives selected inherited descriptors their own pollable Go
// wrapper. Closing a borrowed file alone cannot interrupt a blocking Unix read.
// The caller owns the duplicate, joins any cancellation callback before cleanup,
// and exclusively owns input throughout preparation, reading and restoration.
// Cleanup restores borrowed flags and never closes the borrowed descriptor.
func PrepareStream(reader io.Reader, kinds os.FileMode) (io.Reader, func() error, error) {
	file, ok := reader.(*os.File)
	if !ok {
		return reader, nil, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&kinds == 0 {
		return reader, nil, nil
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	duplicate, originalFlags := -1, 0
	var operationErr error
	controlErr := connection.Control(func(descriptor uintptr) {
		originalFlags, operationErr = unix.FcntlInt(descriptor, unix.F_GETFL, 0)
		if operationErr != nil {
			return
		}
		duplicate, operationErr = unix.FcntlInt(descriptor, unix.F_DUPFD_CLOEXEC, 0)
		if operationErr == nil {
			operationErr = unix.SetNonblock(duplicate, true)
		}
	})
	if err := errors.Join(controlErr, operationErr); err != nil {
		if duplicate >= 0 {
			err = errors.Join(err, unix.Close(duplicate))
		}
		return nil, nil, err
	}
	pollable := os.NewFile(uintptr(duplicate), file.Name())
	cleanup := func() error {
		var restoreErr error
		if originalFlags&unix.O_NONBLOCK == 0 {
			controlErr := connection.Control(func(descriptor uintptr) {
				restoreErr = unix.SetNonblock(int(descriptor), false)
			})
			restoreErr = errors.Join(controlErr, restoreErr)
		}
		closeErr := pollable.Close()
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil // cancellation may have closed the owned duplicate
		}
		return errors.Join(restoreErr, closeErr)
	}
	if err := pollable.SetReadDeadline(time.Time{}); err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	return pollable, cleanup, nil
}
