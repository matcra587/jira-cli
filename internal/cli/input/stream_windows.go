//go:build windows

package input

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// PrepareStream gives selected Windows pipes their own non-inheritable handle.
// Go's pipe Close interrupts pending reads and joins the native operation;
// closing the duplicate leaves the borrowed handle and its modes intact.
// gechr/x/os v0.6.0 has no handle duplication primitive. The caller exclusively
// owns input during preparation, reading and cleanup: os.NewFile's handle-mode
// detection may wait for an outstanding synchronous operation.
func PrepareStream(reader io.Reader, kinds os.FileMode) (io.Reader, func() error, error) {
	file, ok := reader.(*os.File)
	if !ok {
		return reader, nil, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&kinds&os.ModeNamedPipe == 0 {
		return reader, nil, nil
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var duplicate windows.Handle
	var operationErr error
	controlErr := connection.Control(func(handle uintptr) {
		// Windows FILE_TYPE_PIPE also includes sockets. Only a native pipe
		// admits GetNamedPipeInfo; a socket keeps its existing reader owner.
		if probeErr := windows.GetNamedPipeInfo(windows.Handle(handle), nil, nil, nil, nil); probeErr != nil {
			if !errors.Is(probeErr, windows.ERROR_INVALID_FUNCTION) && !errors.Is(probeErr, windows.ERROR_INVALID_HANDLE) {
				operationErr = probeErr
			}
			return
		}
		process := windows.CurrentProcess()
		operationErr = windows.DuplicateHandle(process, windows.Handle(handle), process,
			&duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS)
	})
	if err := errors.Join(controlErr, operationErr); err != nil {
		if duplicate != 0 {
			err = errors.Join(err, windows.CloseHandle(duplicate))
		}
		return nil, nil, err
	}
	if duplicate == 0 {
		return reader, nil, nil
	}
	owned := os.NewFile(uintptr(duplicate), file.Name())
	cleanup := func() error {
		err := owned.Close()
		if errors.Is(err, os.ErrClosed) {
			return nil // cancellation may have closed the owned duplicate
		}
		return err
	}
	return owned, cleanup, nil
}
