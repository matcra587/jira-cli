//go:build windows && nativeconsole

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/windows"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/testfixture"
)

// The outer test allocates a fresh hidden native console for each real public
// command, retaining redirected stdout as the envelope observation boundary.
func TestNativePublicWindowsEditors(t *testing.T) {
	for _, domain := range []string{"config", "draft", "draft-junction", "draft-dot", "draft-space"} {
		for _, mode := range []string{"success", "failure", "invalid", "canceled", "canceled_interrupt"} {
			name := domain + "/" + mode
			t.Run(name, func(t *testing.T) {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				defer cancel()
				process := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestPublicWindowsConsoleProcess$")
				process.Env = append(os.Environ(), "JIRA_TEST_PUBLIC_CONSOLE_CASE="+name)
				process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
				process.WaitDelay = 2 * time.Second
				output, err := process.CombinedOutput()
				if err != nil {
					t.Fatalf("public console invocation failed: %v\n%s", err, output)
				}
				if !strings.Contains(string(output), "public-console-verified:"+name) || !strings.Contains(string(output), "--- PASS: TestPublicWindowsConsoleProcess") || strings.Contains(string(output), "editor-screen-sentinel") {
					t.Fatalf("public acceptance missing or editor polluted stdout: %s", output)
				}
			})
		}
	}
}

