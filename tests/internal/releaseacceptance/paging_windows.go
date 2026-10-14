package releaseacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/gechr/x/ansi"
	"github.com/gechr/x/shell"
	"golang.org/x/sys/windows"
	"golang.org/x/term"

	changelog "github.com/matcra587/jira-cli"
)

const (
	pagingHelper   = "JIRA_TEST_WINDOWS_PAGING"
	screenPreimage = "owned release-notes screen preimage"
)

// VerifyPaging observes real CLI console output and an independently recorded
// pager stdin. Console cells establish display fidelity, not original write bytes.
func VerifyPaging(t *testing.T, binary string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pager", "no-pager", "no-input", "agent", "machine", "short", "builtin", "builtin canceled", "builtin terminated", "parent canceled"} {
		t.Run(mode, func(t *testing.T) {
			runPagingConsole(t, self, binary, mode)
		})
	}
}

// RunWindowsProcess is called only by the exact helper test in each importing
// suite. Its console is freshly allocated; signals never reach the user's console.
func RunWindowsProcess(t *testing.T) {
	t.Helper()
	switch os.Getenv(pagingHelper) {
	case "":
		return
	case "pager":
		runWindowsPager(t)
	case "console":
		waitPagingAdmission(t)
		verifyWindowsPaging(t)
	default:
		t.Fatal("invalid native paging helper role")
	}
}

