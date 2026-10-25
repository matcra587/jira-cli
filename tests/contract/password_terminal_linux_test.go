//go:build linux

package contract

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"
	"go.uber.org/goleak"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// TestAccessiblePasswordTerminalContext exercises the shared native reader
// without a credential backend. Both normal terminal modes and an already
// echo-disabled terminal must survive cancellation, including native startup.
func TestAccessiblePasswordTerminalContext(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, descriptor := range []string{"go poller", "blocking", "direct nonblocking"} {
		for _, originalEchoOff := range []bool{false, true} {
			for _, tc := range []struct {
				name                       string
				partial, deadline, startup bool
			}{
				{name: "cancel empty"},
				{name: "cancel partial", partial: true},
				{name: "deadline empty", deadline: true},
				{name: "deadline partial", partial: true, deadline: true},
				{name: "cancel during native startup", startup: true},
			} {
				prefix := "echo enabled/"
				if originalEchoOff {
					prefix = "echo disabled/"
				}
				t.Run(descriptor+"/"+prefix+tc.name, func(t *testing.T) {
					master, slave, fd := passwordDescriptorPTY(t, descriptor)
					before, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
					if err != nil {
						t.Fatal(err)
					}
					if originalEchoOff {
						// Native ReadPassword enables canonical input. Starting with
						// it disabled makes actual native startup observable here.
						before.Lflag &^= unix.ECHO | unix.ICANON
						if err := unix.IoctlSetTermios(int(fd), unix.TCSETS, before); err != nil {
							t.Fatal(err)
						}
					}
					beforeFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(t.Context())
					if tc.deadline {
						cancel()
						ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					}
					defer cancel()
					var value string
					field := prompt.WithPasswordContext(ctx, huh.NewInput().Title("Synthetic password").EchoMode(huh.EchoModePassword), huh.NewPointerAccessor(&value))
					output := io.Discard
					if tc.startup {
						output = passwordCancelWriter{cancel: cancel}
					}
					finished := make(chan error, 1)
					go func() { finished <- field.RunAccessible(output, slave) }()
					waited := false
					defer func() {
						if !waited {
							cancel()
							_ = master.Close()
							_ = slave.Close()
							if err := <-finished; err != nil {
								t.Logf("native password cleanup: %v", err)
							}
						}
					}()
					if !tc.startup {
						ready := time.NewTimer(2 * time.Second)
						defer ready.Stop()
						poll := time.NewTicker(time.Millisecond)
						defer poll.Stop()
						for {
							state, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
							if err != nil {
								t.Fatal(err)
							}
							if state.Lflag&unix.ECHO == 0 && state.Lflag&unix.ICANON != 0 {
								break
							}
							select {
							case err := <-finished:
								waited = true
								t.Fatalf("password read ended before masked input: %v", err)
							case <-ready.C:
								t.Fatal("native password masking was not observed")
							case <-poll.C:
							}
						}
						if tc.partial {
							if _, err := io.WriteString(master, "synthetic-password-prefix"); err != nil {
								t.Fatal(err)
							}
						}
						if !tc.deadline {
							cancel()
						}
					}
					var outcome error
					select {
					case outcome = <-finished:
						waited = true
					case <-time.After(2 * time.Second):
						t.Fatal("active password read required forced descriptor cleanup")
					}
					want := context.Canceled
					if tc.deadline {
						want = context.DeadlineExceeded
					}
					if !errors.Is(outcome, want) || !errors.Is(field.Err(), want) || value != "" {
						t.Fatal("active password context accepted input or lost its error")
					}
					after, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
					if err != nil || *before != *after {
						t.Fatal("password context damaged original terminal modes")
					}
					afterFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
					if err != nil || beforeFlags != afterFlags {
						t.Fatal("password context damaged original descriptor flags")
					}
					assertPasswordFileDeadline(t, slave)
					// The descriptor remains usable after the native reader and its
					// cancellation controller have returned and joined.
					if _, err := io.WriteString(slave, "usable"); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestPasswordTerminalRestorationFailureRetainsCancellation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	master, slave := editorPTY(t)
	fd := slave.Fd()
	connection, err := slave.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var restoreCause error
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var value string
	field := prompt.WithPasswordContext(ctx, huh.NewInput().Title("Synthetic password").EchoMode(huh.EchoModePassword).Validate(func(string) error {
		// Native masking has already restored its own modes at validation.
		// Closing this test-owned descriptor injects a failure into the
		// adapter's final restoration without mocking its system calls.
		cancel()
		closeErr := slave.Close()
		restoreCause = connection.Control(func(uintptr) {
			t.Error("closed password fixture unexpectedly retained a valid descriptor")
		})
		return closeErr
	}), huh.NewPointerAccessor(&value))
	finished := make(chan error, 1)
	go func() { finished <- field.RunAccessible(io.Discard, slave) }()
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = master.Close()
			_ = slave.Close()
			if err := <-finished; err != nil {
				t.Logf("native password cleanup: %v", err)
			}
		}
	}()
	ready := time.NewTimer(2 * time.Second)
	defer ready.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		state, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if state.Lflag&unix.ECHO == 0 {
			break
		}
		select {
		case <-ready.C:
			t.Fatal("native password masking was not observed")
		case <-poll.C:
		}
	}
	if _, err := io.WriteString(master, "synthetic-password\r"); err != nil {
		t.Fatal(err)
	}
	var outcome error
	select {
	case outcome = <-finished:
		waited = true
	case <-time.After(2 * time.Second):
		t.Fatal("failed password restoration stranded the reader")
	}
	var restoreErr *prompt.TerminalRestoreError
	if restoreCause == nil || !errors.Is(outcome, context.Canceled) || !errors.Is(outcome, restoreCause) || !errors.As(outcome, &restoreErr) || !errors.As(field.Err(), &restoreErr) {
		t.Fatal("native password restoration failure lost its typed cause or cancellation")
	}
}

func assertPasswordFileDeadline(t *testing.T, file *os.File) {
	t.Helper()
	if err := file.SetReadDeadline(time.Unix(0, 0)); err != nil {
		t.Fatalf("password input disabled borrowed Go deadlines: %v", err)
	}
	var data [1]byte
	if _, err := file.Read(data[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("borrowed Go read did not honor its deadline: %v", err)
	}
	if err := file.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestAccessiblePasswordDescriptorOwnership(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	count := func() int {
		files, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(files)
	}
	before := count()
	for _, descriptor := range []string{"go poller", "blocking", "direct nonblocking"} {
		for range 8 {
			t.Run(descriptor, func(t *testing.T) {
				_, slave, _ := passwordDescriptorPTY(t, descriptor)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				field := prompt.WithPasswordContext(ctx, huh.NewInput().Title("Synthetic password").EchoMode(huh.EchoModePassword), &huh.EmbeddedAccessor[string]{})
				if err := field.RunAccessible(passwordCancelWriter{cancel: cancel}, slave); !errors.Is(err, context.Canceled) {
					t.Fatalf("password lifecycle lost cancellation: %v", err)
				}
			})
		}
	}
	if after := count(); after != before {
		t.Fatalf("password lifecycle leaked descriptors: before=%d after=%d", before, after)
	}
}

func TestPasswordBorrowedReadDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, descriptor := range []string{"go poller", "direct nonblocking"} {
		t.Run(descriptor, func(t *testing.T) {
			master, slave, _ := passwordDescriptorPTY(t, descriptor)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			field := prompt.WithPasswordContext(ctx, huh.NewInput().Title("Synthetic password").EchoMode(huh.EchoModePassword), &huh.EmbeddedAccessor[string]{})
			if err := field.RunAccessible(passwordCancelWriter{cancel: cancel}, slave); !errors.Is(err, context.Canceled) {
				t.Fatalf("password lifecycle lost cancellation: %v", err)
			}
			finished := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				var data [1]byte
				_, err := slave.Read(data[:])
				finished <- err
			}()
			waited := false
			defer func() {
				if !waited {
					_ = master.Close()
					_ = slave.Close()
					if err := <-finished; err != nil {
						t.Logf("owned task cleanup: %v", err)
					}
				}
			}()
			<-started
			// Set a future deadline after starting a read on the empty terminal.
			// An expired deadline checked before Read cannot expose a blocking
			// syscall that no longer participates in the borrowed Go poller.
			if err := slave.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				waited = true
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("borrowed read lost its active deadline: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("borrowed read deadline required forced descriptor cleanup")
			}
		})
	}
}

type passwordCancelWriter struct{ cancel context.CancelFunc }

func (w passwordCancelWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Synthetic password") {
		w.cancel()
	}
	return len(p), nil
}

func TestAccessiblePasswordDescriptorValidation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, descriptor := range []string{"go poller", "blocking", "direct nonblocking"} {
		t.Run(descriptor, func(t *testing.T) {
			master, slave, fd := passwordDescriptorPTY(t, descriptor)
			before, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			beforeFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var value string
			validated := make(chan string, 2)
			field := prompt.WithPasswordContext(ctx, huh.NewInput().Title("Synthetic password").EchoMode(huh.EchoModePassword).Validate(func(candidate string) error {
				validated <- candidate
				if candidate != "synthetic-password" {
					return errors.New("fixture password rejected")
				}
				return nil
			}), huh.NewPointerAccessor(&value))
			finished := make(chan error, 1)
			go func() { finished <- field.RunAccessible(io.Discard, slave) }()
			waited := false
			defer func() {
				if !waited {
					cancel()
					_ = master.Close()
					_ = slave.Close()
					if err := <-finished; err != nil {
						t.Logf("owned task cleanup: %v", err)
					}
				}
			}()
			ready := time.NewTimer(2 * time.Second)
			defer ready.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				state, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
				if err != nil {
					t.Fatal(err)
				}
				if state.Lflag&unix.ECHO == 0 && state.Lflag&unix.ICANON != 0 {
					break
				}
				select {
				case err := <-finished:
					waited = true
					t.Fatalf("password validation ended before masked input: %v", err)
				case <-ready.C:
					t.Fatal("native password masking was not observed")
				case <-poll.C:
				}
			}
			// Both lines reach the native reader after masking. A rejected first
			// value must re-enter native validation without changing ownership.
			if _, err := io.WriteString(master, "invalid\rsynthetic-password\r"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				waited = true
				if err != nil || field.Err() != nil || value != "synthetic-password" {
					t.Fatalf("native password validation failed: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("native password validation stranded the reader")
			}
			if len(validated) != 2 || <-validated != "invalid" || <-validated != "synthetic-password" {
				t.Fatal("native password validation lost a rejection or accepted different input")
			}
			after, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
			if err != nil || *after != *before {
				t.Fatal("successful password validation changed terminal modes")
			}
			afterFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil || afterFlags != beforeFlags {
				t.Fatal("successful password validation changed borrowed flags")
			}
			assertPasswordFileDeadline(t, slave)
		})
	}
}

// passwordDescriptorPTY captures the borrowed descriptor without File.Fd,
// which changes flags on a file registered with Go's runtime poller.
func passwordDescriptorPTY(t *testing.T, mode string) (*os.File, *os.File, uintptr) {
	t.Helper()
	master, slave := editorPTY(t)
	raw, err := slave.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fd uintptr
	if err := raw.Control(func(candidate uintptr) { fd = candidate }); err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil || flags&unix.O_NONBLOCK == 0 {
		t.Fatal("PTY fixture is not registered as a nonblocking Go file")
	}
	switch mode {
	case "blocking":
		if err := unix.SetNonblock(int(fd), false); err != nil {
			t.Fatal(err)
		}
	case "direct nonblocking":
		duplicate, err := unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		next := os.NewFile(uintptr(duplicate), "nonblocking-password-terminal")
		t.Cleanup(func() { _ = next.Close() })
		if err := slave.Close(); err != nil {
			t.Fatal(err)
		}
		slave, fd = next, uintptr(duplicate)
	}
	return master, slave, fd
}
