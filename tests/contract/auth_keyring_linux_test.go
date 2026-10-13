//go:build linux && nativekeyring

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestNativeKeyringAuthLifecycle uses a private bus and synthetic credentials.
// Its child inherits no desktop session or existing credential directories.
func TestNativeKeyringAuthLifecycle(t *testing.T) {
	if os.Getenv("JIRA_TEST_KEYRING_CHILD") == "1" {
		testNativeKeyringAuthLifecycle(t)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd, _ := nativeKeyringSession(t, ctx, jiraBinary(t), false)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("private native lifecycle: %v\n%s", err, out)
	}
	t.Logf("private native lifecycle:\n%s", out)
}

func nativeKeyringSession(t *testing.T, ctx context.Context, binary string, block bool) (*exec.Cmd, string) {
	t.Helper()
	for _, name := range []string{"dbus-run-session", "gnome-keyring-daemon", "secret-tool", "gdbus"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("native keyring gate requires %s: %v", name, err)
		}
	}
	dir := t.TempDir()
	if err := xos.EnsureDir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("native session directory must be private")
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "JIRA_TEST_KEYRING_CHILD=1", "JIRA_TEST_KEYRING_ROOT=" + dir, "JIRA_TEST_KEYRING_BINARY=" + binary}
	if block {
		env = append(env, "JIRA_TEST_KEYRING_BLOCK=1")
	}
	for _, entry := range []struct{ name, directory string }{
		{name: "XDG_DATA_HOME", directory: "data"},
		{name: "XDG_CONFIG_HOME", directory: "config"},
		{name: "XDG_CACHE_HOME", directory: "cache"},
		{name: "XDG_RUNTIME_DIR", directory: "runtime"},
	} {
		path := filepath.Join(dir, entry.directory)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, entry.name+"="+path)
	}
	busConfig := filepath.Join(dir, "bus.conf")
	busXML := `<busconfig><type>session</type><listen>unix:path=` + html.EscapeString(filepath.Join(dir, "bus")) + `</listen><auth>EXTERNAL</auth><policy context="default"><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`
	if err := os.WriteFile(busConfig, []byte(busXML), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "dbus-run-session", "--config-file="+busConfig, "--", self, "-test.run=^TestNativeKeyringAuthLifecycle$", "-test.timeout=90s", "-test.v")
	cmd.Env = env
	// x/os v0.6.0 has no process-group supervisor. Keep the launcher, private
	// bus, child test and native daemon in a group owned by this test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killNativeKeyringSession(cmd) }
	cmd.WaitDelay = 5 * time.Second
	t.Cleanup(func() { _ = killNativeKeyringSession(cmd) })
	return cmd, dir
}

