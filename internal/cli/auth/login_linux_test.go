//go:build linux

package auth

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestReadTokenPreservesBorrowedPipe(t *testing.T) {
	for _, name := range []string{"eof", "cancel", "deadline"} {
		t.Run(name, func(t *testing.T) {
			input, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()  //nolint:errcheck // test owns both pipe ends
			defer writer.Close() //nolint:errcheck // test owns both pipe ends
			descriptor := int(input.Fd())
			before, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			if name == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			if _, err := io.WriteString(writer, "synthetic-token\n"); err != nil {
				t.Fatal(err)
			}
			if name == "eof" {
				_ = writer.Close()
			}
			type result struct {
				token string
				err   error
			}
			finished := make(chan result, 1)
			go func() {
				token, err := readToken(ctx, command.Invocation{Stdin: input})
				finished <- result{token: token, err: err}
			}()
			waited := false
			defer func() {
				if !waited {
					cancel()
					_ = writer.Close()
					<-finished
				}
			}()
			observed := time.NewTimer(500 * time.Millisecond)
			defer observed.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				queued, err := unix.IoctlGetInt(descriptor, unix.TIOCINQ)
				if err != nil {
					t.Fatal(err)
				}
				if queued == 0 {
					break
				}
				select {
				case <-observed.C:
					_ = writer.Close()
					<-finished
					waited = true
					t.Fatal("token input did not consume the pipe prefix")
				case <-poll.C:
				}
			}
			if name == "cancel" {
				cancel()
			}
			var got result
			select {
			case got = <-finished:
				waited = true
			case <-time.After(2 * time.Second):
				_ = writer.Close()
				<-finished
				waited = true
				t.Fatal("token input required forced EOF cleanup")
			}
			if name == "eof" {
				if got.err != nil || got.token != "synthetic-token" {
					t.Fatalf("complete token: error=%v, expected token=%t", got.err, got.token == "synthetic-token")
				}
			} else {
				want := context.Canceled
				if name == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(got.err, want) || got.token != "" {
					t.Fatalf("expired pipe: error=%v, token accepted=%t", got.err, got.token != "")
				}
			}
			after, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
			if err != nil || after != before {
				t.Fatal("token input closed the borrowed descriptor or changed its flags")
			}
		})
	}
}
