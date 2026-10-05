package prompt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

// InputCleanupError preserves failure to restore or release an owned input
// resource, including when the read was canceled successfully.
type InputCleanupError struct {
	Err error
}

func (e *InputCleanupError) Error() string { return fmt.Sprintf("input cleanup failed: %v", e.Err) }

// Unwrap preserves the operating system's cleanup failure.
func (e *InputCleanupError) Unwrap() error { return e.Err }

// InputReadError preserves a native accessible read failure that Huh discards.
type InputReadError struct {
	Err error
}

func (e *InputReadError) Error() string { return fmt.Sprintf("input read failed: %v", e.Err) }

// Unwrap preserves the original read failure.
func (e *InputReadError) Unwrap() error { return e.Err }

// IsInputFailure reports read or cleanup failures that must survive a caller's
// ordinary quiet-abort policy, even when joined with cancellation.
func IsInputFailure(err error) bool {
	var cleanupErr *InputCleanupError
	var readErr *InputReadError
	var restoreErr *TerminalRestoreError
	return errors.As(err, &cleanupErr) || errors.As(err, &readErr) || errors.As(err, &restoreErr)
}

func runAccessibleField(ctx context.Context, field huh.Field, w io.Writer, r io.Reader) (err error) {
	reader, cleanup, err := input.PrepareStream(r, os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket)
	if err != nil {
		return &InputReadError{Err: err}
	}
	if cleanup != nil {
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				err = errors.Join(err, &InputCleanupError{Err: cleanupErr})
			}
		}()
		// Only the prepared duplicate is owned here. Join its cancellation
		// callback before cleanup restores flags or closes remaining resources.
		closed := make(chan struct{})
		var closeErr error
		stop := context.AfterFunc(ctx, func() {
			defer close(closed)
			closeErr = reader.(io.Closer).Close()
		})
		defer func() {
			if !stop() {
				<-closed
				if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
					err = errors.Join(err, &InputCleanupError{Err: closeErr})
				}
			}
		}()
	}
	line := &accessibleLineReader{reader: reader}
	err = field.RunAccessible(w, line)
	if readErr := line.err; readErr != nil {
		if ctx.Err() == nil || !errors.Is(readErr, os.ErrClosed) {
			err = errors.Join(err, &InputReadError{Err: readErr})
		}
	}
	return errors.Join(err, ctx.Err())
}

var errAccessibleLineTooLong = errors.New("input line exceeds accessible form limit")

// Huh makes a fresh Scanner for each field and hides its error. Reading one
// byte at a time preserves queued later answers and detects incomplete EOF.
// The explicit limit reports overflow before Scanner can silently use a default.
type accessibleLineReader struct {
	reader   io.Reader
	err      error
	bytes    int
	complete bool
	finalEOF bool
}

func (r *accessibleLineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.finalEOF {
		r.err = io.EOF
		return 0, r.err
	}
	if r.complete {
		r.bytes, r.complete = 0, false
	}
	if r.bytes >= bufio.MaxScanTokenSize-1 {
		r.err = errAccessibleLineTooLong
		return 0, r.err
	}
	n, err := r.reader.Read(p[:1])
	r.bytes += n
	r.complete = n != 0 && p[n-1] == '\n'
	if errors.Is(err, io.EOF) && r.complete {
		// Defer EOF until another read. Scanner otherwise remembers EOF and
		// can silently return a rejected answer when validation retries.
		r.finalEOF = true
		return n, nil
	}
	if err != nil {
		r.err = err
	}
	return n, err
}