func killNativeKeyringSession(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// TestNativeKeyringSessionCancellation observes the actual private bus, child
// test and daemon before cancellation, then checks every owned process exits.
func TestNativeKeyringSessionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, dir := nativeKeyringSession(t, ctx, "", true)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = killNativeKeyringSession(cmd)
			_ = cmd.Wait()
		}
	}()
	var actors []int
	deadline := time.Now().Add(10 * time.Second)
	for {
		body, err := os.ReadFile(filepath.Join(dir, "actors.json"))
		if err == nil {
			if err := json.Unmarshal(body, &actors); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatalf("native session did not establish its process owners: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(actors) != 3 {
		t.Fatal("native session did not report all three descendants")
	}
	for _, pid := range actors {
		group, err := unix.Getpgid(pid)
		if err != nil || group != cmd.Process.Pid {
			t.Fatalf("native descendant %d escaped its owned process group", pid)
		}
	}
	start := time.Now()
	cancel()
	err := cmd.Wait()
	waited = true
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("private session cancellation did not finish promptly: %v", err)
	}
	for _, pid := range actors {
		for {
			body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			// A reparented zombie is already terminated; its ancestor owns reaping.
			end := bytes.LastIndexByte(body, ')')
			if err == nil && end >= 0 && len(body) > end+2 && body[end+2] == 'Z' {
				break
			}
			if err != nil || time.Since(start) > 2*time.Second {
				t.Fatalf("native descendant %d survived session cancellation", pid)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func testNativeKeyringAuthLifecycle(t *testing.T) {
	t.Helper()
	// The explicit socket and directory checks reject an inherited desktop bus
	// even when someone accidentally leaves the child marker in their environment.
	dir := os.Getenv("JIRA_TEST_KEYRING_ROOT")
	if dir == "" || !filepath.IsAbs(dir) || strings.Split(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), ",")[0] != "unix:path="+filepath.Join(dir, "bus") {
		t.Fatal("session bus does not belong to the private fixture")
	}
	for _, entry := range []struct{ name, directory string }{
		{name: "XDG_DATA_HOME", directory: "data"},
		{name: "XDG_CONFIG_HOME", directory: "config"},
		{name: "XDG_CACHE_HOME", directory: "cache"},
		{name: "XDG_RUNTIME_DIR", directory: "runtime"},
	} {
		path := filepath.Join(dir, entry.directory)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || os.Getenv(entry.name) != path {
			t.Fatalf("invalid private %s directory", entry.name)
		}
	}
	control := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "keyring")
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	daemon := exec.CommandContext(t.Context(), "gnome-keyring-daemon", "--foreground", "--unlock", "--components=secrets", "--control-directory="+control)
	daemon.Stdin = strings.NewReader("synthetic-keyring-password")
	var diagnostics bytes.Buffer
	daemon.Stdout, daemon.Stderr = &diagnostics, &diagnostics
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	readyCtx, readyCancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer readyCancel()
	if out, err := exec.CommandContext(readyCtx, "gdbus", "wait", "--session", "--timeout=10", "org.freedesktop.secrets").CombinedOutput(); err != nil {
		t.Fatalf("private daemon readiness: %v: %s", err, out)
	}
	if os.Getenv("JIRA_TEST_KEYRING_BLOCK") == "1" {
		out, err := exec.CommandContext(readyCtx, "gdbus", "call", "--session", "--dest=org.freedesktop.DBus", "--object-path=/org/freedesktop/DBus", "--method=org.freedesktop.DBus.GetConnectionUnixProcessID", "org.freedesktop.DBus").Output()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(out))
		if len(fields) != 2 || fields[0] != "(uint32" {
			t.Fatal("private bus did not report its process identity")
		}
		busPID, err := strconv.Atoi(strings.TrimRight(fields[1], ",)"))
		if err != nil || busPID <= 0 {
			t.Fatal("private bus process identity is invalid")
		}
		body, err := json.Marshal([]int{os.Getpid(), daemon.Process.Pid, busPID})
		if err != nil {
			t.Fatal(err)
		}
		if err := xos.AtomicWrite(filepath.Join(dir, "actors.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
		select {}
	}
	// A separate native client establishes readiness and proves this bus can
	// write. The marker never contains a Jira credential and has its own key.
	nativeKeyringTool(t, "synthetic-readiness", 0, "store", "--label=CLI native fixture", "service", "jira-cli", "username", "readiness")
	nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "readiness")

	const email = "native-fixture@example.invalid"
	const token = "synthetic-native-fixture-token"
	const rejected = "synthetic-rejected-fixture-token"
	var calls atomic.Int32
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == http.MethodGet && r.URL.Path == "/_edge/tenant_info" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("public discovery received private authentication state")
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		account, candidate, ok := r.BasicAuth()
		if !ok || account != email || candidate != token || revoked.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"errorMessages":["Fixture credential rejected"]}`)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"native:fixture","displayName":"Native fixture","active":true}`)
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "selected.toml")
	if err := os.WriteFile(configPath, []byte("base_url = '"+server.URL+"'\nemail = '"+email+"'\nkeyring = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=true", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env = append(env, name+"="+os.Getenv(name))
	}
	invoke := func(t *testing.T, input string, wantExit int, args ...string) json.RawMessage {
		t.Helper()
		argv := append([]string{"--config", configPath, "--no-input", "--output=json"}, args...)
		stdout, stderr, exit := runExecutableIOInDirStarted(t, os.Getenv("JIRA_TEST_KEYRING_BINARY"), t.TempDir(), env, strings.NewReader(input), nil, nil, argv...)
		if strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, rejected) {
			t.Fatal("credential appeared in command output")
		}
		if exit != wantExit || (wantExit == 0 && stderr != "") {
			t.Fatalf("native command %v: exit=%d want=%d stderr=%q stdout=%q", args, exit, wantExit, stderr, stdout)
		}
		var frame struct {
			OK     bool            `json:"ok"`
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.OK != (wantExit == 0) {
			t.Fatal("native command reported an inconsistent success envelope")
		}
		if wantExit != 0 {
			wantCode := "jira_unauthorized"
			if wantExit == cli.ExitValidation {
				wantCode = "flag_required"
			}
			if len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode {
				t.Fatal("native command returned the wrong error classification")
			}
		}
		return frame.Data
	}
	stored := func(t *testing.T) string {
		t.Helper()
		return strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default"), "\n")
	}
	absent := func(t *testing.T) {
		t.Helper()
		if out := nativeKeyringTool(t, "", 1, "lookup", "service", "jira-cli", "username", "default"); out != "" {
			t.Fatal("native credential still exists")
		}
	}
	absent(t)
	t.Run("login preview stores nothing", func(t *testing.T) {
		before, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var out envelope.AuthLoginOutput
		if err := json.Unmarshal(invoke(t, token, 0, "auth", "login", "--with-token", "--dry-run"), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Verified || out.Credential.Saved || out.Credential.Attempted || out.Credential.Status != "prepared" || out.Settings.Saved || !out.Settings.WouldChange {
			t.Fatal("native preview invented publication or omitted preparation")
		}
		after, err := os.ReadFile(configPath)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("native login preview changed public settings")
		}
		absent(t)
	})
	t.Run("verified login stores complete native record", func(t *testing.T) {
		var out envelope.AuthLoginOutput
		if err := json.Unmarshal(invoke(t, token, 0, "auth", "login", "--with-token"), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Verified || !out.Credential.Saved || !out.Credential.Attempted || !out.Settings.Saved || !out.Settings.Changed {
			t.Fatal("native login lost actual credential/settings acknowledgment")
		}
		var record credentials.Record
		if err := json.Unmarshal([]byte(stored(t)), &record); err != nil {
			t.Fatal(err)
		}
		if record.Version != credentials.Version || record.Site != server.URL || record.Email != email || record.Token != token {
			t.Fatal("native record differs from the verified credential tuple")
		}
		fresh, err := config.Resolve(t.Context(), config.Options{Path: configPath, Environ: func() []string { return nil }}, nil)
		if err != nil || fresh.Config.CachedAccountID() != "native:fixture" || fresh.Config.APIToken != "" {
			t.Fatal("native login has no usable secret-free bound identity", err)
		}
	})
	t.Run("new process authenticates from native record", func(t *testing.T) {
		data := invoke(t, "", 0, "auth", "whoami")
		var out struct {
			User struct {
				AccountID string `json:"accountId"`
			} `json:"user"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		if out.User.AccountID != "native:fixture" {
			t.Fatal("stored native credential did not authenticate the expected account")
		}
	})
	t.Run("rejected login preserves native record", func(t *testing.T) {
		before := stored(t)
		invoke(t, rejected, cli.ExitAuth, "auth", "login", "--with-token")
		if stored(t) != before {
			t.Fatal("rejected login changed the stored credential")
		}
	})
	t.Run("revoked read preserves native record", func(t *testing.T) {
		before := stored(t)
		revoked.Store(true)
		invoke(t, "", cli.ExitAuth, "auth", "whoami")
		revoked.Store(false)
		if stored(t) != before {
			t.Fatal("failed identity read changed the stored credential")
		}
	})
	t.Run("logout preview and refusal preserve native record", func(t *testing.T) {
		before := stored(t)
		invoke(t, "", 0, "auth", "logout", "--dry-run")
		invoke(t, "", cli.ExitValidation, "auth", "logout")
		if stored(t) != before {
			t.Fatal("unconfirmed logout changed the stored credential")
		}
	})
	t.Run("confirmed logout removes native record", func(t *testing.T) {
		invoke(t, "", 0, "auth", "logout", "--yes")
		absent(t)
	})
	t.Run("repeated logout remains absent", func(t *testing.T) {
		invoke(t, "", 0, "auth", "logout", "--yes")
		absent(t)
	})
	if calls.Load() != 6 {
		t.Fatalf("native lifecycle sent %d requests, want 6", calls.Load())
	}
	t.Run("migration and rollback", testNativeKeyringMigrationCommands)
	t.Run("interactive terminal", testNativeKeyringAuthTerminal)
	t.Run("active accessible password context", testNativeAccessiblePasswordContext)
	t.Run("active accessible input context", testNativeAccessibleInputContext)
}

func nativeKeyringTool(t *testing.T, input string, wantExit int, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", args...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	exit := 0
	if err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			t.Fatalf("native keyring client: %v", err)
		}
		exit = exited.ExitCode()
	}
	if exit != wantExit || stderr.Len() != 0 {
		t.Fatalf("native keyring %s: exit=%d want=%d stderr=%q", args[0], exit, wantExit, stderr.String())
	}
	return string(out)
}
