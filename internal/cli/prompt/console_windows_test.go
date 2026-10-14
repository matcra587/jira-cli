//go:build windows && nativeconsole

package prompt_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"charm.land/huh/v2"
	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/cli/input"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// Native input events belong only to each newly allocated hidden console.
// Redirected stdout remains available for independent test-entry receipts.
func TestNativeWindowsAccessibleConsole(t *testing.T) {
	for _, kind := range []string{"text", "confirmation", "password"} {
		for _, outcome := range []string{"cancel", "deadline", "success", "retry"} {
			for _, echo := range []string{"echo_on", "echo_off"} {
				name := kind + "/" + outcome + "/" + echo
				t.Run(name, func(t *testing.T) { runOwnedInputConsole(t, name) })
			}
		}
	}
}

func TestNativeWindowsConsoleOwner(t *testing.T) {
	for _, outcome := range []string{"panic", "before_kernel", "restore_failure", "restore_reuse", "pre_canceled", "queued_unicode", "canceled_queued_unicode"} {
		t.Run(outcome, func(t *testing.T) { runOwnedInputConsole(t, "owner/"+outcome) })
	}
}

func runOwnedInputConsole(t *testing.T, name string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestWindowsInputConsoleProcess$", "-test.timeout=12s")
	child.Env = append(os.Environ(), "JIRA_TEST_OWNED_INPUT_CONSOLE="+name)
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	child.WaitDelay = time.Second
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("owned native console case failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "input-console-verified:"+name) ||
		!strings.Contains(string(output), "--- PASS: TestWindowsInputConsoleProcess") {
		t.Fatalf("native input guard did not enter: %s", output)
	}
}

func TestWindowsInputConsoleProcess(t *testing.T) {
	name := os.Getenv("JIRA_TEST_OWNED_INPUT_CONSOLE")
	if name == "" {
		return
	}
	parts := strings.Split(name, "/")
	terminal, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminal.Close() }()
	if parts[0] == "owner" {
		testWindowsConsoleOwner(t, terminal, parts[1])
	} else {
		before := ownedConsoleMode(t, terminal)
		before |= windows.ENABLE_LINE_INPUT
		if parts[2] == "echo_off" {
			before &^= windows.ENABLE_ECHO_INPUT
		} else {
			before |= windows.ENABLE_ECHO_INPUT
		}
		if err := windows.SetConsoleMode(windows.Handle(terminal.Fd()), before); err != nil {
			t.Fatal(err)
		}
		// Repeated fields exercise native mode restoration and reader reuse.
		for range 2 {
			testWindowsAccessibleConsole(t, terminal, before, parts[0], parts[1])
		}
		assertOwnedConsoleReuse(t, terminal)
	}
	fmt.Println("input-console-verified:" + name)
}

