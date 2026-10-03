//go:build linux

package output

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestDocumentPagerHelperProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_DOCUMENT_PAGER") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 3 {
		os.Exit(40)
	}
	mode, document, ready := os.Args[index+1], os.Args[index+2], os.Args[index+3]
	content, err := io.ReadAll(os.Stdin)
	if err != nil || os.WriteFile(document, content, 0o600) != nil {
		os.Exit(41)
	}
	if _, err := term.MakeRaw(int(os.Stdout.Fd())); err != nil {
		os.Exit(42)
	}
	if _, err := term.MakeRaw(int(os.Stderr.Fd())); err != nil {
		os.Exit(43)
	}
	if os.WriteFile(ready, []byte("ready"), 0o600) != nil {
		os.Exit(44)
	}
	switch mode {
	case "failure":
		os.Exit(45)
	case "wait":
		time.Sleep(30 * time.Second)
	}
	_, _ = fmt.Fprint(os.Stdout, "pager selected destination")
	os.Exit(0)
}

func documentTestTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(descriptor), "document-test-terminal")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 6, Col: 40}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func TestExternalDocumentPagerSelectionAndTerminalRestoration(t *testing.T) {
	t.Setenv("JIRA_TEST_DOCUMENT_PAGER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"preferred", "missing preferred", "invalid preferred", "failed started", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			_, input := documentTestTerminal(t)
			_, destination := documentTestTerminal(t)
			_, diagnostics := documentTestTerminal(t)
			streams := []*os.File{input, destination, diagnostics}
			before := make([]*term.State, len(streams))
			for i, file := range streams {
				state, err := term.GetState(int(file.Fd()))
				if err != nil {
					t.Fatal(err)
				}
				before[i] = state
			}
			dir := t.TempDir()
			document, ready, fallback := filepath.Join(dir, "document bytes"), filepath.Join(dir, "ready"), filepath.Join(dir, "fallback")
			childMode := "success"
			if mode == "failed started" {
				childMode = "failure"
			}
			if mode == "canceled" {
				childMode = "wait"
			}
			command := shell.Quote(executable) + " -test.run=^TestDocumentPagerHelperProcess$ -- " + childMode + " " + shell.Quote(document) + " " + shell.Quote(ready)
			t.Setenv("JIRA_PAGER", command)
			t.Setenv("PAGER", shell.Quote(executable)+" -test.run=^TestDocumentPagerHelperProcess$ -- success "+shell.Quote(fallback)+" "+shell.Quote(ready))
			if mode == "missing preferred" {
				t.Setenv("JIRA_PAGER", filepath.Join(dir, "missing pager"))
				t.Setenv("PAGER", command)
			}
			if mode == "invalid preferred" {
				t.Setenv("JIRA_PAGER", "\"")
				t.Setenv("PAGER", command)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			const content = "# Document\n\nExact **Markdown** & <content>\n"
			go func() { finished <- runPager(ctx, input, destination, diagnostics, content) }()
			var result error
			if mode == "canceled" {
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					select {
					case err := <-finished:
						t.Fatalf("pager exited before readiness: %v", err)
					case <-ctx.Done():
						t.Fatal("pager did not start")
					case <-time.After(time.Millisecond):
					}
				}
				cancel()
			}
			select {
			case result = <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("pager did not finish")
			}
			if mode == "failed started" {
				if result == nil || !strings.Contains(result.Error(), "--no-pager") {
					t.Fatalf("failed started pager lost failure: %v", result)
				}
			} else if mode == "canceled" {
				if !errors.Is(result, context.Canceled) {
					t.Fatalf("pager lost cancellation: %v", result)
				}
			} else if result != nil {
				t.Fatal(result)
			}
			data, err := os.ReadFile(document)
			if err != nil || string(data) != content {
				t.Fatalf("pager input differs: %q, %v", data, err)
			}
			if _, err := os.Stat(fallback); !os.IsNotExist(err) {
				t.Fatal("selected or started pager unexpectedly fell through")
			}
			for i, file := range streams {
				after, err := term.GetState(int(file.Fd()))
				if err != nil || *before[i] != *after {
					t.Fatalf("borrowed terminal%d was not restored: %v", i, err)
				}
			}
		})
	}
}

func TestDocumentBufferedViewKeepsTerminalWidthAndStreamsShortContent(t *testing.T) {
	t.Setenv("JIRA_PAGER", "must-not-launch")
	master, terminal := documentTestTerminal(t)
	var observedWidth int
	res := Result{Envelope: envelope.Envelope{OK: true}, Human: func(view *View, _ envelope.Envelope) error {
		observedWidth = view.Output().Width()
		view.Line("short document")
		return nil
	}}
	if err := RenderDocument(t.Context(), terminal, FormatHuman, res, DocumentOptions{Input: terminal, Diagnostics: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if observedWidth != 40 {
		t.Fatalf("buffered View lost terminal width: %d", observedWidth)
	}
	poll := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
	if count, err := unix.Poll(poll, 1000); err != nil || count != 1 {
		t.Fatal(err)
	}
	buffer := make([]byte, 100)
	n, err := master.Read(buffer)
	if err != nil || !bytes.Contains(buffer[:n], []byte("short document")) {
		t.Fatalf("short document vanished: %q, %v", buffer[:n], err)
	}
}
