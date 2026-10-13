//go:build linux && nativekeyring

package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// testNativeKeyringAuthTerminal runs only inside the validated private bus.
// The terminal and native records contain synthetic fixture data throughout.
func testNativeKeyringAuthTerminal(t *testing.T) {
	const email = "terminal-fixture@example.invalid"
	const token = "synthetic-terminal-fixture-token"
	const other = "other-fixture@example.invalid"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		account, candidate, ok := r.BasicAuth()
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" || !ok || account != email || candidate != token {
			t.Error("terminal login sent an unexpected verification request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"terminal:fixture","displayName":"Terminal fixture","active":true}`)
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "terminal.toml")
	if err := os.WriteFile(configPath, []byte("base_url = '"+server.URL+"'\nemail = '"+email+"'\nkeyring = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=true", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_NO_UPDATE_CHECK=1", "TERM=xterm-256color"}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env = append(env, name+"="+os.Getenv(name))
	}
	absent := func(t *testing.T) {
		t.Helper()
		if out := nativeKeyringTool(t, "", 1, "lookup", "service", "jira-cli", "username", "default"); out != "" {
			t.Fatal("terminal operation left a native credential")
		}
	}
	stored := func(t *testing.T) string {
		t.Helper()
		return nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default")
	}
	invoke := func(t *testing.T, extra []string, steps []terminalAuthStep, wantExit int, wantCode string, args ...string) (frame, string) {
		t.Helper()
		argv := append([]string{"--config", configPath, "--output=json"}, args...)
		stdout, screen, exit := nativeKeyringTerminal(t, append(append([]string{}, env...), extra...), steps, argv...)
		if strings.Contains(ansi.Strip(stdout+screen), token) {
			t.Fatal("password appeared in terminal or machine output")
		}
		if exit != wantExit {
			t.Fatalf("terminal command %v: exit=%d want=%d", args, exit, wantExit)
		}
		var result frame
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("terminal UI polluted machine stdout: %v", err)
		}
		if result.OK != (wantExit == cli.ExitOK) {
			t.Fatal("terminal command reported inconsistent success")
		}
		if wantExit != cli.ExitOK {
			var entry struct {
				Code string `json:"code"`
			}
			if len(result.Errors) != 1 || json.Unmarshal(result.Errors[0], &entry) != nil || entry.Code != wantCode {
				t.Fatal("terminal command returned the wrong error classification")
			}
		}
		return result, screen
	}
	pinned := []string{"auth", "login", "--site", server.URL, "--email", email}
	absent(t)
	t.Run("canceled first login stores nothing", func(t *testing.T) {
		invoke(t, nil, []terminalAuthStep{{wait: "API token", input: token}, {wait: "*****", input: "\x03"}}, cli.ExitCanceled, "canceled", pinned...)
		absent(t)
		if calls.Load() != 0 {
			t.Fatal("canceled login contacted Jira")
		}
	})
	t.Run("blank token validates before native storage", func(t *testing.T) {
		result, screen := invoke(t, nil, []terminalAuthStep{
			{wait: "API token", input: "\r"},
			{wait: "API token is required", input: token + "\r"},
		}, cli.ExitOK, "", pinned...)
		var record credentials.Record
		if err := json.Unmarshal([]byte(stored(t)), &record); err != nil {
			t.Fatal(err)
		}
		if record.Site != server.URL || record.Email != email || record.Token != token || record.Version != credentials.Version {
			t.Fatal("terminal login stored a different credential tuple")
		}
		var data struct {
			AccountID string `json:"account_id"`
		}
		if json.Unmarshal(result.Data, &data) != nil || data.AccountID != "terminal:fixture" || calls.Load() != 1 {
			t.Fatal("terminal login did not verify exactly once")
		}
		if strings.Contains(screen, "Jira site") || strings.Contains(screen, "Email") {
			t.Fatal("terminal login asked again for explicitly pinned identity")
		}
	})
	t.Run("canceled replacement preserves native record", func(t *testing.T) {
		before := stored(t)
		invoke(t, nil, []terminalAuthStep{{wait: "API token", input: "\x03"}}, cli.ExitCanceled, "canceled", pinned...)
		if stored(t) != before || calls.Load() != 1 {
			t.Fatal("canceled replacement changed credentials or contacted Jira")
		}
	})
	t.Run("accessible login collects site account and token", func(t *testing.T) {
		before := stored(t)
		_, screen := invoke(t, []string{"TERM=dumb"}, []terminalAuthStep{
			{wait: "Jira site", input: server.URL + "\r"},
			{wait: "Email", input: email + "\r"},
			{wait: "API token", passwordReady: true, input: token + "\r"},
		}, cli.ExitOK, "", "auth", "login")
		if !strings.Contains(screen, "Email") || stored(t) != before || calls.Load() != 2 {
			t.Fatal("terminal login did not collect and preserve the selected identity")
		}
	})
	t.Run("dumb terminal keeps login UI off machine stdout", func(t *testing.T) {
		before := stored(t)
		invoke(t, []string{"TERM=dumb"}, []terminalAuthStep{{passwordReady: true, input: token + "\r"}}, cli.ExitOK, "", pinned...)
		if stored(t) != before || calls.Load() != 3 {
			t.Fatal("accessible login did not verify the same native credential once")
		}
	})
	t.Run("agent and no-input refuse terminal prompts", func(t *testing.T) {
		for _, flags := range []struct {
			name string
			env  []string
			args []string
		}{
			{name: "agent", env: []string{"AGENT=1"}},
			{name: "no-input", args: []string{"--no-input"}},
		} {
			t.Run(flags.name, func(t *testing.T) {
				before := stored(t)
				_, screen := invoke(t, flags.env, nil, cli.ExitValidation, "flag_required", append(append([]string{}, pinned...), flags.args...)...)
				if strings.Contains(screen, "API token") || stored(t) != before || calls.Load() != 3 {
					t.Fatal("headless login asked for or changed credentials")
				}
			})
		}
	})
	t.Run("negative logout confirms stored identity", func(t *testing.T) {
		before := stored(t)
		_, screen := invoke(t, []string{"JIRA_EMAIL=" + other}, []terminalAuthStep{{wait: "Keep", input: "n\r"}}, cli.ExitCanceled, "canceled", "auth", "logout")
		if !strings.Contains(screen, email) || !strings.Contains(screen, server.URL) || strings.Contains(screen, other) || !strings.Contains(screen, "not revoked") {
			t.Fatal("logout confirmation did not identify the actual stored credential and revocation caveat")
		}
		if stored(t) != before || calls.Load() != 3 {
			t.Fatal("negative logout changed the native record or contacted Jira")
		}
	})
	t.Run("aborted logout preserves native record", func(t *testing.T) {
		before := stored(t)
		invoke(t, nil, []terminalAuthStep{{wait: "Keep", input: "\x03"}}, cli.ExitCanceled, "canceled", "auth", "logout")
		if stored(t) != before {
			t.Fatal("aborted logout removed the native record")
		}
	})
	t.Run("dumb terminal keeps confirmation UI off machine stdout", func(t *testing.T) {
		before := stored(t)
		invoke(t, []string{"TERM=dumb"}, []terminalAuthStep{{wait: "[y/N]", beforeInput: []string{email, server.URL, "not revoked"}, input: "n\r"}}, cli.ExitCanceled, "canceled", "auth", "logout")
		if stored(t) != before || calls.Load() != 3 {
			t.Fatal("accessible negative logout changed the native record or contacted Jira")
		}
	})
	t.Run("dumb terminal default logout keeps native record", func(t *testing.T) {
		before := stored(t)
		invoke(t, []string{"TERM=dumb"}, []terminalAuthStep{{wait: "[y/N]", beforeInput: []string{email, server.URL, "not revoked"}, input: "\r"}}, cli.ExitCanceled, "canceled", "auth", "logout")
		if stored(t) != before || calls.Load() != 3 {
			t.Fatal("accessible default logout changed the native record or contacted Jira")
		}
	})
	t.Run("affirmative logout removes native record", func(t *testing.T) {
		result, _ := invoke(t, nil, []terminalAuthStep{{wait: "Keep", input: "y\r"}}, cli.ExitOK, "", "auth", "logout")
		var data struct {
			Email            string `json:"email"`
			RevocationNeeded bool   `json:"revocation_needed"`
		}
		if json.Unmarshal(result.Data, &data) != nil || data.Email != email || !data.RevocationNeeded {
			t.Fatal("confirmed logout lost the credential identity or revocation requirement")
		}
		absent(t)
		if calls.Load() != 3 {
			t.Fatal("logout contacted Jira")
		}
	})
	t.Run("prefilled identity", func(t *testing.T) {
		testNativeKeyringPrefilledLogin(t, invoke, server.URL, email, token, &calls)
	})
}

