//go:build linux

package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestPipedTokenRootHonorsCommandContext(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		deadline bool
		wantExit int
		wantCode string
	}{
		{name: "cancel", wantExit: cli.ExitCanceled, wantCode: "canceled"},
		{name: "deadline", deadline: true, wantExit: cli.ExitTimeout, wantCode: "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, exit := runTokenInputContextProcess(t, server.URL, tc.deadline)
			if exit != tc.wantExit {
				t.Fatalf("token input exit=%d, want %d", exit, tc.wantExit)
			}
			var frame struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(stdout, &frame); err != nil {
				t.Fatalf("token input polluted machine stdout: %v", err)
			}
			if frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.wantCode {
				t.Fatalf("wrong token input context frame: %+v", frame)
			}
			if calls.Load() != 0 {
				t.Fatal("canceled token input contacted Jira")
			}
		})
	}
}

func runTokenInputContextProcess(t *testing.T, site string, deadline bool) ([]byte, int) {
	t.Helper()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()  //nolint:errcheck // test owns both pipe ends
	defer writer.Close() //nolint:errcheck // test owns both pipe ends
	descriptor := int(input.Fd())
	before, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Keep EOF withheld after a real inherited read has consumed this prefix.
	if _, err := io.WriteString(writer, "synthetic-token"); err != nil {
		t.Fatal(err)
	}
	control, trigger, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close() //nolint:errcheck // test owns cancellation pipe
	defer trigger.Close() //nolint:errcheck // test owns cancellation pipe
	home := t.TempDir()
	configPath := filepath.Join(home, "selected.toml")
	if err := os.WriteFile(configPath, []byte("keyring = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestPromptContextProcess$", "--",
		"--config", configPath, "--output=json", "--no-input", "auth", "login",
		"--site", site, "--email", "input-fixture@example.invalid", "--with-token", "--dry-run")
	child.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=dumb", "NO_COLOR=1",
		"JIRA_KEYRING=false", "JIRA_NO_UPDATE_CHECK=1", "JIRA_TEST_PROMPT_PROCESS=1",
	}
	if deadline {
		child.Env = append(child.Env, "JIRA_TEST_PROMPT_DEADLINE=1")
	}
	var stdout, stderr bytes.Buffer
	child.Stdin, child.Stdout, child.Stderr, child.ExtraFiles = input, &stdout, &stderr, []*os.File{control}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- child.Wait() }()
	waited := false
	defer func() {
		if !waited {
			cancel()
			<-finished
		}
	}()
	observed := time.NewTimer(time.Second)
	defer observed.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		queued, err := unix.IoctlGetInt(descriptor, unix.TIOCINQ)
		if err != nil {
			t.Fatal(err)
		}
		if queued == 0 {
			break
		}
		select {
		case err := <-finished:
			waited = true
			t.Fatalf("child exited before reading token prefix: %v", err)
		case <-observed.C:
			t.Fatal("did not observe an actual inherited token read")
		case <-poll.C:
		}
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
			t.Fatal("piped token ignored command context and required forced child cleanup")
		}
		var exitError *exec.ExitError
		if err != nil && !errors.As(err, &exitError) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("piped token ignored command context and required forced child cleanup")
	}
	after, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil || after != before {
		t.Fatal("token input changed borrowed pipe flags")
	}
	return stdout.Bytes(), child.ProcessState.ExitCode()
}
