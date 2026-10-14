//go:build windows

package input_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

func TestWindowsPreparedPipeCancellationPreservesBorrowedHandle(t *testing.T) {
	for _, kind := range []string{"anonymous", "synchronous named", "overlapped named"} {
		t.Run(kind, func(t *testing.T) {
			read, write := windowsPipeFixture(t, kind)
			handle := windows.Handle(read.Fd())
			if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
				t.Fatal(err)
			}
			beforeFlags := windowsHandleFlags(t, handle)
			beforeMode := windowsPipeMode(t, handle)
			for attempt := range 3 {
				reader, cleanup, err := input.PrepareStream(read, os.ModeNamedPipe)
				if err != nil || cleanup == nil || reader == read {
					t.Fatalf("selected native pipe was not duplicated: %v", err)
				}
				t.Cleanup(func() {
					if err := cleanup(); err != nil {
						t.Error(err)
					}
				})
				owned, ok := reader.(*os.File)
				if !ok {
					t.Fatal("native pipe duplicate has no file owner")
				}
				if flags := windowsHandleFlags(t, windows.Handle(owned.Fd())); flags&windows.HANDLE_FLAG_INHERIT != 0 {
					t.Fatal("owned pipe duplicate can be inherited")
				}
				if _, err := write.WriteString("entered"); err != nil {
					t.Fatal(err)
				}
				entered, finished := make(chan struct{}), make(chan error, 1)
				go func() {
					prefix := make([]byte, len("entered"))
					if _, err := io.ReadFull(reader, prefix); err != nil {
						finished <- err
						return
					}
					close(entered)
					_, err := io.ReadAll(reader)
					finished <- err
				}()
				select {
				case <-entered:
				case err := <-finished:
					t.Fatalf("native read did not consume its entry prefix: %v", err)
				case <-time.After(3 * time.Second):
					t.Fatal("native read did not enter")
				}
				select {
				case err := <-finished:
					t.Fatalf("native read returned without cancellation or EOF: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				closed := make(chan error, 1)
				go func() { closed <- owned.Close() }()
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					_ = write.Close()
					<-finished
					<-closed
					t.Fatal("closing owned native pipe did not join its pending read")
				}
				if err := <-finished; !errors.Is(err, os.ErrClosed) {
					t.Fatalf("native cancellation lost owned-file closure: %v", err)
				}
				for range 2 {
					if err := cleanup(); err != nil {
						t.Fatal(err)
					}
				}
				if flags, mode := windowsHandleFlags(t, handle), windowsPipeMode(t, handle); flags != beforeFlags || mode != beforeMode {
					t.Fatal("native duplicate changed borrowed flags or pipe mode")
				}
				// The original overlapped read must wait through its own poller.
				// Buffered data alone cannot prove IOCP association survived.
				if kind == "overlapped named" {
					if err := read.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
						t.Fatal(err)
					}
				}
				reused := make(chan error, 1)
				go func() {
					var value [1]byte
					_, err := io.ReadFull(read, value[:])
					if err == nil && value[0] != byte('a'+attempt) {
						err = fmt.Errorf("original pipe read wrong reuse byte: %q", value)
					}
					reused <- err
				}()
				select {
				case err := <-reused:
					t.Fatalf("original pending read failed before data: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				if _, err := write.Write([]byte{byte('a' + attempt)}); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-reused:
					if err != nil {
						t.Fatal("original native pipe unusable after cleanup:", err)
					}
				case <-time.After(4 * time.Second):
					_ = write.Close()
					<-reused
					t.Fatal("original pending pipe read did not complete")
				}
			}
		})
	}
}

func TestWindowsPreparedPipeAcceptsEOF(t *testing.T) {
	for _, kind := range []string{"anonymous", "synchronous named", "overlapped named"} {
		t.Run(kind, func(t *testing.T) {
			read, write := windowsPipeFixture(t, kind)
			reader, cleanup, err := input.PrepareStream(read, os.ModeNamedPipe)
			if err != nil || cleanup == nil {
				t.Fatalf("pipe preparation failed: %v", err)
			}
			defer func() {
				if err := cleanup(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := write.WriteString("exact native bytes"); err != nil {
				t.Fatal(err)
			}
			if err := write.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != "exact native bytes" {
				t.Fatalf("native EOF lost input: %q %v", data, err)
			}
		})
	}
}

func TestWindowsPreparedStreamRetainsOtherReaders(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = regular.Close() })
	read, _ := windowsPipeFixture(t, "anonymous")
	null, err := os.Open("NUL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = null.Close() })
	for _, tc := range []struct {
		name   string
		reader io.Reader
		kinds  os.FileMode
	}{
		{name: "regular", reader: regular, kinds: os.ModeNamedPipe | os.ModeSocket | os.ModeCharDevice},
		{name: "unselected pipe", reader: read, kinds: os.ModeCharDevice},
		{name: "character device", reader: null, kinds: os.ModeNamedPipe | os.ModeCharDevice},
		{name: "nonfile", reader: strings.NewReader("input"), kinds: os.ModeNamedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, cleanup, err := input.PrepareStream(tc.reader, tc.kinds)
			if err != nil || cleanup != nil || reader != tc.reader {
				t.Fatalf("unselected reader acquired a native pipe owner: %v", err)
			}
		})
	}
}

