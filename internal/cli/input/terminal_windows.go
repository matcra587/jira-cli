//go:build windows

package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

// RunTerminalContext interrupts native input-console reads on the reading thread.
// gechr/x/os v0.6.0 has no native synchronous-I/O cancellation primitive. The
// caller exclusively owns this console and runs only its accessible field here;
// result output and unrelated I/O must happen after this function returns.
// Operation and cleanup errors remain separate so callers retain failed mode
// restoration even when cancellation successfully interrupted the read.
func RunTerminalContext(ctx context.Context, reader io.Reader, run func(io.Reader) error) (err, cleanupErr error) {
	if err := ctx.Err(); err != nil {
		return err, nil
	}
	file, ok := reader.(*os.File)
	if !ok {
		return run(reader), nil
	}
	info, err := file.Stat()
	if err != nil {
		return err, nil
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return run(reader), nil
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return err, nil
	}
	var handle windows.Handle
	var before uint32
	var admissionErr error
	controlErr := connection.Control(func(descriptor uintptr) {
		handle = windows.Handle(descriptor)
		admissionErr = windows.GetConsoleMode(handle, &before)
		if admissionErr == nil {
			// GetConsoleMode also accepts output buffers. Only input buffers
			// admit this query; output and NUL retain their ordinary readers.
			var queued uint32
			admissionErr = windows.GetNumberOfConsoleInputEvents(handle, &queued)
		}
	})
	if controlErr != nil {
		return controlErr, nil
	}
	if admissionErr != nil {
		if errors.Is(admissionErr, windows.ERROR_INVALID_HANDLE) {
			return run(reader), nil
		}
		return fmt.Errorf("inspect native console input: %w", admissionErr), nil
	}
	interrupt := windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")
	if err := interrupt.Find(); err != nil {
		return fmt.Errorf("prepare native console cancellation: %w", err), nil
	}
	// A canceled borrowed or duplicated console read can stall a later read
	// through the caller's handle. Open an independent handle to the admitted
	// process console, then retire that read lifetime after joining. A process
	// has one attached console input buffer; ordinary readers never reach here.
	owned, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open owned native console input: %w", err), nil
	}
	ownedHandle := windows.Handle(owned.Fd())
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		return fmt.Errorf("prepare native console thread: %w", err), owned.Close()
	}
	finished, joined := make(chan struct{}), make(chan error, 1)
	go func() { joined <- interruptConsoleContext(ctx, thread, interrupt, finished) }()
	defer func() {
		// A panic must also stop cancellation before this OS thread can run
		// unrelated work. Huh may have attempted its own mode restoration;
		// restore again only after no cancellation call can still race it.
		close(finished)
		cancelErr := <-joined
		inputErr := owned.Close()
		if inputErr != nil {
			inputErr = fmt.Errorf("release owned native console input: %w", inputErr)
		}
		// The callback may have closed the borrowed file. Its numeric slot can
		// already belong to another object, so retain the original FD lifetime.
		var modeErr error
		modeControlErr := connection.Control(func(descriptor uintptr) {
			modeErr = windows.SetConsoleMode(windows.Handle(descriptor), before)
		})
		modeErr = errors.Join(modeControlErr, modeErr)
		if modeErr != nil {
			modeErr = fmt.Errorf("restore native console input modes: %w", modeErr)
		}
		closeErr := windows.CloseHandle(thread)
		if closeErr != nil {
			closeErr = fmt.Errorf("release native console thread handle: %w", closeErr)
		}
		cleanupErr = errors.Join(cancelErr, inputErr, modeErr, closeErr)
		err = consoleReadOutcome(ctx, err)
		if err == nil {
			err = ctx.Err()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err, nil
	}
	prepared := consoleReader{
		read: func(buffer []byte) (int, error) {
			n, err := owned.Read(buffer)
			if err == nil && ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return n, consoleReadOutcome(ctx, err)
		},
		descriptor: uintptr(ownedHandle),
	}
	return run(prepared), nil
}

// Cancellation may precede kernel entry or validation may start another read.
// Retry only while this native field still owns its pinned thread, and join
// before releasing it. THREAD_TERMINATE is the API access right; no thread is
// terminated. The monitor never reads or supplies a successful answer.
func interruptConsoleContext(ctx context.Context, thread windows.Handle, interrupt *windows.LazyProc, finished <-chan struct{}) error {
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-finished:
			return nil
		default:
		}
		result, _, err := interrupt.Call(uintptr(thread))
		if result == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return fmt.Errorf("interrupt native console input: %w", err)
		}
		select {
		case <-finished:
			return nil
		case <-ticker.C:
		}
	}
}

// ReadConsole interruption can appear as EOF rather than a native abort.
// Normalize only a single cause within this expired native console scope.
// A joined error may retain an independent failure and must never be erased.
func consoleReadOutcome(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return err
	}
	for cause := err; cause != nil; {
		if cause == windows.ERROR_OPERATION_ABORTED || cause == io.EOF { //nolint:errorlint // Inspect single causes explicitly; errors.Is would erase independent joined failures.
			return ctx.Err()
		}
		wrapped, ok := cause.(interface{ Unwrap() error })
		if !ok {
			break
		}
		cause = wrapped.Unwrap()
	}
	return err
}

type consoleReader struct {
	read       func([]byte) (int, error)
	descriptor uintptr
}

func (r consoleReader) Read(buffer []byte) (int, error) { return r.read(buffer) }

// Huh's password reader uses its native Fd path and owns an internal duplicate.
func (r consoleReader) Fd() uintptr { return r.descriptor }