func pagingEnvironment(dir string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "PATHEXT=" + os.Getenv("PATHEXT"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "WINDIR=" + os.Getenv("WINDIR"),
		"TEMP=" + dir, "TMP=" + dir, "HOME=" + dir, "USERPROFILE=" + dir,
		"APPDATA=" + filepath.Join(dir, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(dir, "AppData", "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"TERM=xterm-256color", "JIRA_KEYRING=false", "JIRA_NO_UPDATE_CHECK=1",
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
	}
}

func consoleFile(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.OpenFile(name, os.O_RDWR, 0) //nolint:gosec // G304: callers select only this owned console's CONIN$/CONOUT$ devices.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func consoleMode(t *testing.T, f *os.File) uint32 {
	t.Helper()
	var value uint32
	if err := windows.GetConsoleMode(windows.Handle(f.Fd()), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func setConsoleWindow(t *testing.T, output *os.File, height int16) {
	t.Helper()
	window := windows.SmallRect{Right: 99, Bottom: height - 1}
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleWindowInfo")
	// Win32 receives one owned SMALL_RECT for this helper's console.
	result, _, err := call.Call(output.Fd(), 1, uintptr(unsafe.Pointer(&window))) //nolint:gosec // G103: bounded native console fixture.
	if result == 0 {
		t.Fatal("set native paging window", err)
	}
}

func prepareConsole(t *testing.T, output *os.File, short bool) {
	t.Helper()
	setConsoleWindow(t, output, 8)
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleScreenBufferSize")
	// A wide backing buffer avoids line wrapping in the display observation;
	// terminal.Size still observes the 100-column, eight-row window.
	size := uintptr(1024) | uintptr(2048)<<16
	if result, _, err := call.Call(output.Fd(), size); result == 0 {
		t.Fatal("set native paging buffer", err)
	}
	if short {
		setConsoleWindow(t, output, 40)
	}
	if err := windows.SetConsoleCursorPosition(windows.Handle(output.Fd()), windows.Coord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(output, screenPreimage); err != nil {
		t.Fatal(err)
	}
}

type consoleScreen struct {
	width      int
	cursor     windows.Coord
	cells      []uint16
	attributes []uint16
}

// consoleCell is Win32 CHAR_INFO: one UTF-16 code unit and its WORD attributes.
type consoleCell struct {
	char, attributes uint16
}

func readConsole(t *testing.T, output *os.File) consoleScreen {
	t.Helper()
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(output.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	count := int(info.Size.X) * (int(info.CursorPosition.Y) + 1)
	if count <= 0 || count > 1024*2048 {
		t.Fatal("invalid native paging screen size", info.Size, info.CursorPosition)
	}
	buffer := make([]consoleCell, count)
	if unsafe.Sizeof(buffer[0]) != 4 {
		t.Fatal("invalid native paging cell layout")
	}
	region := windows.SmallRect{Right: info.Size.X - 1, Bottom: info.CursorPosition.Y}
	wantRegion := region
	size := uintptr(uint16(info.Size.X)) | uintptr(uint16(info.CursorPosition.Y+1))<<16
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleOutputW")
	// Read cells rather than character counts: wide diagnostic glyphs occupy
	// multiple cells. Require the exact copied rectangle, including blank cells.
	result, _, err := call.Call(output.Fd(), uintptr(unsafe.Pointer(&buffer[0])), size, 0, uintptr(unsafe.Pointer(&region))) //nolint:gosec // G103: bounded, owned CHAR_INFO rectangle.
	if result == 0 || region != wantRegion {
		t.Fatal("read native paging screen", region, wantRegion, err)
	}
	cells := make([]uint16, count)
	attributes := make([]uint16, count)
	for i, cell := range buffer {
		cells[i] = cell.char
		attributes[i] = cell.attributes
	}
	return consoleScreen{width: int(info.Size.X), cursor: info.CursorPosition, cells: cells, attributes: attributes}
}

func readActiveConsole(t *testing.T) consoleScreen {
	t.Helper()
	active, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := active.Close(); err != nil {
			t.Error(err)
		}
	}()
	return readConsole(t, active)
}

func (s consoleScreen) text() string {
	rows := make([]string, 0, len(s.cells)/s.width)
	for start := 0; start < len(s.cells); start += s.width {
		rows = append(rows, strings.TrimRight(windows.UTF16ToString(s.cells[start:start+s.width]), " "))
	}
	return strings.Join(rows, "\n")
}

func assertDocumentDisplay(t *testing.T, screen consoleScreen, document string) {
	t.Helper()
	// Reading console cells cannot distinguish authored trailing spaces from
	// unused cells. Compare the documented display projection on both sides;
	// exact raw bytes are checked independently through the pager's stdin.
	rows := strings.Split(document, "\n")
	for i, row := range rows {
		encoded, err := windows.UTF16FromString(row)
		if err != nil || strings.ContainsAny(row, "\t\r") || ansi.StringWidth(row) != utf8.RuneCountInString(row) || len(encoded)-1 >= screen.width {
			t.Fatal("document needs a richer native cell-layout fixture")
		}
		rows[i] = strings.TrimRight(row, " ")
	}
	want := screenPreimage + "\n" + strings.Join(rows, "\n")
	if screen.text() != want {
		t.Fatal("native console display differs from complete embedded Markdown")
	}
}

type pagingKey struct {
	event, padding              uint16
	down                        uint32
	repeat, virtual, scan, char uint16
	state                       uint32
}

func sendPagingKey(t *testing.T, input *os.File, char uint16) {
	t.Helper()
	virtual := char
	if virtual >= 'a' && virtual <= 'z' {
		virtual -= 'a' - 'A'
	}
	records := []pagingKey{{event: windows.KEY_EVENT, down: 1, repeat: 1, virtual: virtual, char: char}, {event: windows.KEY_EVENT, repeat: 1, virtual: virtual, char: char}}
	if unsafe.Sizeof(records[0]) != 20 {
		t.Fatal("invalid native paging key layout")
	}
	var written uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	result, _, err := call.Call(input.Fd(), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written))) //nolint:gosec // G103: two owned native key records.
	if result == 0 || written != uint32(len(records)) {
		t.Fatal("write native paging keys", written, err)
	}
}

type pagerReceipt struct {
	Args                        []string
	InputBefore, InputChanged   uint32
	OutputBefore, OutputChanged uint32
}

func runWindowsPager(t *testing.T) {
	t.Helper()
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 4 || args[3] != "literal $value & `quoted` ; 古い" {
		t.Fatal("native pager literal arguments changed", args)
	}
	document, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(args[1], document, 0o600); err != nil {
		t.Fatal(err)
	}
	input, output := consoleFile(t, "CONIN$"), consoleFile(t, "CONOUT$")
	receipt := pagerReceipt{Args: args[1:], InputBefore: consoleMode(t, input), OutputBefore: consoleMode(t, output)}
	if err := windows.SetConsoleMode(windows.Handle(input.Fd()), receipt.InputBefore&^(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT)); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(windows.Handle(output.Fd()), receipt.OutputBefore^windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		t.Fatal(err)
	}
	receipt.InputChanged, receipt.OutputChanged = consoleMode(t, input), consoleMode(t, output)
	if receipt.InputChanged == receipt.InputBefore || receipt.OutputChanged == receipt.OutputBefore {
		t.Fatal("native pager did not change both borrowed modes")
	}
	// Leave both modes changed deliberately. The CLI's shared paging owner
	// must restore them after this independently observed child exits.
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(args[2], data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func verifyWindowsPaging(t *testing.T) {
	t.Helper()
	mode, binary := os.Getenv("JIRA_TEST_PAGING_MODE"), os.Getenv("JIRA_TEST_PAGING_BINARY")
	if binary == "" || !slices.Contains([]string{"pager", "no-pager", "no-input", "agent", "machine", "short", "builtin", "builtin canceled", "builtin terminated", "parent canceled"}, mode) {
		t.Fatal("invalid native paging helper selection")
	}
	input, output := consoleFile(t, "CONIN$"), consoleFile(t, "CONOUT$")
	prepareConsole(t, output, mode == "short")
	width, height, err := term.GetSize(int(output.Fd()))
	if err != nil || width != 100 || (mode != "short" && height != 8) || (mode == "short" && height != 40) {
		t.Fatal("native paging terminal size unproven", width, height, err)
	}
	beforeInput, beforeOutput := consoleMode(t, input), consoleMode(t, output)
	beforeScreen := readConsole(t, output)
	dir := t.TempDir()
	document, marker := filepath.Join(dir, "document 古い"), filepath.Join(dir, "launched $")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	literal := "literal $value & `quoted` ; 古い"
	pagerArgs := []string{self, "-test.run=^TestReleaseNotesWindowsProcess$", "--", document, marker, literal}
	quoted := make([]string, len(pagerArgs))
	for i, arg := range pagerArgs {
		quoted[i] = shell.Quote(arg)
	}
	args := []string{"release-notes", "--output=human", "--color=never"}
	switch mode {
	case "no-pager", "no-input":
		args = append(args, "--"+mode)
	case "machine":
		args = []string{"release-notes", "--output=json"}
	case "short":
		args = append(args, "0.1.0")
	}
	env := append(pagingEnvironment(dir), pagingHelper+"=pager", "JIRA_PAGER="+strings.Join(quoted, " "), "PAGER=")
	if mode == "agent" {
		env = append(env, "AGENT=1")
	}
	builtin := strings.HasPrefix(mode, "builtin") || mode == "parent canceled"
	if builtin {
		env = append(env, "JIRA_PAGER="+filepath.Join(dir, "missing pager"))
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // G204: caller supplies the suite's owned compile-once CLI.
	cmd.Env, cmd.Dir = env, dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, output
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-finished
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	sentEnd, sentQuit := false, false
	var runErr error
wait:
	for {
		select {
		case runErr = <-finished:
			joined = true
			break wait
		case <-ticker.C:
			if !builtin || sentQuit {
				continue
			}
			text := readActiveConsole(t).text()
			if !sentEnd && strings.Contains(text, "(q to quit)") {
				if strings.Contains(text, screenPreimage) {
					t.Fatal("builtin pager did not enter a separate native screen")
				}
				sentEnd = true
				switch mode {
				case "parent canceled":
					data, err := json.Marshal(cmd.Process.Pid)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(os.Getenv("JIRA_TEST_PAGING_READY"), data, 0o600); err != nil {
						t.Fatal(err)
					}
				case "builtin":
					sendPagingKey(t, input, 'G')
				default:
					event := uint32(windows.CTRL_C_EVENT)
					if mode == "builtin terminated" {
						event = windows.CTRL_BREAK_EVENT
					}
					if err := windows.GenerateConsoleCtrlEvent(event, 0); err != nil {
						t.Fatal(err)
					}
					sentQuit = true
				}
			}
			if mode == "builtin" && sentEnd && !sentQuit && strings.Contains(text, "Page and query with truthful JQL") {
				sendPagingKey(t, input, 'q')
				sentQuit = true
			}
		}
	}
	if ctx.Err() != nil {
		t.Fatal("native paging did not complete before deadline", ctx.Err())
	}
	if mode == "builtin canceled" || mode == "builtin terminated" {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != 6 {
			t.Fatal("native pager cancellation lost exit6", runErr)
		}
	} else if runErr != nil {
		t.Fatal("native release notes failed", runErr, readConsole(t, output).text())
	}
	if consoleMode(t, input) != beforeInput || consoleMode(t, output) != beforeOutput {
		t.Fatal("native pager left borrowed console modes changed")
	}
	_, markerErr := os.Stat(marker)
	if mode == "pager" {
		data, err := os.ReadFile(document) //nolint:gosec // G304: independently observe the owned pager's temporary output.
		if err != nil || string(data) != changelog.Full() {
			t.Fatal("native pager did not receive exact full Markdown", err)
		}
		data, err = os.ReadFile(marker) //nolint:gosec // G304: independently observe the owned pager's literal argv receipt.
		var observed pagerReceipt
		if err != nil || json.Unmarshal(data, &observed) != nil || !slices.Equal(observed.Args, []string{document, marker, literal}) {
			t.Fatal("native configured pager did not acknowledge literal argv", err)
		}
		if observed.InputBefore != beforeInput || observed.OutputBefore != beforeOutput || observed.InputChanged == beforeInput || observed.OutputChanged == beforeOutput {
			t.Fatal("native pager mode-change receipt does not bind the borrowed console")
		}
	} else {
		if !errors.Is(markerErr, os.ErrNotExist) {
			t.Fatal("disabled or builtin paging launched the configured pager", markerErr)
		}
		if _, err := os.Stat(document); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("disabled or builtin paging entered the configured pager", err)
		}
		active := consoleFile(t, "CONOUT$")
		screen := readConsole(t, active)
		switch {
		case builtin:
			if !sentEnd || !sentQuit || !strings.HasPrefix(screen.text(), screenPreimage+"\n") {
				t.Fatal("builtin navigation, cancellation or original screen restoration unproven")
			}
			if mode == "builtin" {
				if screen.width != beforeScreen.width || screen.cursor != beforeScreen.cursor || !slices.Equal(screen.cells, beforeScreen.cells) || !slices.Equal(screen.attributes, beforeScreen.attributes) {
					t.Fatal("builtin quit changed original console cells, attributes or cursor")
				}
			} else if !strings.Contains(screen.text(), "context canceled") || strings.Contains(screen.text(), "(q to quit)") || strings.Contains(screen.text(), "# Release Notes") {
				t.Fatal("canceled builtin left pager residue or lost its diagnostic")
			}
		case mode == "machine":
			// Compact JSON is one line. Preserve all wrapped cell spaces rather
			// than applying the human display's row-padding projection.
			var frame struct {
				OK   bool `json:"ok"`
				Meta struct {
					Command string `json:"command"`
				} `json:"meta"`
			}
			if len(screen.cells) <= screen.width || json.Unmarshal([]byte(windows.UTF16ToString(screen.cells[screen.width:])), &frame) != nil || !frame.OK || frame.Meta.Command != "release.notes" {
				t.Fatal("native machine console lost its successful frame")
			}
		case mode == "short":
			release, ok := changelog.Find("0.1.0")
			if !ok {
				t.Fatal("short native paging fixture missing")
			}
			assertDocumentDisplay(t, screen, release.Markdown+"\n")
		default:
			assertDocumentDisplay(t, screen, changelog.Full())
		}
	}
	fmt.Println("windows-paging-verified:" + mode)
}
