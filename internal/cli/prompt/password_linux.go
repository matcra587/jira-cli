package prompt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/huh/v2"
	"golang.org/x/sys/unix"
)

// Huh v2.0.3 delegates masking to x/term v0.2.2, whose Linux reader blocks in
// unix.Read without a context. x/terminal v0.6.0 has no password reader. Keep
// Huh's native input and wake its kernel read only after cancellation; neither
// closing a duplicate descriptor nor a Go reader deadline interrupts that read.
func runAccessiblePassword(ctx context.Context, input *huh.Input, w io.Writer, r io.Reader) (err error) {
	r, cleanup, err := preparePasswordTerminal(r)
	if err != nil {
		return fmt.Errorf("prepare password terminal: %w", err)
	}
	if cleanup != nil {
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				err = errors.Join(err, &TerminalRestoreError{Err: cleanupErr})
			}
		}()
	}
	terminal, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return input.RunAccessible(w, r)
	}
	fd := terminal.Fd()
	before, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
	if err != nil {
		return fmt.Errorf("read password terminal modes: %w", err)
	}
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		return fmt.Errorf("read password descriptor flags: %w", err)
	}
	// Native x/term reads directly with unix.Read and cannot wait for readiness
	// on an inherited nonblocking descriptor. The form exclusively owns input.
	if err := unix.SetNonblock(int(fd), false); err != nil {
		return fmt.Errorf("prepare blocking password input: %w", err)
	}
	finished := make(chan struct{})
	joined := make(chan error, 1)
	go func() {
		joined <- wakeCanceledPassword(ctx, int(fd), finished)
	}()
	err = input.RunAccessible(w, r)
	close(finished)
	wakeErr := <-joined
	// Join before restoring: cancellation can race both native startup and its
	// restoration, including a terminal whose original echo was already off.
	modeErr := unix.IoctlSetTermios(int(fd), unix.TCSETS, before)
	if modeErr != nil {
		modeErr = fmt.Errorf("restore password terminal modes: %w", modeErr)
	}
	_, flagErr := unix.FcntlInt(fd, unix.F_SETFL, flags)
	if flagErr != nil {
		flagErr = fmt.Errorf("restore password descriptor flags: %w", flagErr)
	}
	restoreErr := errors.Join(modeErr, flagErr)
	if restoreErr != nil {
		restoreErr = &TerminalRestoreError{Err: restoreErr}
	}
	return errors.Join(err, wakeErr, restoreErr)
}

// preparePasswordTerminal avoids File.Fd's permanent effects on the borrowed
// Go file's poller. Native masking uses an owned duplicate; its status flags
// still share the original open file description and must be restored.
func preparePasswordTerminal(reader io.Reader) (io.Reader, func() error, error) {
	file, ok := reader.(*os.File)
	if !ok {
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
	})
	if err := errors.Join(controlErr, operationErr); err != nil {
		if duplicate >= 0 {
			err = errors.Join(err, unix.Close(duplicate))
		}
		return nil, nil, err
	}
	owned := os.NewFile(uintptr(duplicate), file.Name())
	cleanup := func() error {
		var restoreErr error
		controlErr := connection.Control(func(descriptor uintptr) {
			_, restoreErr = unix.FcntlInt(descriptor, unix.F_SETFL, originalFlags)
		})
		return errors.Join(controlErr, restoreErr, owned.Close())
	}
	return owned, cleanup, nil
}

func wakeCanceledPassword(ctx context.Context, fd int, finished <-chan struct{}) error {
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
	}
	// Cancellation may precede native masking or validation may start another
	// read. Keep checking until the caller has left Huh's accessible reader.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-finished:
			return nil
		case <-ticker.C:
		}
		state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			return fmt.Errorf("read canceled password terminal modes: %w", err)
		}
		if state.Lflag&unix.ECHO != 0 || state.Lflag&unix.ICANON == 0 {
			continue
		}
		if err := unix.SetNonblock(fd, true); err != nil {
			return fmt.Errorf("interrupt password descriptor: %w", err)
		}
		// Changing canonical mode wakes the blocked read. Nonblocking mode then
		// makes an empty read return EAGAIN instead of stranding the reader.
		state.Lflag &^= unix.ICANON
		state.Cc[unix.VMIN] = 1
		state.Cc[unix.VTIME] = 0
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, state); err != nil {
			return fmt.Errorf("interrupt password terminal read: %w", err)
		}
	}
}