type terminalAuthStep struct {
	wait          string
	input         string
	passwordReady bool
	beforeInput   []string
	focused       bool
	focusedValue  string
	signal        os.Signal
}

// nativeKeyringTerminal keeps stdout as a pipe while stdin and stderr share
// a real terminal. Each input waits for an observed screen, not a fixed delay.
func nativeKeyringTerminal(t *testing.T, env []string, steps []terminalAuthStep, args ...string) (string, string, int) {
	t.Helper()
	master, slave := editorPTY(t)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 120}); err != nil {
		t.Fatal(err)
	}
	before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	beforeFlags, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	changed := make(chan struct{}, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		buffer := make([]byte, 4096)
		for {
			n, readErr := master.Read(buffer)
			if n != 0 {
				mu.Lock()
				_, _ = screen.Write(buffer[:n])
				mu.Unlock()
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	defer func() {
		_ = slave.Close()
		_ = master.Close()
		<-finished
	}()
	onStart := func(process *os.Process) {
		for _, step := range steps {
			deadline := time.NewTimer(5 * time.Second)
			for {
				mu.Lock()
				visible := strings.Contains(screen.String(), step.wait)
				if step.focused {
					// Huh's renderer can update the focus border without
					// repeating the title. Observe its focused input line;
					// blurred titles can already be visible in the form.
					visible = false
					for _, line := range strings.FieldsFunc(ansi.Strip(screen.String()), func(r rune) bool { return r == '\r' || r == '\n' }) {
						if strings.TrimRight(line, " ") == "┃ > "+step.focusedValue || (step.focusedValue == "" && strings.TrimRight(line, " ") == "┃ >") {
							visible = true
							break
						}
					}
				}
				mu.Unlock()
				if step.passwordReady {
					state, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
					if err != nil {
						t.Fatal(err)
					}
					visible = visible && state.Lflag&unix.ECHO == 0
				}
				if visible {
					deadline.Stop()
					break
				}
				select {
				case <-changed:
				case <-time.After(5 * time.Millisecond):
				case <-deadline.C:
					mu.Lock()
					observed := ansi.Strip(screen.String())
					mu.Unlock()
					t.Fatalf("terminal command did not display %q; observed %q", step.wait, observed)
				}
			}
			mu.Lock()
			observed := screen.String()
			mu.Unlock()
			for _, text := range step.beforeInput {
				if !strings.Contains(observed, text) {
					t.Fatalf("terminal command did not display %q before input", text)
				}
			}
			if _, err := io.WriteString(master, step.input); err != nil {
				t.Fatal(err)
			}
			if step.signal != nil {
				if err := process.Signal(step.signal); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	stdout, _, exit := runExecutableIOInDirStarted(t, os.Getenv("JIRA_TEST_KEYRING_BINARY"), t.TempDir(), env, slave, slave, onStart, args...)
	after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil || *after != *before {
		t.Fatal("authentication form did not restore borrowed terminal modes")
	}
	afterFlags, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
	if err != nil || afterFlags != beforeFlags {
		t.Fatal("authentication form did not restore borrowed descriptor flags")
	}
	_ = slave.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal output reader did not finish")
	}
	mu.Lock()
	result := screen.String()
	mu.Unlock()
	return stdout, result, exit
}
