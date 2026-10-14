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
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
)

// Real composition and command grammar run in a fresh console with owned
// public settings. Native credential storage is disabled for every fixture.
func TestNativePublicWindowsInput(t *testing.T) {
	for _, field := range []string{"site", "email", "password", "confirmation"} {
		for _, outcome := range []string{"cancel", "deadline"} {
			name := field + "/" + outcome
			t.Run(name, func(t *testing.T) {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, executable, "-test.v", "-test.run=^TestPublicWindowsInputProcess$", "-test.timeout=10s")
				child.Env = append(os.Environ(), "JIRA_TEST_PUBLIC_INPUT_CONSOLE="+name)
				child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
				child.WaitDelay = time.Second
				output, err := child.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "public-input-verified:"+name) ||
					!strings.Contains(string(output), "--- PASS: TestPublicWindowsInputProcess") {
					t.Fatalf("public input acceptance did not complete: %v\n%s", err, output)
				}
			})
		}
	}
}

func TestPublicWindowsInputProcess(t *testing.T) {
	name := os.Getenv("JIRA_TEST_PUBLIC_INPUT_CONSOLE")
	if name == "" {
		return
	}
	field, outcome, ok := strings.Cut(name, "/")
	if !ok {
		t.Fatal("invalid owned public input case")
	}
	input, diagnostics := publicWindowsConsole(t, "CONIN$"), publicWindowsConsole(t, "CONOUT$")
	beforeInput, beforeOutput := publicWindowsConsoleMode(t, input), publicWindowsConsoleMode(t, diagnostics)
	dir := t.TempDir()
	for _, pair := range [][2]string{
		{"HOME", dir},
		{"USERPROFILE", dir},
		{"APPDATA", filepath.Join(dir, "appdata")},
		{"LOCALAPPDATA", filepath.Join(dir, "localappdata")},
		{"XDG_CONFIG_HOME", filepath.Join(dir, "config")},
		{"XDG_CACHE_HOME", filepath.Join(dir, "cache")},
		{"XDG_DATA_HOME", filepath.Join(dir, "data")},
		{"AGENT", "0"},
		{"TERM", "dumb"},
		{"JIRA_NO_UPDATE_CHECK", "1"},
		{"JIRA_KEYRING", "false"},
		{"JIRA_API_TOKEN", ""},
		{"JIRA_API_TOKEN_ENV", ""},
		{"JIRA_BASE_URL", ""},
		{"JIRA_EMAIL", ""},
		{"JIRA_SECRET_BACKEND", ""},
		{"OP_SERVICE_ACCOUNT_TOKEN", ""},
	} {
		t.Setenv(pair[0], pair[1])
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	path := filepath.Join(dir, "settings.toml")
	original := []byte("keyring=false\n")
	args := []string{"--config", path, "--output=json", "auth", "login"}
	title, prefix := "Jira site", "https://owned.example.invalid"
	switch field {
	case "email":
		args = append(args, "--site", server.URL)
		title, prefix = "Email", "owned-console@example.invalid"
	case "password":
		args = append(args, "--site", server.URL, "--email", "owned-console@example.invalid")
		title, prefix = "API token", "owned-synthetic-token-prefix"
	case "confirmation":
		original = []byte("# retained legacy source\ndefault_profile='owned'\n[[profiles]]\nname='owned'\nbase_url='" + server.URL + "'\nemail='owned-console@example.invalid'\nsecret_backend='env'\n")
		path = filepath.Join(dir, "legacy.toml")
		args = []string{"--output=json", "config", "migrate", "--source", path, "--config", filepath.Join(dir, "target.toml"), "--recovery-dir", filepath.Join(dir, "recovery")}
		title, prefix = "Convert legacy profile", "invalid boolean"
	case "site":
		args = append(args, "--email", "owned-console@example.invalid")
	default:
		t.Fatal("unknown public native input field")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := os.Create(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	oldInput, oldOutput, oldDiagnostics := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = input, capture, diagnostics
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldInput, oldOutput, oldDiagnostics }()
	ctx, cancel := context.WithCancel(t.Context())
	if outcome == "deadline" {
		cancel()
		ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	}
	defer cancel()
	finished := make(chan int, 1)
	go func() { finished <- root.Run(ctx, args) }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			writePublicWindowsInput(t, input, "\r")
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Error("public native input did not join during failure recovery")
			}
		}
	}()
	ready := time.NewTimer(2 * time.Second)
	defer ready.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		screen := windowsElvishConsoleText(diagnostics)
		masked := field != "password" || publicWindowsConsoleMode(t, input)&windows.ENABLE_ECHO_INPUT == 0
		if strings.Contains(screen, title) && masked {
			break
		}
		select {
		case code := <-finished:
			joined = true
			t.Fatalf("public command exited before its native prompt: exit=%d", code)
		case <-ready.C:
			t.Fatal("public native input prompt was not observed")
		case <-ticker.C:
		}
	}
	if field == "confirmation" {
		prefix += "\r"
	}
	writePublicWindowsInput(t, input, prefix)
	prefixReady := time.NewTimer(2 * time.Second)
	defer prefixReady.Stop()
	for {
		var queued uint32
		if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(input.Fd()), &queued); err != nil {
			t.Fatal(err)
		}
		if queued == 0 {
			break
		}
		select {
		case code := <-finished:
			joined = true
			t.Fatalf("public native input ended before consuming its prefix: exit=%d", code)
		case <-prefixReady.C:
			t.Fatal("public native input did not consume its prefix")
		case <-ticker.C:
		}
	}
	select {
	case code := <-finished:
		joined = true
		t.Fatalf("public input completed without cancellation or an accepted answer: exit=%d", code)
	case <-time.After(20 * time.Millisecond):
	}
	var exit int
	if outcome == "cancel" {
		cancel()
	} else {
		select {
		case code := <-finished:
			joined = true
			if ctx.Err() == nil {
				t.Fatalf("public native input ended before its deadline: exit=%d", code)
			}
			exit = code
		case <-ctx.Done():
		}
	}
	if !joined {
		select {
		case exit = <-finished:
			joined = true
		case <-time.After(3 * time.Second):
			t.Fatal("public native input required synthetic Enter after cancellation")
		}
	}
	os.Stdout = oldOutput
	if _, err := capture.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(capture).Decode(&result); err != nil {
		t.Fatalf("public input polluted or omitted result stdout: %v", err)
	}
	wantExit, wantCode := cli.ExitCanceled, "canceled"
	if outcome == "deadline" {
		wantExit, wantCode = cli.ExitTimeout, "timeout"
	}
	if exit != wantExit || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != wantCode || requests.Load() != 0 {
		t.Fatalf("public input lost cancellation or entered HTTP: exit=%d errors=%v requests=%d", exit, result.Errors, requests.Load())
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, original) {
		t.Fatal("public input cancellation changed original settings")
	}
	if field == "confirmation" {
		for _, unpublished := range []string{"target.toml", "recovery"} {
			if _, err := os.Stat(filepath.Join(dir, unpublished)); !os.IsNotExist(err) {
				t.Fatalf("canceled confirmation published %s: %v", unpublished, err)
			}
		}
	}
	if publicWindowsConsoleMode(t, input) != beforeInput || publicWindowsConsoleMode(t, diagnostics) != beforeOutput {
		t.Fatal("public input cancellation changed borrowed native modes")
	}
	if field == "password" && strings.Contains(windowsElvishConsoleText(diagnostics), prefix) {
		t.Fatal("public native password exposed its synthetic prefix")
	}
	fmt.Println("public-input-verified:" + name)
}

func writePublicWindowsInput(t *testing.T, input *os.File, text string) {
	t.Helper()
	var records []windowsElvishKeyRecord
	for _, char := range utf16.Encode([]rune(text)) {
		virtual := uint16(0)
		if char == '\r' {
			virtual = windows.VK_RETURN
		} else if char >= 'a' && char <= 'z' {
			virtual = char - 'a' + 'A'
		}
		records = append(records,
			windowsElvishKeyRecord{EventType: windows.KEY_EVENT, Down: 1, Repeat: 1, Virtual: virtual, Char: char},
			windowsElvishKeyRecord{EventType: windows.KEY_EVENT, Repeat: 1, Virtual: virtual, Char: char})
	}
	writeWindowsElvishKeys(t, input, records)
}