func testWindowsAccessibleConsole(t *testing.T, terminal *os.File, before uint32, kind, outcome string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	if outcome == "deadline" {
		cancel()
		ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	}
	defer cancel()
	value, answer := "unchanged", false
	entered := make(chan struct{})
	var once sync.Once
	validate := func() error {
		if outcome == "success" {
			return nil
		}
		once.Do(func() { close(entered) })
		return errors.New("owned validation requires another answer")
	}
	var field huh.Field
	switch kind {
	case "text":
		field = huh.NewInput().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&value))).Validate(func(string) error { return validate() })
	case "confirmation":
		field = huh.NewConfirm().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&answer)))
		if outcome != "success" {
			field = observedConsoleField{Field: field, entered: entered}
		}
	case "password":
		field = prompt.WithPasswordContext(ctx, huh.NewInput().EchoMode(huh.EchoModePassword).
			Validate(func(string) error { return validate() }), huh.NewPointerAccessor(&value))
	default:
		t.Fatal("unknown native field")
	}
	form := prompt.NewForm(field).WithAccessible(true).WithInput(terminal).WithOutput(io.Discard)
	finished := make(chan error, 1)
	go func() { finished <- prompt.RunForm(ctx, form) }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			// Failure recovery releases only this owned console's pending read.
			ownedConsoleKeys(t, terminal, "\r")
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Error("owned console input did not join during failure cleanup")
			}
		}
	}()
	const unicodeAnswer = "古い café"
	if outcome == "success" {
		text := unicodeAnswer
		if kind == "confirmation" {
			text = "yes"
		}
		ownedConsoleKeys(t, terminal, text+"\r")
	} else if kind == "password" && outcome != "retry" {
		waitOwnedConsole(t, finished, &joined, func() bool {
			return ownedConsoleMode(t, terminal)&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT) == 0
		})
		ownedConsoleKeys(t, terminal, "synthetic-prefix")
		waitOwnedConsole(t, finished, &joined, func() bool {
			var queued uint32
			if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(terminal.Fd()), &queued); err != nil {
				t.Fatal(err)
			}
			return queued == 0
		})
	} else {
		text := "first rejected answer"
		if kind == "confirmation" {
			// Accessible Confirm uses its own boolean parser rather than
			// the rich field's Validate callback. Force that native retry.
			text = "invalid boolean"
		}
		ownedConsoleKeys(t, terminal, text+"\r")
		waitOwnedConsole(t, finished, &joined, func() bool {
			select {
			case <-entered:
				return true
			default:
				return false
			}
		})
	}
	if outcome != "success" {
		select {
		case err := <-finished:
			joined = true
			t.Fatalf("native input returned before cancellation or another answer: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		if outcome != "deadline" {
			cancel()
		}
	}
	var runErr error
	select {
	case runErr = <-finished:
		joined = true
	case <-time.After(2 * time.Second):
		t.Fatal("native input required synthetic Enter after cancellation")
	}
	if outcome == "success" {
		if runErr != nil || (kind == "confirmation" && !answer) || (kind != "confirmation" && value != unicodeAnswer) {
			t.Fatalf("successful native input lost its answer: value=%q answer=%v error=%v", value, answer, runErr)
		}
	} else {
		want := context.Canceled
		if outcome == "deadline" {
			want = context.DeadlineExceeded
		}
		if !errors.Is(runErr, want) || prompt.IsInputFailure(runErr) || value != "unchanged" || answer {
			t.Fatalf("native cancellation lost its ordinary outcome or published input: value=%q answer=%v error=%v", value, answer, runErr)
		}
	}
	if after := ownedConsoleMode(t, terminal); after != before {
		t.Fatalf("native field changed borrowed console modes: before=%d after=%d", before, after)
	}
}

