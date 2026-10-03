//go:build unix

package command

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// preparePipeInput gives inherited blocking descriptors their own pollable Go
// wrapper. Closing os.Stdin alone cannot interrupt a blocking Unix read. The
// duplicate is owned here; borrowed descriptors remain open, and their original
// nonblocking state is restored after the read. Input has one reader per run.
func preparePipeInput(input io.Reader) (io.Reader, func() error, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return input, nil, nil
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
	reader := os.NewFile(uintptr(duplicate), file.Name())
	cleanup := func() error {
		var restoreErr error
		if originalFlags&unix.O_NONBLOCK == 0 {
			controlErr := connection.Control(func(descriptor uintptr) {
				restoreErr = unix.SetNonblock(int(descriptor), false)
			})
			restoreErr = errors.Join(controlErr, restoreErr)
		}
		closeErr := reader.Close()
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil // cancellation may have closed the owned duplicate
		}
		return errors.Join(restoreErr, closeErr)
	}
	if err := reader.SetReadDeadline(time.Time{}); err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	return reader, cleanup, nil
}
