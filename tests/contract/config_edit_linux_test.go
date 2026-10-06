//go:build linux

package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
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

func TestConfigEditActualTerminalPublishesSelectedFileWithoutEditorStdout(t *testing.T) {
	t.Parallel()
	_, slave := editorPTY(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "selected.toml")
	if err := os.WriteFile(path, []byte("base_url = 'invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(dir, "editor")
	// The editor changes the scratch file and paints its own stdout. The CLI's
	// stdout must still contain one parseable success envelope, with no screen.
	const script = "#!/bin/sh\nprintf 'editor screen\\n'\nprintf '# repaired by editor\\nread_only=true\\n' > \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := runBinaryIOStarted(t, []string{"JIRA_EDITOR=" + shell.Quote(editor), "JIRA_KEYRING=false"}, slave, slave, nil, "config", "edit", "--config", path, "--output=json")
	var result struct {
		OK   bool                      `json:"ok"`
		Data envelope.ConfigEditOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &result) != nil || !result.OK || !result.Data.Changed || result.Data.Created || result.Data.Path != path || len(result.Data.Editor) != 1 || result.Data.Editor[0] != editor {
		t.Fatalf("terminal edit did not publish a clean receipt: exit=%d stdout=%q", exit, stdout)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != "# repaired by editor\nread_only=true\n" {
		t.Fatalf("terminal edit did not repair the selected file: %q, %v", after, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("terminal edit did not secure the config: %v, %v", info, err)
	}
	directory, err := os.Stat(dir)
	if err != nil || directory.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("terminal edit changed existing directory permissions: %v", err)
	}
}

func editorPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open terminal fixture: %v", err)
	}
	master := os.NewFile(uintptr(descriptor), "editor-terminal")
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
	return master, slave
}

// TestConfigEditorHelperProcess is an editor subprocess using the existing
// test executable. It changes real terminal modes and deliberately leaves
// them changed when it exits, so the CLI must restore both borrowed terminals.
func TestConfigEditorHelperProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_EDITOR_PROCESS") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 3 {
		os.Exit(20)
	}
	args := os.Args[index+1:]
	mode, marker, scratch := args[0], args[1], args[2]
	interrupt := make(chan os.Signal, 1)
	if mode == "ignore" {
		signal.Ignore(os.Interrupt)
	} else {
		signal.Notify(interrupt, os.Interrupt)
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		os.Exit(21)
	}
	if _, err := term.MakeRaw(int(os.Stderr.Fd())); err != nil {
		os.Exit(22)
	}
	if mode != "graceful" {
		if err := os.WriteFile(scratch, []byte("# editor saved work\nread_only=true\n"), 0o600); err != nil {
			os.Exit(23)
		}
	}
	if strings.HasPrefix(mode, "fifo_") {
		if err := os.Remove(scratch); err != nil {
			os.Exit(29)
		}
		if err := unix.Mkfifo(scratch, 0o600); err != nil {
			os.Exit(30)
		}
	}
	data, err := json.Marshal(readyEditor{PID: os.Getpid(), Scratch: scratch})
	if err != nil || os.WriteFile(marker, data, 0o600) != nil {
		os.Exit(24)
	}
	switch mode {
	case "success":
		os.Exit(0)
	case "failure":
		os.Exit(25)
	case "fifo_success":
		os.Exit(0)
	case "fifo_failure":
		os.Exit(25)
	case "fifo_graceful":
		<-interrupt
		os.Exit(0)
	case "graceful":
		<-interrupt
		if err := os.WriteFile(scratch, []byte("# editor saved work\nread_only=true\n"), 0o600); err != nil {
			os.Exit(26)
		}
		os.Exit(0)
	case "ignore":
		time.Sleep(time.Hour)
		os.Exit(27)
	default:
		os.Exit(28)
	}
}