func TestWindowsPreparedStreamDoesNotCoerceSocket(t *testing.T) {
	var data windows.WSAData
	if err := windows.WSAStartup(0x202, &data); err != nil {
		t.Fatal(err)
	}
	defer windows.WSACleanup() //nolint:errcheck // test balances its own Winsock reference
	socket, err := windows.Socket(windows.AF_INET, windows.SOCK_STREAM, windows.IPPROTO_TCP)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.Closesocket(socket) //nolint:errcheck // test owns the unused socket
	file := os.NewFile(uintptr(socket), "native-socket")
	defer file.Close() //nolint:errcheck // retire Go wrapper; Closesocket owns native release
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("socket fixture did not exercise Windows pipe classification: %v", err)
	}
	reader, cleanup, err := input.PrepareStream(file, os.ModeNamedPipe|os.ModeSocket)
	if err != nil || cleanup != nil || reader != file {
		t.Fatalf("native socket acquired a pipe duplicate: %v", err)
	}
	if _, err := windows.GetFileType(socket); err != nil {
		t.Fatal("socket discriminator closed the original handle:", err)
	}
}

func TestWindowsPreparedStreamRefusesClosedFile(t *testing.T) {
	read, _ := windowsPipeFixture(t, "anonymous")
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	reader, cleanup, err := input.PrepareStream(read, os.ModeNamedPipe)
	if reader != nil || cleanup != nil || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("closed native pipe lost its refusal: %v", err)
	}
}

func TestWindowsPreparedStreamDoesNotHidePipeAdmissionFailure(t *testing.T) {
	read, write := windowsPipeFixture(t, "anonymous")
	process := windows.CurrentProcess()
	var handle windows.Handle
	// Read data access alone does not grant read attributes. A readable native
	// pipe can fail the information query; it must never fall back to borrowing.
	if err := windows.DuplicateHandle(process, windows.Handle(read.Fd()), process, &handle,
		windows.FILE_READ_DATA|windows.SYNCHRONIZE, false, 0); err != nil {
		t.Fatal(err)
	}
	restricted := os.NewFile(uintptr(handle), "restricted native pipe")
	defer restricted.Close() //nolint:errcheck // test owns the restricted alias
	if _, err := write.WriteString("r"); err != nil {
		t.Fatal(err)
	}
	var consumed [1]byte
	if _, err := io.ReadFull(restricted, consumed[:]); err != nil || consumed[0] != 'r' {
		t.Fatalf("restricted pipe was not actually readable: %q %v", consumed, err)
	}
	probeErr := windows.GetNamedPipeInfo(handle, nil, nil, nil, nil)
	if !errors.Is(probeErr, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("restricted fixture did not exercise pipe admission failure: %v", probeErr)
	}
	reader, cleanup, err := input.PrepareStream(restricted, os.ModeNamedPipe)
	if reader != nil || cleanup != nil || !errors.Is(err, probeErr) {
		t.Fatalf("pipe admission failure silently retained borrowed input: %v", err)
	}
	if _, err := write.WriteString("reuse"); err != nil {
		t.Fatal(err)
	}
	value := make([]byte, len("reuse"))
	if _, err := io.ReadFull(read, value); err != nil || string(value) != "reuse" {
		t.Fatalf("refused preparation damaged original native input: %q %v", value, err)
	}
}

func windowsPipeFixture(t *testing.T, kind string) (*os.File, *os.File) {
	t.Helper()
	var read, write *os.File
	if kind == "anonymous" {
		var err error
		read, write, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\jira-input-%d-%d`, os.Getpid(), time.Now().UnixNano()))
		if err != nil {
			t.Fatal(err)
		}
		flags := uint32(windows.PIPE_ACCESS_INBOUND | windows.FILE_FLAG_FIRST_PIPE_INSTANCE)
		if kind == "overlapped named" {
			flags |= windows.FILE_FLAG_OVERLAPPED
		}
		server, err := windows.CreateNamedPipe(name, flags, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			_ = windows.CloseHandle(server)
			t.Fatal(err)
		}
		var overlapped windows.Overlapped
		if err := windows.ConnectNamedPipe(server, &overlapped); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			_ = windows.CloseHandle(server)
			_ = windows.CloseHandle(client)
			t.Fatal(err)
		}
		read, write = os.NewFile(uintptr(server), kind), os.NewFile(uintptr(client), "native pipe writer")
	}
	t.Cleanup(func() { _ = read.Close() })
	t.Cleanup(func() { _ = write.Close() })
	return read, write
}

func windowsHandleFlags(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var flags uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	result, _, err := call.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags))) //nolint:gosec // native output points to this live uint32 for the synchronous call
	if result == 0 {
		t.Fatal("cannot inspect native handle flags:", err)
	}
	return flags
}

func windowsPipeMode(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetNamedPipeHandleState(handle, &mode, nil, nil, nil, nil, 0); err != nil {
		t.Fatal("cannot inspect native pipe mode:", err)
	}
	return mode
}