func publicWindowsConsole(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("native console allocation unusable: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func publicWindowsConsoleMode(t *testing.T, file *os.File) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(file.Fd()), &mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestPublicWindowsConsoleProcess(t *testing.T) {
	name := os.Getenv("JIRA_TEST_PUBLIC_CONSOLE_CASE")
	if name == "" {
		return
	}
	domain, mode, ok := strings.Cut(name, "/")
	if !ok {
		t.Fatal("invalid owned console case")
	}
	draftDomain := domain == "draft" || strings.HasPrefix(domain, "draft-")
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	input, diagnostics := publicWindowsConsole(t, "CONIN$"), publicWindowsConsole(t, "CONOUT$")
	dir := t.TempDir()
	marker, release, replacement := filepath.Join(dir, "ready.json"), filepath.Join(dir, "release"), filepath.Join(dir, "replacement")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const literal = "literal $value & `quoted` ; no shell 古い"
	argv := []string{executable, "-test.run=^TestPublicWindowsEditorProcess$", "--", mode, marker, release, replacement, literal}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shell.Quote(arg)
	}
	editor := strings.Join(quoted, " ")
	env := []string{"JIRA_EDITOR=" + editor, "JIRA_TEST_PUBLIC_EDITOR=1", "JIRA_KEYRING=false", "JIRA_NO_UPDATE_CHECK=1"}
	path := filepath.Join(dir, "settings 古い $.toml")
	const original = "read_only=false\n"
	saved := []byte("# editor saved work\nread_only=true\n")
	var source []byte
	var directory string
	verifyPath := func() {}
	var reads, writes, requests func() int32
	args := []string{"config", "edit", "--config", path, "--output=json"}
	if draftDomain {
		server, observedReads, observedWrites, _ := binaryDraftServer(t)
		var calls atomic.Int32
		observed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			server.Config.Handler.ServeHTTP(w, r)
		}))
		t.Cleanup(observed.Close)
		directory = filepath.Join(dir, "draft 古い $")
		selected := directory
		switch domain {
		case "draft-junction":
			actual, link := filepath.Join(dir, "actual '古い"), filepath.Join(dir, "selected 'é")
			if err := os.MkdirAll(filepath.Join(actual, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			before := testfixture.Junction(t, link, actual)
			selected = filepath.Join(link, "nested", "draft")
			directory = filepath.Join(actual, "nested", "draft")
			verifyPath = func() { testfixture.SameJunction(t, link, actual, before) }
		case "draft-dot", "draft-space":
			ordinary := directory
			if err := os.Mkdir(ordinary, 0o700); err != nil {
				t.Fatal(err)
			}
			suffix := "."
			if domain == "draft-space" {
				suffix = " "
			}
			directory = `\\?\` + directory + suffix
			selected = directory
			t.Cleanup(func() {
				if err := os.RemoveAll(directory); err != nil {
					t.Error(err)
				}
			})
			verifyPath = func() {
				entries, err := os.ReadDir(ordinary)
				if err != nil || len(entries) != 0 {
					t.Fatalf("editor changed the ordinary sibling: %v %v", entries, err)
				}
			}
		}
		stdout, stderr, exit := runBinaryEnv(t, binaryDraftEnv(t, observed.URL), "issue", "edit", "draft", "KEY-1", "--directory", selected, "--output=json")
		if exit != 0 || observedReads.Load() != 1 || observedWrites.Load() != 0 || calls.Load() != 1 {
			t.Fatal("draft preparation unrun", exit, stdout, stderr)
		}
		source, err = os.ReadFile(filepath.Join(directory, "source.json"))
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(directory, "description.md")
		markdown, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		saved = bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1)
		if bytes.Equal(saved, markdown) {
			t.Fatal("draft fixture did not change")
		}
		// Keep usable synthetic credentials so any unnecessary remote attempt
		// reaches the observed endpoint, including requests whose errors are ignored.
		env = append(env, "JIRA_BASE_URL="+observed.URL, "JIRA_EMAIL=reader@example.test", "JIRA_API_TOKEN=synthetic-token", "JIRA_SECRET_BACKEND=env")
		reads, writes, requests = observedReads.Load, observedWrites.Load, calls.Load
		args = []string{"issue", "edit", "resume", "--directory", selected, "--output=json"}
	} else if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if mode == "invalid" {
		saved = []byte{0xff}
	}
	if err := os.WriteFile(replacement, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	// Fixture subprocesses can change the shared console. Capture the modes
	// borrowed by the editor only after all fixture preparation has completed.
	beforeInput, beforeOutput := publicWindowsConsoleMode(t, input), publicWindowsConsoleMode(t, diagnostics)
	var cancellationTime time.Time
	var editorProcess windows.Handle
	defer func() {
		if editorProcess != 0 {
			_ = windows.CloseHandle(editorProcess)
		}
	}()
	hook := func(process *os.Process) {
		var observation struct {
			PID  uint32
			Args []string
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if raw, err := os.ReadFile(marker); err == nil && json.Unmarshal(raw, &observation) == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("real public CLI editor did not reach readiness")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if len(observation.Args) != 6 || !slices.Equal(observation.Args[:5], []string{mode, marker, release, replacement, literal}) || observation.Args[5] == "" {
			t.Fatalf("literal editor argv changed: %q", observation.Args)
		}
		if draftDomain && observation.Args[len(observation.Args)-1] != path {
			t.Fatal("resume edited wrong saved member")
		}
		if publicWindowsConsoleMode(t, input) == beforeInput || publicWindowsConsoleMode(t, diagnostics) == beforeOutput {
			t.Fatal("editor did not change both actual native modes")
		}
		var err error
		editorProcess, err = windows.OpenProcess(windows.SYNCHRONIZE, false, observation.PID)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "canceled" || mode == "canceled_interrupt" {
			// This subprocess owns a fresh hidden console. Broadcast only there;
			// the driver receives the event and the editor deliberately ignores it.
			cancellationTime = time.Now()
			event := uint32(windows.CTRL_BREAK_EVENT)
			if mode == "canceled_interrupt" {
				event = windows.CTRL_C_EVENT
			}
			if err := windows.GenerateConsoleCtrlEvent(event, 0); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, exit := runBinaryIOStarted(t, env, input, diagnostics, hook, args...)
	if (mode == "canceled" || mode == "canceled_interrupt") && (cancellationTime.IsZero() || time.Since(cancellationTime) > 5*time.Second) {
		t.Fatal("public cancellation did not join the editor within five seconds")
	}
	var frame struct {
		OK     bool            `json:"ok"`
		Meta   envelope.Meta   `json:"meta"`
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatalf("public result not one envelope: %d %q %q", exit, stdout, stderr)
	}
	code := ""
	switch mode {
	case "failure":
		code = "editor_failed"
	case "canceled", "canceled_interrupt":
		code = "canceled"
	case "invalid":
		if draftDomain {
			code = "issue_edit_draft_invalid"
		} else {
			code = "config_invalid"
		}
	}
	wantExit := 0
	if code != "" {
		wantExit = 3
	}
	if mode == "canceled" || mode == "canceled_interrupt" {
		wantExit = 6
	}
	wantCommand := "config.edit"
	if draftDomain {
		wantCommand = "issue.edit.resume"
	}
	if exit != wantExit || frame.Meta.Command != wantCommand || frame.OK != (code == "") || code == "" && len(frame.Errors) != 0 || code != "" && (len(frame.Errors) != 1 || frame.Errors[0].Code != code) {
		t.Fatalf("public editor outcome wrong: %d %q", exit, stdout)
	}
	if publicWindowsConsoleMode(t, input) != beforeInput || publicWindowsConsoleMode(t, diagnostics) != beforeOutput {
		t.Fatal("public CLI did not restore borrowed native modes")
	}
	state, err := windows.WaitForSingleObject(editorProcess, 0)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("CLI returned before editor exited: %d %v", state, err)
	}
	if draftDomain {
		var receipt envelope.IssueEditDraftOutput
		if json.Unmarshal(frame.Data, &receipt) != nil || receipt.Source == nil || string(receipt.Source.DescriptionBytes) != binaryDraftNative || !receipt.Draft.Ready || !receipt.Draft.EditorAttempted || receipt.Draft.Directory != directory || reads() != 1 || writes() != 0 || requests() != 1 {
			t.Fatal("resume lost recovery or reopened Jira", stdout)
		}
		if receipt.Draft.EditorSucceeded != (mode == "success" || mode == "invalid") {
			t.Fatal("resume reported the wrong editor acknowledgment", stdout)
		}
		if code == "" && (!receipt.Draft.EditorSucceeded || receipt.Changed == nil || !*receipt.Changed || !bytes.Contains(receipt.Description, []byte("authored")) || !bytes.Contains(receipt.Description, []byte("9007199254740993")) || !bytes.Contains(receipt.Description, []byte("retained"))) {
			t.Fatal("resume lost lossless candidate", stdout)
		}
		if code != "" && (len(receipt.Description) != 0 || receipt.Changed != nil) {
			t.Fatal("failed editor fabricated candidate", stdout)
		}
		after, err := os.ReadFile(filepath.Join(directory, "source.json"))
		if err != nil || !bytes.Equal(after, source) {
			t.Fatal("resume changed native source", err)
		}
		after, err = os.ReadFile(path)
		if err != nil || !bytes.Equal(after, saved) {
			t.Fatal("resume lost saved Markdown", err)
		}
		verifyPath()
	} else {
		want := []byte(original)
		if mode == "success" {
			want = saved
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, want) {
			t.Fatal("config editor published wrong bytes", err)
		}
		if code != "" {
			after, err := os.ReadFile(path + ".rejected")
			if err != nil || !bytes.Equal(after, saved) {
				t.Fatal("config editor lost rejected work", err)
			}
		}
	}
	t.Log("public-console-verified:" + name)
}

// The editor leaves changed console modes for the actual CLI to restore. It
// waits for independent observation before completing, including failed exits.
func TestPublicWindowsEditorProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_PUBLIC_EDITOR") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 6 {
		os.Exit(40)
	}
	args := os.Args[index+1:]
	if args[0] == "canceled" || args[0] == "canceled_interrupt" {
		signal.Ignore(os.Interrupt)
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		os.Exit(41)
	}
	var mode uint32
	handle := windows.Handle(os.Stderr.Fd())
	if windows.GetConsoleMode(handle, &mode) != nil || windows.SetConsoleMode(handle, mode^windows.ENABLE_WRAP_AT_EOL_OUTPUT) != nil {
		os.Exit(42)
	}
	_, _ = fmt.Fprintln(os.Stdout, "editor-screen-sentinel")
	data, err := os.ReadFile(args[3])
	if err != nil || os.WriteFile(args[5], data, 0o600) != nil {
		os.Exit(43)
	}
	raw, err := json.Marshal(struct {
		PID  uint32
		Args []string
	}{uint32(os.Getpid()), args})
	if err != nil || os.WriteFile(args[1], raw, 0o600) != nil {
		os.Exit(44)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(args[2]); err == nil {
			if args[0] == "failure" {
				os.Exit(45)
			}
			os.Exit(0)
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(46)
}
