//go:build unix

package input_test

import (
	"errors"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

func TestPreparedStreamOwnsOnlyItsDuplicate(t *testing.T) {
	for _, tt := range []struct {
		name        string
		nonblocking bool
	}{
		{name: "blocking", nonblocking: false},
		{name: "nonblocking", nonblocking: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := read.Close(); err != nil {
					t.Error(err)
				}
			})
			t.Cleanup(func() {
				if err := write.Close(); err != nil {
					t.Error(err)
				}
			})
			fd := read.Fd()
			if err := unix.SetNonblock(int(fd), tt.nonblocking); err != nil {
				t.Fatal(err)
			}
			before, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			reader, cleanup, err := input.PrepareStream(read, os.ModeNamedPipe)
			if err != nil || cleanup == nil || reader == read {
				t.Fatalf("selected stream was not duplicated: %v", err)
			}
			if err := reader.(io.Closer).Close(); err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			after, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil || before != after {
				t.Fatal("stream cleanup changed borrowed flags or closed its descriptor")
			}
			if _, err := write.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			var buffer [1]byte
			if n, err := read.Read(buffer[:]); n != 1 || err != nil || buffer[0] != 'x' {
				t.Fatal("borrowed stream stopped working after duplicate cleanup")
			}
		})
	}
}

func TestPreparedStreamLeavesUnselectedFileBorrowed(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	reader, cleanup, err := input.PrepareStream(file, os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket)
	if err != nil || cleanup != nil || reader != file {
		t.Fatal("regular file acquired an unnecessary cancellation owner")
	}
}

func TestPreparedStreamReportsClosedBorrowedFile(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := write.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	reader, cleanup, err := input.PrepareStream(read, os.ModeNamedPipe)
	if reader != nil || cleanup != nil || !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed stream preparation lost its failure")
	}
}