func testWindowsConsoleOwner(t *testing.T, terminal *os.File, outcome string) {
	t.Helper()
	// Retain this exact thread after the nested owner unlocks, so a lingering
	// monitor would interrupt the independent read used to prove thread reuse.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before := ownedConsoleMode(t, terminal)
	if outcome == "restore_reuse" {
		// Warm output admission before freeing the slot under test.
		warm, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = ownedConsoleMode(t, warm)
		if err := warm.Close(); err != nil {
			t.Fatal(err)
		}
		// This valid input mode also admits output restoration, where bit 4
		// enables VT processing. A stale slot must not modify that new object.
		before = windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT
		if err := windows.SetConsoleMode(windows.Handle(terminal.Fd()), before); err != nil {
			t.Fatal(err)
		}
	}
	borrowedConnection, err := terminal.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if outcome == "queued_unicode" || outcome == "canceled_queued_unicode" {
		const firstAnswer, secondAnswer = "古い🙂", "next-é"
		first, second := "original", "unchanged"
		firstField := huh.NewInput().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&first)))
		if outcome == "canceled_queued_unicode" {
			firstField.Validate(func(string) error { cancel(); return nil })
		}
		form := prompt.NewForm(firstField, huh.NewInput().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&second)))).
			WithAccessible(true).WithInput(terminal).WithOutput(io.Discard)
		ownedConsoleKeys(t, terminal, firstAnswer+"\r"+secondAnswer+"\r")
		err := prompt.RunForm(ctx, form)
		if outcome == "queued_unicode" {
			if err != nil || first != firstAnswer || second != secondAnswer {
				t.Fatalf("independent console owners lost queued Unicode: first=%q second=%q error=%v", first, second, err)
			}
		} else {
			if !errors.Is(err, context.Canceled) || first != "original" || second != "unchanged" {
				t.Fatalf("canceled queued form published input: first=%q second=%q error=%v", first, second, err)
			}
			remaining, err := bufio.NewReader(terminal).ReadString('\n')
			if err != nil || strings.TrimRight(remaining, "\r\n") != secondAnswer {
				t.Fatalf("canceled console owner consumed later queued input: %q %v", remaining, err)
			}
		}
		if after := ownedConsoleMode(t, terminal); after != before {
			t.Fatalf("queued console input changed original modes: before=%d after=%d", before, after)
		}
		return
	}
	borrowed := windows.Handle(terminal.Fd())
	if err := windows.SetHandleInformation(borrowed, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	beforeFlags := ownedConsoleHandleFlags(t, borrowed)
	var preparedHandle windows.Handle
	var closedCause error
	var replacement *os.File
	var otherOutputs []*os.File
	defer func() {
		for _, file := range otherOutputs {
			_ = file.Close()
		}
	}()
	var replacementMode uint32
	inspectPrepared := func(reader io.Reader) {
		fd, ok := reader.(interface{ Fd() uintptr })
		if !ok {
			t.Fatal("native console owner did not preserve the password descriptor API")
		}
		preparedHandle = windows.Handle(fd.Fd())
		if preparedHandle == borrowed || ownedConsoleHandleFlags(t, preparedHandle)&windows.HANDLE_FLAG_INHERIT != 0 {
			t.Fatal("native console input does not own an independent non-inherited handle")
		}
	}
	if outcome == "panic" {
		const sentinel = "owned field panic"
		func() {
			defer func() {
				if recovered := recover(); recovered != sentinel {
					t.Fatalf("native owner changed panic: %v", recovered)
				}
			}()
			_, _ = input.RunTerminalContext(ctx, terminal, func(reader io.Reader) error {
				inspectPrepared(reader)
				if err := windows.SetConsoleMode(windows.Handle(terminal.Fd()), before^windows.ENABLE_ECHO_INPUT); err != nil {
					t.Fatal(err)
				}
				cancel()
				panic(sentinel)
			})
		}()
	} else {
		if outcome == "pre_canceled" {
			cancel()
		}
		err, cleanupErr := input.RunTerminalContext(ctx, terminal, func(reader io.Reader) error {
			if outcome == "pre_canceled" {
				t.Fatal("expired console context entered native field")
			}
			inspectPrepared(reader)
			if outcome == "restore_failure" || outcome == "restore_reuse" {
				if err := terminal.Close(); err != nil {
					return err
				}
				closedCause = borrowedConnection.Control(func(uintptr) { t.Error("closed original file admitted restoration") })
				if outcome == "restore_reuse" {
					var err error
					// Handle allocation need not reuse the first free slot.
					// Keep misses alive until this bounded fixture finds it.
					for range 32 {
						replacement, err = os.OpenFile("CONOUT$", os.O_RDWR, 0)
						if err != nil {
							t.Fatal(err)
						}
						if windows.Handle(replacement.Fd()) == borrowed {
							break
						}
						otherOutputs = append(otherOutputs, replacement)
					}
					// Keep the replacement alive throughout the owner's cleanup.
					t.Cleanup(func() { _ = replacement.Close() })
					if windows.Handle(replacement.Fd()) != borrowed {
						t.Fatal("owned replacement did not reuse the closed borrowed slot")
					}
					replacementMode = ownedConsoleMode(t, replacement)
					if replacementMode == before {
						t.Fatal("replacement output fixture does not distinguish input restoration")
					}
				}
				cancel()
				return nil
			}
			cancel()
			// Give the monitor time to observe ERROR_NOT_FOUND before kernel
			// entry. A single cancellation attempt cannot pass this guard.
			time.Sleep(20 * time.Millisecond)
			_, err := reader.Read(make([]byte, 1))
			return err
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("native owner lost cancellation: %v", err)
		}
		if outcome == "restore_failure" || outcome == "restore_reuse" {
			if replacement != nil && ownedConsoleMode(t, replacement) != replacementMode {
				t.Fatal("console restoration changed the replacement output handle's modes")
			}
			if closedCause == nil || !errors.Is(cleanupErr, closedCause) {
				t.Fatalf("failed native restoration disappeared: %v", cleanupErr)
			}
			assertOwnedConsoleHandleClosed(t, preparedHandle)
			return
		}
		if cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
	}
	if preparedHandle != 0 {
		assertOwnedConsoleHandleClosed(t, preparedHandle)
	}
	if flags := ownedConsoleHandleFlags(t, borrowed); flags != beforeFlags {
		t.Fatalf("console owner changed borrowed handle flags: before=%d after=%d", beforeFlags, flags)
	}
	if after := ownedConsoleMode(t, terminal); after != before {
		t.Fatalf("native owner failed exact restoration: before=%d after=%d", before, after)
	}
	assertOwnedConsoleReuse(t, terminal)
}

func ownedConsoleHandleFlags(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var flags uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	result, _, err := call.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags))) //nolint:gosec // G103: live uint32 output for this synchronous native call.
	if result == 0 {
		t.Fatal("native console handle flags unavailable:", err)
	}
	return flags
}

