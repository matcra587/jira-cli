//go:build linux

package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
)

// TestPromptContextProcess runs the real composition entry in an owned child.
// The parent's control pipe cancels only after the form has rendered.
func TestPromptContextProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_PROMPT_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(20)
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if os.Getenv("JIRA_TEST_PROMPT_DEADLINE") == "1" {
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
		control := os.NewFile(3, "prompt-cancel-control")
		go func() {
			var trigger [1]byte
			_, _ = control.Read(trigger[:])
			cancel()
			_ = control.Close()
		}()
	}
	defer cancel()
	// This child owns the default transport. Count requests without opening a
	// listener so a canceled prompt cannot pass merely because networking failed.
	var calls atomic.Int32
	http.DefaultTransport = promptRequestTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("prompt fixture forbids HTTP requests")
	})
	exit := Run(ctx, os.Args[separator+1:])
	if calls.Load() != 0 {
		os.Exit(21)
	}
	os.Exit(exit)
}

func TestRichPromptsHonorCommandContext(t *testing.T) {
	const site = "https://example.invalid"
	for _, operation := range []struct {
		name   string
		marker string
		args   []string
	}{
		{name: "login", marker: "API token", args: []string{"auth", "login", "--site", site, "--email", "prompt-fixture@example.invalid"}},
		{name: "logout", marker: "Remove the stored token", args: []string{"auth", "logout"}},
		{name: "comment delete", marker: "Delete comment", args: []string{"issue", "comment", "delete", "KEY-1", "10042"}},
	} {
		for _, deadline := range []bool{false, true} {
			name, wantExit, wantCode := "cancel", cli.ExitCanceled, "canceled"
			if deadline {
				name, wantExit, wantCode = "deadline", cli.ExitTimeout, "timeout"
			}
			t.Run(operation.name+"/"+name, func(t *testing.T) {
				stdout, exit := runPromptContextProcess(t, site, operation.marker, deadline, operation.args)
				if exit == 21 {
					t.Fatal("canceled prompt contacted Jira")
				}
				if exit != wantExit {
					t.Fatalf("context exit=%d, want %d", exit, wantExit)
				}
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if err := json.Unmarshal(stdout, &frame); err != nil {
					t.Fatalf("prompt polluted machine stdout: %v", err)
				}
				if frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode {
					t.Fatalf("wrong context error classification: %+v", frame)
				}
			})
		}
	}
}

type promptRequestTransport func(*http.Request) (*http.Response, error)

func (f promptRequestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func runPromptContextProcess(t *testing.T, site, marker string, deadline bool, args []string) ([]byte, int) {
	t.Helper()
	master, slave := openPromptTerminal(t)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 120}); err != nil {
		t.Fatal(err)
	}
	before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	control, trigger, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	defer func() { _ = trigger.Close() }()
	home := t.TempDir()
	configPath := filepath.Join(home, "selected.toml")
	if err := os.WriteFile(configPath, []byte("base_url = '"+site+"'\nemail = 'prompt-fixture@example.invalid'\nkeyring = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestPromptContextProcess$", "--", "--config", configPath, "--output=json"}, args...)
	child := exec.CommandContext(ctx, executable, argv...)
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=xterm-256color", "NO_COLOR=1", "JIRA_KEYRING=false", "JIRA_API_TOKEN=synthetic-prompt-token", "JIRA_NO_UPDATE_CHECK=1", "JIRA_TEST_PROMPT_PROCESS=1"}
	if deadline {
		child.Env = append(child.Env, "JIRA_TEST_PROMPT_DEADLINE=1")
	}
	child.Stdin, child.Stderr, child.ExtraFiles = slave, slave, []*os.File{control}
	var stdout bytes.Buffer
	child.Stdout = &stdout
	var mu sync.Mutex
	var screen bytes.Buffer
	var terminalReadErr error
	changed, drained := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(drained)
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
				mu.Lock()
				terminalReadErr = readErr
				mu.Unlock()
				return
			}
		}
	}()
	defer func() {
		_ = slave.Close()
		_ = master.Close()
		<-drained
	}()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		finished <- child.Wait()
		close(exited)
	}()
	waited := false
	defer func() {
		if !waited {
			cancel()
			<-finished
		}
	}()
	observed := time.NewTimer(time.Second)
	defer observed.Stop()
	observation := promptObservation{
		marker: marker,
		snapshot: func() string {
			mu.Lock()
			defer mu.Unlock()
			return screen.String()
		},
		changed: changed,
		expired: observed.C,
		exited:  exited,
		drained: drained,
	}
	if err := observation.wait(); err != nil {
		var waitErr error
		forcedCleanup := false
		select {
		case waitErr = <-finished:
		default:
			forcedCleanup = true
			cancel()
			waitErr = <-finished
		}
		waited = true
		mu.Lock()
		captured, readErr := screen.String(), terminalReadErr
		mu.Unlock()
		// Wait joins the stdout copier before reading its buffer.
		t.Fatalf("%v (deadline=%t, forced_cleanup=%t, wait=%v, parent_context=%v, reader=%v, screen=%q, stdout=%q)", err, deadline, forcedCleanup, waitErr, ctx.Err(), readErr, captured, stdout.String())
	}
	if !deadline {
		if _, err := io.WriteString(trigger, "c"); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-finished:
		waited = true
		if ctx.Err() != nil {
			t.Fatal("prompt ignored command context and required forced cleanup")
		}
		var exitError *exec.ExitError
		if err != nil && !errors.As(err, &exitError) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("prompt ignored command context and required forced cleanup")
	}
	after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil || *after != *before {
		t.Fatal("prompt did not restore borrowed terminal modes")
	}
	return stdout.Bytes(), child.ProcessState.ExitCode()
}

type promptObservation struct {
	marker   string
	snapshot func() string
	changed  <-chan struct{}
	expired  <-chan time.Time
	exited   <-chan struct{}
	drained  <-chan struct{}
}

func (p promptObservation) wait() error {
	exited := p.exited
	reason := "observation timeout"
	for {
		if strings.Contains(p.snapshot(), p.marker) {
			return nil
		}
		select {
		case <-p.changed:
			continue
		case <-p.expired:
			return p.finish(reason)
		case <-exited:
			// Wait does not join the terminal reader; queued output may remain.
			reason = "child exited (observation timeout)"
			exited = nil
		case <-p.drained:
			return p.finish("terminal reader stopped")
		}
	}
}

func (p promptObservation) finish(reason string) error {
	// A marker may have arrived since the check preceding select.
	if strings.Contains(p.snapshot(), p.marker) {
		return nil
	}
	return fmt.Errorf("did not observe %q: %s", p.marker, reason)
}

func openPromptTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(descriptor), "prompt-terminal")
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