func TestConfigEditorRefusesScratchFIFOAfterExitOrCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode string
		code string
		exit int
	}{
		{mode: "fifo_success", code: "config_invalid", exit: 3},
		{mode: "fifo_failure", code: "editor_failed", exit: 3},
		{mode: "fifo_graceful", code: "canceled", exit: 6},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			_, input := editorPTY(t)
			_, diagnostics := editorPTY(t)
			beforeInput, beforeDiagnostics := editorTermios(t, input), editorTermios(t, diagnostics)
			dir := t.TempDir()
			path, marker := filepath.Join(dir, "settings.toml"), filepath.Join(dir, "ready.json")
			const original = "read_only=false\n"
			const earlier = "# earlier rejected work\nread_only=true\n"
			for _, fixture := range []struct{ path, body string }{{path: path, body: original}, {path: path + ".rejected", body: earlier}} {
				if err := os.WriteFile(fixture.path, []byte(fixture.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			editor := strings.Join([]string{shell.Quote(executable), "-test.run=^TestConfigEditorHelperProcess$", "--", tc.mode, shell.Quote(marker)}, " ")
			var emergency *time.Timer
			var scratch string
			var editorPID int
			hook := func(process *os.Process) {
				ready := waitEditorReady(t, marker)
				scratch, editorPID = ready.Scratch, ready.PID
				emergency = time.AfterFunc(3*time.Second, func() { _ = process.Kill() })
				if tc.mode == "fifo_graceful" {
					if err := process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer func() {
				if emergency != nil {
					emergency.Stop()
				}
			}()
			stdout, _, exit := runBinaryIOStarted(t, []string{"JIRA_EDITOR=" + editor, "JIRA_TEST_EDITOR_PROCESS=1", "JIRA_KEYRING=false", "GORACE=atexit_sleep_ms=0", "TMPDIR=" + dir}, input, diagnostics, hook, "config", "edit", "--config", path, "--output=json")
			var result apiFrame
			if exit != tc.exit || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != tc.code || len(result.Data) != 0 && string(result.Data) != "null" {
				t.Fatalf("scratch FIFO blocked or fabricated a result: exit=%d stdout=%q", exit, stdout)
			}
			if editorTermios(t, input) != beforeInput || editorTermios(t, diagnostics) != beforeDiagnostics {
				t.Fatal("scratch refusal left borrowed terminals changed")
			}
			if err := unix.Kill(editorPID, 0); !errors.Is(err, unix.ESRCH) {
				t.Fatalf("editor was not reaped: %v", err)
			}
			for _, fixture := range []struct{ path, body string }{{path: path, body: original}, {path: path + ".rejected", body: earlier}} {
				got, err := os.ReadFile(fixture.path)
				if err != nil || string(got) != fixture.body {
					t.Fatalf("scratch refusal changed stored work: %q, %v", got, err)
				}
			}
			if _, err := os.Stat(filepath.Dir(scratch)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch directory survived refusal: %v", err)
			}
		})
	}
}

func TestConfigEditorCancellationRestoresBothTerminalsAndKeepsSavedWork(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"graceful", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			_, input := editorPTY(t)
			_, diagnostics := editorPTY(t)
			beforeInput := editorTermios(t, input)
			beforeDiagnostics := editorTermios(t, diagnostics)
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			const original = "read_only=false\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "editor-ready.json")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			editor := strings.Join([]string{shell.Quote(executable), "-test.run=^TestConfigEditorHelperProcess$", "--", mode, shell.Quote(marker)}, " ")
			var emergency *time.Timer
			var interrupted time.Time
			var scratch string
			var editorPID int
			defer func() {
				if emergency != nil {
					emergency.Stop()
				}
			}()
			hook := func(process *os.Process) {
				ready := waitEditorReady(t, marker)
				owned, err := os.FindProcess(ready.PID)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = owned.Kill()
					_ = owned.Release()
				})
				scratch = ready.Scratch
				editorPID = ready.PID
				if editorTermios(t, input) == beforeInput || editorTermios(t, diagnostics) == beforeDiagnostics {
					t.Fatal("editor did not establish changed terminal modes before cancellation")
				}
				interrupted = time.Now()
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				// A failing implementation is still cleaned up. This timer is
				// deliberately later than the CLI's accepted cancellation bound.
				emergency = time.AfterFunc(4*time.Second, func() {
					_ = owned.Kill()
				})
			}
			// The editor is this test executable, which may be race-enabled.
			// Disable the detector's artificial one-second exit delay so it
			// cannot masquerade as slow editor cancellation.
			stdout, _, exit := runBinaryIOStarted(t, []string{"JIRA_EDITOR=" + editor, "JIRA_TEST_EDITOR_PROCESS=1", "JIRA_KEYRING=false", "GORACE=atexit_sleep_ms=0"}, input, diagnostics, hook, "config", "edit", "--config", path, "--output=json")
			if emergency != nil {
				emergency.Stop()
			}
			elapsed := time.Since(interrupted)
			var result apiFrame
			if exit != 6 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "canceled" || !strings.Contains(result.Errors[0].Message, path+".rejected") {
				t.Fatalf("editor cancellation lost its recovery receipt: exit=%d stdout=%q", exit, stdout)
			}
			if elapsed > 3*time.Second {
				t.Fatalf("editor cancellation exceeded its bound: %v", elapsed)
			}
			if mode == "graceful" && elapsed > time.Second {
				t.Fatalf("graceful editor was not interrupted promptly: %v", elapsed)
			}
			if err := unix.Kill(editorPID, 0); !errors.Is(err, unix.ESRCH) {
				t.Fatalf("supervised editor was not reaped: %v", err)
			}
			if editorTermios(t, input) != beforeInput || editorTermios(t, diagnostics) != beforeDiagnostics {
				t.Fatal("editor cancellation left borrowed terminals changed")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatalf("cancellation published the config: %q, %v", after, err)
			}
			saved, err := os.ReadFile(path + ".rejected")
			if err != nil || string(saved) != "# editor saved work\nread_only=true\n" {
				t.Fatalf("saved editor work lost: %q, %v", saved, err)
			}
			if _, err := os.Stat(filepath.Dir(scratch)); !os.IsNotExist(err) {
				t.Fatalf("scratch directory retained after cancellation: %v", err)
			}
		})
	}
}

