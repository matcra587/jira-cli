//go:build unix

package input_test

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

func TestPreparedScreenPreservesSharedDescriptorFlags(t *testing.T) {
	for _, nonblocking := range []bool{false, true} {
		name := "blocking"
		if nonblocking {
			name = "nonblocking"
		}
		t.Run(name, func(t *testing.T) {
			testPreparedScreenFlags(t, nonblocking)
		})
	}
}

func testPreparedScreenFlags(t *testing.T, nonblocking bool) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = read.Close() })
	t.Cleanup(func() { _ = write.Close() })
	fd := read.Fd()
	if err := unix.SetNonblock(int(fd), nonblocking); err != nil {
		t.Fatal(err)
	}
	before, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := input.PrepareScreen(read)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	after, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("screen changed shared descriptor flags: before=%#x after=%#x", before, after)
	}
	if _, err := write.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var buffer [1]byte
	if n, err := prepared.Read(buffer[:]); n != 1 || err != nil || buffer[0] != 'x' {
		t.Fatalf("prepared screen read: n=%d err=%v bytes=%q", n, err, buffer[:])
	}
}
