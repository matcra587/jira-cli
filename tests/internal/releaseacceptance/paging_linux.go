//go:build linux

package releaseacceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/ansi"
	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	changelog "github.com/matcra587/jira-cli"
)

// VerifyPaging checks actual terminal paging and headless policy on a rebuilt CLI.
func VerifyPaging(t *testing.T, binary string) {
	t.Helper()
	for _, mode := range []string{"pager", "no-pager", "no-input", "agent", "machine", "short", "builtin", "builtin canceled", "builtin terminated"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := openTerminal(t)
			height := uint16(8)
			if mode == "short" {
				height = 100
			}
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: height, Col: 100}); err != nil {
				t.Fatal(err)
			}
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			pager := filepath.Join(dir, "pager script")
			document := filepath.Join(dir, "document")
			marker := filepath.Join(dir, "launched")
			if err := os.WriteFile(pager, []byte("#!/bin/sh\ncat > \"$1\"\nprintf launched > \"$2\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			args := []string{"release-notes", "--output=human", "--color=never"}
			if mode == "no-pager" {
				args = append(args, "--no-pager")
			}
			if mode == "no-input" {
				args = append(args, "--no-input")
			}
			if mode == "machine" {
				args = []string{"release-notes", "--output=json"}
			}
			if mode == "short" {
				args = append(args, "0.1.0")
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TERM=xterm-256color", "JIRA_KEYRING=false", "JIRA_PAGER=" + shell.Quote(pager) + " " + shell.Quote(document) + " " + shell.Quote(marker), "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			if strings.HasPrefix(mode, "builtin") {
				env = append(env, "JIRA_PAGER="+filepath.Join(dir, "missing pager"), "PAGER=")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, binary, args...)
			process.Env = env
			process.Dir = dir
			process.Stdin = slave
			process.Stdout = slave
			process.Stderr = slave
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- process.Wait() }()
			if err := unix.SetNonblock(int(master.Fd()), true); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			buffer := make([]byte, 8192)
			var runErr error
			done := false
			sentEnd, sentQuit := false, false
			for {
				n, readErr := unix.Read(int(master.Fd()), buffer)
				if n > 0 {
					output.Write(buffer[:n])
				}
				if strings.HasPrefix(mode, "builtin") && !sentQuit {
					if !sentEnd && bytes.Contains(output.Bytes(), []byte("(q to quit)")) {
						sentEnd = true
						if mode == "builtin canceled" || mode == "builtin terminated" {
							signal := os.Interrupt
							if mode == "builtin terminated" {
								signal = unix.SIGTERM
							}
							if err := process.Process.Signal(signal); err != nil {
								t.Fatal(err)
							}
							sentQuit = true
						} else if _, err := unix.Write(int(master.Fd()), []byte("G")); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "builtin" && bytes.Contains(output.Bytes(), []byte("Page and query with truthful JQL")) {
						sentQuit = true
						if _, err := unix.Write(int(master.Fd()), []byte("q")); err != nil {
							t.Fatal(err)
						}
					}
				}
				if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EIO) {
					t.Fatal(readErr)
				}
				if n <= 0 && done {
					break
				}
				select {
				case runErr = <-finished:
					done = true
				case <-ctx.Done():
					t.Fatal("terminal release notes did not finish")
				case <-time.After(time.Millisecond):
				}
			}
			if mode == "builtin canceled" || mode == "builtin terminated" {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 6 {
					t.Fatalf("builtin pager cancellation lost exit6: %v %q", runErr, output.String())
				}
			} else if runErr != nil {
				t.Fatalf("terminal release notes failed: %v %q", runErr, output.String())
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || *before != *after {
				t.Fatal("release notes left borrowed terminal modes changed")
			}
			_, markerErr := os.Stat(marker)
			if strings.HasPrefix(mode, "builtin") {
				if !os.IsNotExist(markerErr) || !sentEnd || !sentQuit || !bytes.Contains(output.Bytes(), []byte("\x1b[?1049h")) || !bytes.Contains(output.Bytes(), []byte("\x1b[?1049l")) {
					t.Fatal("builtin pager lost alternate screen, navigation, cancellation or restoration")
				}
			} else if mode == "pager" {
				root, err := os.OpenRoot(dir)
				if err != nil {
					t.Fatal(err)
				}
				data, err := root.ReadFile("document")
				if closeErr := root.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil || string(data) != changelog.Full() || markerErr != nil {
					t.Fatalf("pager did not receive exact full document: %v %v", err, markerErr)
				}
			} else {
				if !os.IsNotExist(markerErr) {
					t.Fatal("headless, short or disabled paging launched a pager")
				}
				plain := strings.ReplaceAll(ansi.Strip(output.String()), "\r\n", "\n")
				if mode == "short" {
					release, _ := changelog.Find("0.1.0")
					if plain != release.Markdown+"\n" {
						t.Fatal("short document changed raw Markdown")
					}
				} else if mode == "machine" {
					if !strings.Contains(plain, `"command":"release.notes"`) {
						t.Fatal("machine terminal lost framed document")
					}
				} else if plain != changelog.Full() {
					t.Fatal("direct terminal output lost full Markdown")
				}
			}
		})
	}
}

func openTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(descriptor), "release-notes-terminal")
	t.Cleanup(func() {
		if err := master.Close(); err != nil {
			t.Error(err)
		}
	})
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
	t.Cleanup(func() {
		if err := slave.Close(); err != nil {
			t.Error(err)
		}
	})
	return master, slave
}