func assertOwnedConsoleHandleClosed(t *testing.T, handle windows.Handle) {
	t.Helper()
	// Cleanup can reuse this numeric slot for a disk handle while formatting
	// another native failure. Require the admitted console object to be gone.
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("owned native console handle survived cleanup: mode=%d error=%v", mode, err)
	}
}

func assertOwnedConsoleReuse(t *testing.T, terminal *os.File) {
	t.Helper()
	writer := make(chan struct{})
	go func() {
		defer close(writer)
		time.Sleep(20 * time.Millisecond)
		ownedConsoleKeys(t, terminal, "reuse\r")
		var queued uint32
		if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(terminal.Fd()), &queued); err != nil {
			t.Error(err)
		}
		t.Logf("owned reuse input queued=%d modes=%d", queued, ownedConsoleMode(t, terminal))
	}()
	// Read only authored data, without assuming native CR/LF translation.
	got := make([]byte, len("reuse"))
	_, err := io.ReadFull(observedReuseReader{file: terminal, test: t}, got)
	<-writer
	if err != nil || string(got) != "reuse" {
		t.Fatalf("borrowed console or reused thread is unusable: %q %v", got, err)
	}
}

type observedReuseReader struct {
	file *os.File
	test *testing.T
}

func (r observedReuseReader) Read(buffer []byte) (int, error) {
	n, err := r.file.Read(buffer)
	r.test.Logf("owned reuse read bytes=%q error=%v", buffer[:n], err)
	return n, err
}

type observedConsoleField struct {
	huh.Field
	entered chan struct{}
}

func (f observedConsoleField) RunAccessible(w io.Writer, r io.Reader) error {
	return f.Field.RunAccessible(w, &observedConsoleReader{reader: r, entered: f.entered})
}

type observedConsoleReader struct {
	reader  io.Reader
	entered chan struct{}
	once    sync.Once
}

func (r *observedConsoleReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	for _, b := range buffer[:n] {
		if b == '\n' {
			r.once.Do(func() { close(r.entered) })
		}
	}
	return n, err
}

func waitOwnedConsole(t *testing.T, finished <-chan error, joined *bool, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case err := <-finished:
			*joined = true
			t.Fatalf("native input ended before observed entry: %v", err)
		case <-deadline.C:
			t.Fatal("native console entry was not observed")
		case <-ticker.C:
		}
	}
}

func ownedConsoleMode(t *testing.T, terminal *os.File) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(terminal.Fd()), &mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

type ownedConsoleKey struct {
	EventType uint16
	Padding   uint16
	Down      uint32
	Repeat    uint16
	Virtual   uint16
	Scan      uint16
	Char      uint16
	State     uint32
}

func ownedConsoleKeys(t *testing.T, terminal *os.File, text string) {
	t.Helper()
	var records []ownedConsoleKey
	for _, char := range utf16.Encode([]rune(text)) {
		virtual := uint16(0)
		if char == '\r' {
			virtual = windows.VK_RETURN
		} else if char >= 'a' && char <= 'z' {
			virtual = char - 'a' + 'A'
		} else if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == ' ' {
			virtual = char
		}
		records = append(records,
			ownedConsoleKey{EventType: windows.KEY_EVENT, Down: 1, Repeat: 1, Virtual: virtual, Char: char},
			ownedConsoleKey{EventType: windows.KEY_EVENT, Repeat: 1, Virtual: virtual, Char: char})
	}
	if len(records) == 0 || unsafe.Sizeof(ownedConsoleKey{}) != 20 {
		t.Fatal("invalid owned console key records")
	}
	var written uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	// Only the test-owned console receives these synchronous native records.
	result, _, err := call.Call(terminal.Fd(), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written))) //nolint:gosec // G103: pointers describe the verified native INPUT_RECORD layout during this synchronous call.
	if result == 0 || written != uint32(len(records)) {
		t.Fatalf("owned native input injection failed: count=%d error=%v", written, err)
	}
}
