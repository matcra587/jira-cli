//go:build linux

package contract

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/huh/v2"
	"go.uber.org/goleak"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// TestAccessibleInputTerminalContext exercises real terminal input independently
// of keyring services, including borrowed nonblocking and echo-disabled states.
func TestAccessibleInputTerminalContext(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, echoOff := range []bool{false, true} {
		for _, nonblocking := range []bool{false, true} {
			for _, kind := range []string{"text", "confirmation"} {
				for _, tc := range []struct {
					name                       string
					partial, deadline, startup bool
				}{
					{name: "cancel empty"},
					{name: "cancel partial", partial: true},
					{name: "deadline empty", deadline: true},
					{name: "deadline partial", partial: true, deadline: true},
					{name: "cancel during startup", startup: true},
				} {
					prefix := "echo on/blocking/"
					if echoOff {
						prefix = strings.ReplaceAll(prefix, "echo on", "echo off")
					}
					if nonblocking {
						prefix = strings.ReplaceAll(prefix, "blocking", "nonblocking")
					}
					t.Run(prefix+kind+"/"+tc.name, func(t *testing.T) {
						master, slave := editorPTY(t)
						fd := slave.Fd()
						before, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
						if err != nil {
							t.Fatal(err)
						}
						if echoOff {
							before.Lflag &^= unix.ECHO
							if err := unix.IoctlSetTermios(int(fd), unix.TCSETS, before); err != nil {
								t.Fatal(err)
							}
						}
						if err := unix.SetNonblock(int(fd), nonblocking); err != nil {
							t.Fatal(err)
						}
						beforeFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
						if err != nil {
							t.Fatal(err)
						}
						ctx, cancel := context.WithCancel(t.Context())
						if tc.deadline {
							cancel()
							ctx, cancel = context.WithTimeout(t.Context(), 250*time.Millisecond)
						}
						defer cancel()
						value, later := "default", "unchanged"
						var answer bool
						var field huh.Field = huh.NewInput().Title("Synthetic input").Value(&value)
						if kind == "confirmation" {
							field = huh.NewConfirm().Title("Synthetic input").Value(&answer)
						}
						ready := &inputReadyWriter{ready: make(chan struct{})}
						if tc.startup {
							ready.cancel = cancel
						}
						form := prompt.NewForm(field, huh.NewInput().Title("Later field").Value(&later)).
							WithAccessible(true).WithInput(slave).WithOutput(ready)
						finished := make(chan error, 1)
						go func() { finished <- prompt.RunForm(ctx, form) }()
						waited := false
						defer func() {
							if !waited {
								cancel()
								_ = master.Close()
								_ = slave.Close()
								if err := <-finished; err != nil {
									t.Logf("native input cleanup: %v", err)
								}
							}
						}()
						select {
						case <-ready.ready:
						case <-time.After(2 * time.Second):
							t.Fatal("native input did not reach its prompt")
						}
						if tc.partial {
							if _, err := io.WriteString(master, "yes"); err != nil {
								t.Fatal(err)
							}
						}
						if !tc.deadline {
							cancel()
						}
						var outcome error
						select {
						case outcome = <-finished:
							waited = true
						case <-time.After(2 * time.Second):
							t.Fatal("active input required forced descriptor cleanup")
						}
						want := context.Canceled
						if tc.deadline {
							want = context.DeadlineExceeded
						}
						if !errors.Is(outcome, want) || later != "unchanged" {
							t.Fatalf("native input lost cancellation or advanced fields: %v", outcome)
						}
						after, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
						if err != nil || *before != *after {
							t.Fatal("input cancellation damaged borrowed terminal modes")
						}
						afterFlags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
						if err != nil || beforeFlags != afterFlags {
							t.Fatal("input cancellation damaged borrowed descriptor flags")
						}
						if _, err := io.WriteString(slave, "usable"); err != nil {
							t.Fatal("input cancellation closed the borrowed terminal")
						}
					})
				}
			}
		}
	}
}

func TestAccessibleInputCleanupFailureRetainsCancellation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	master, slave := editorPTY(t)
	if err := unix.SetNonblock(int(slave.Fd()), false); err != nil {
		t.Fatal(err)
	}
	connection, err := slave.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var value string
	var injected error
	form := prompt.NewForm(huh.NewInput().Title("Synthetic input").Value(&value).Validate(func(string) error {
		// Native validation runs after the completed read. Closing this
		// test-owned descriptor injects failure into final flag restoration.
		cancel()
		if err := slave.Close(); err != nil {
			t.Fatal(err)
		}
		injected = connection.Control(func(uintptr) { t.Error("closed descriptor remained usable") })
		return nil
	})).WithAccessible(true).WithInput(slave).WithOutput(io.Discard)
	if _, err := io.WriteString(master, "answer\r"); err != nil {
		t.Fatal(err)
	}
	var cleanupErr *prompt.InputCleanupError
	err = prompt.RunForm(ctx, form)
	if !errors.Is(err, context.Canceled) || injected == nil || !errors.Is(err, injected) || !errors.As(err, &cleanupErr) {
		t.Fatalf("input cleanup lost its typed cause or cancellation: %v", err)
	}
}

func TestAccessibleInputReleasesOwnedDescriptors(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	// Initialize Go's poller before counting; its process-wide descriptors
	// are unrelated to the form's per-read ownership.
	master, slave := editorPTY(t)
	if err := errors.Join(master.Close(), slave.Close()); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for range 32 {
		master, slave := editorPTY(t)
		ctx, cancel := context.WithCancel(t.Context())
		ready := &inputReadyWriter{ready: make(chan struct{}), cancel: cancel}
		form := prompt.NewForm(huh.NewInput().Title("Synthetic input")).
			WithAccessible(true).WithInput(slave).WithOutput(ready)
		err := prompt.RunForm(ctx, form)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation lost its error: %v", err)
		}
		if err := errors.Join(master.Close(), slave.Close()); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); before != after {
		t.Fatalf("accessible input leaked descriptors: before=%d after=%d", before, after)
	}
}

type inputReadyWriter struct {
	once   sync.Once
	ready  chan struct{}
	cancel context.CancelFunc
}

func (w *inputReadyWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Synthetic input") {
		w.once.Do(func() {
			if w.cancel != nil {
				w.cancel()
			}
			close(w.ready)
		})
	}
	return len(p), nil
}