func TestConfigEditorRestoresBothTerminalsAfterNormalOrFailedExit(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			_, input := editorPTY(t)
			_, diagnostics := editorPTY(t)
			beforeInput, beforeDiagnostics := editorTermios(t, input), editorTermios(t, diagnostics)
			dir := t.TempDir()
			path, marker := filepath.Join(dir, "config.toml"), filepath.Join(dir, "ready.json")
			const original = "read_only=false\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			editor := strings.Join([]string{shell.Quote(executable), "-test.run=^TestConfigEditorHelperProcess$", "--", mode, shell.Quote(marker)}, " ")
			stdout, _, exit := runBinaryIOStarted(t, []string{"JIRA_EDITOR=" + editor, "JIRA_TEST_EDITOR_PROCESS=1", "JIRA_KEYRING=false"}, input, diagnostics, nil, "config", "edit", "--config", path, "--output=json")
			var result apiFrame
			if json.Unmarshal([]byte(stdout), &result) != nil || result.OK != (mode == "success") || (mode == "success" && exit != 0) || (mode == "failure" && (exit != 3 || len(result.Errors) != 1 || result.Errors[0].Code != "editor_failed")) {
				t.Fatalf("editor exit misreported: %d %q", exit, stdout)
			}
			if editorTermios(t, input) != beforeInput || editorTermios(t, diagnostics) != beforeDiagnostics {
				t.Fatal("editor exit left borrowed terminals changed")
			}
			after, err := os.ReadFile(path)
			want := original
			if mode == "success" {
				want = "# editor saved work\nread_only=true\n"
			}
			if err != nil || string(after) != want {
				t.Fatalf("editor outcome changed authored bytes: %q %v", after, err)
			}
			if mode == "failure" {
				saved, err := os.ReadFile(path + ".rejected")
				if err != nil || string(saved) != "# editor saved work\nread_only=true\n" {
					t.Fatalf("failed editor lost work: %q %v", saved, err)
				}
			}
		})
	}
}

type readyEditor struct {
	PID     int    `json:"pid"`
	Scratch string `json:"scratch"`
}

func waitEditorReady(t *testing.T, path string) readyEditor {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		var ready readyEditor
		if err == nil && json.Unmarshal(data, &ready) == nil && ready.PID > 0 && ready.Scratch != "" {
			return ready
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("editor did not reach its ready state")
	return readyEditor{}
}

func editorTermios(t *testing.T, file *os.File) unix.Termios {
	t.Helper()
	state, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return *state
}

func TestConfigEditActualTerminalUsesStoredEditorWithoutShellExpansion(t *testing.T) {
	t.Parallel()
	_, slave := editorPTY(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	editor := filepath.Join(dir, "editor with spaces")
	const literal = "literal ; $(never-run)"
	const script = "#!/bin/sh\n[ \"$1\" = 'literal ; $(never-run)' ] || exit 19\nprintf 'read_only=true\\n' > \"$2\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := shell.Quote(editor) + " " + shell.Quote(literal)
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "set", "editor", command, "--config", path, "--no-input", "--output=json")
	if exit != 0 {
		t.Fatalf("cannot store terminal editor: %d %q %q", exit, stdout, stderr)
	}
	stdout, _, exit = runBinaryIOStarted(t, []string{"JIRA_EDITOR=", "VISUAL=unavailable-visual", "EDITOR=unavailable-editor", "JIRA_KEYRING=false"}, slave, slave, nil, "config", "edit", "--config", path, "--output=json")
	var result struct {
		OK   bool                      `json:"ok"`
		Data envelope.ConfigEditOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &result) != nil || !result.OK || !result.Data.Changed || !slices.Equal(result.Data.Editor, []string{editor, literal}) {
		t.Fatalf("stored terminal command lost literal arguments: %d %q", exit, stdout)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != "read_only=true\n" {
		t.Fatalf("stored editor did not publish scratch bytes: %q %v", after, err)
	}
}
