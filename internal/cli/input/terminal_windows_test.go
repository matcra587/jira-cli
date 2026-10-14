//go:build windows

package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsConsoleReadOutcomePreservesIndependentFailures(t *testing.T) {
	live := t.Context()
	ctx, cancel := context.WithCancel(live)
	cancel()
	failure := errors.New("independent console failure")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "native abort", err: windows.ERROR_OPERATION_ABORTED},
		{name: "EOF", err: io.EOF},
		{name: "wrapped EOF", err: &os.PathError{Op: "read", Path: "owned", Err: io.EOF}},
		{name: "wrapped abort", err: fmt.Errorf("owned read: %w", windows.ERROR_OPERATION_ABORTED)},
		{name: "joined abort", err: errors.Join(windows.ERROR_OPERATION_ABORTED, failure)},
		{name: "joined EOF", err: fmt.Errorf("owned read: %w", errors.Join(io.EOF, failure))},
		{name: "genuine failure", err: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := consoleReadOutcome(live, tc.err); got != tc.err { //nolint:errorlint // Assert exact identity, including independent joined errors.
				t.Fatalf("live context changed native outcome: %v", got)
			}
			got := consoleReadOutcome(ctx, tc.err)
			if errors.Is(tc.err, failure) {
				if got != tc.err || !errors.Is(got, failure) { //nolint:errorlint // A joined failure must survive without replacement.
					t.Fatalf("cancellation erased independent failure: %v", got)
				}
			} else if !errors.Is(got, context.Canceled) {
				t.Fatalf("expected native interruption lost ordinary cancellation: %v", got)
			}
		})
	}
}

func TestWindowsTerminalContextKeepsOrdinaryReaders(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	null, err := os.Open("NUL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	for _, reader := range []io.Reader{strings.NewReader("ordinary"), file, null} {
		entered := false
		failure := errors.New("callback failure")
		err, cleanupErr := RunTerminalContext(t.Context(), reader, func(prepared io.Reader) error {
			entered = true
			if prepared != reader {
				t.Fatal("non-console reader acquired native console owner")
			}
			return failure
		})
		if !entered || !errors.Is(err, failure) || cleanupErr != nil {
			t.Fatalf("ordinary reader outcome changed: entered=%v error=%v cleanup=%v", entered, err, cleanupErr)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entered := false
	err, cleanupErr := RunTerminalContext(t.Context(), file, func(io.Reader) error {
		entered = true
		return nil
	})
	if entered || (!errors.Is(err, os.ErrClosed) && !errors.Is(err, windows.ERROR_INVALID_HANDLE)) || cleanupErr != nil {
		t.Fatalf("closed native admission was hidden: entered=%v error=%v cleanup=%v", entered, err, cleanupErr)
	}
}

func TestWindowsConsoleCancellationRetainsNativeThreadFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	interrupt := windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")
	if err := interrupt.Find(); err != nil {
		t.Fatal(err)
	}
	err := interruptConsoleContext(ctx, windows.InvalidHandle, interrupt, make(chan struct{}))
	if !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("native cancellation error was swallowed: %v", err)
	}
}
