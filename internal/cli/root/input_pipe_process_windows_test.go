//go:build windows

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
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/cli"
)

// The public HTTP timeout is not a command deadline. This isolated helper gives
// the production Run path a real caller deadline without replacing its owners.
func TestWindowsPipeCommandProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_WINDOWS_PIPE_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(20)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	os.Exit(Run(ctx, os.Args[separator+1:]))
}

func TestWindowsProductionPipeInputDeadline(t *testing.T) {
	for _, route := range []string{"token", "native payload", "markdown", "prepared input", "prepared metadata"} {
		t.Run(route, func(t *testing.T) {
			read, write := windowsRegisteredInputPipe(t)
			defer read.Close()  //nolint:errcheck // test owns both pipe ends
			defer write.Close() //nolint:errcheck // test owns both pipe ends
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			fixture := t.TempDir()
			configPath := filepath.Join(fixture, "selected.toml")
			if err := os.WriteFile(configPath, []byte("keyring = false\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			metadata, payload := filepath.Join(fixture, "metadata.json"), filepath.Join(fixture, "input.json")
			for _, path := range []string{metadata, payload} {
				if err := os.WriteFile(path, []byte(`{"fields":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-test.run=^TestWindowsPipeCommandProcess$", "--", "--config=" + configPath, "--output=json", "--no-input"}
			prefix := "unfinished native input"
			switch route {
			case "token":
				args = append(args, "auth", "login", "--site="+server.URL, "--email=fixture@example.invalid", "--with-token", "--dry-run")
			case "native payload":
				args = append(args, "issue", "edit", "KEY-123", "--json-input=-", "--dry-run")
				prefix = `{"fields":`
			case "markdown":
				args = append(args, "issue", "comment", "add", "KEY-123", "--markdown-file=-", "--dry-run")
			case "prepared input":
				args = append(args, "issue", "input", "prepare", "--input=-", "--metadata="+metadata)
				prefix = `{"fields":`
			case "prepared metadata":
				args = append(args, "issue", "input", "prepare", "--input="+payload, "--metadata=-")
				prefix = `{"fields":`
			}
			if _, err := write.WriteString(prefix); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, args...)
			child.Dir = fixture
			child.Env = []string{
				"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
				"WINDIR=" + os.Getenv("WINDIR"), "TEMP=" + fixture, "TMP=" + fixture,
				"USERPROFILE=" + fixture, "HOME=" + fixture,
				"APPDATA=" + filepath.Join(fixture, "appdata"), "LOCALAPPDATA=" + filepath.Join(fixture, "localappdata"),
				"XDG_CONFIG_HOME=" + filepath.Join(fixture, "config"), "XDG_CACHE_HOME=" + filepath.Join(fixture, "cache"),
				"XDG_DATA_HOME=" + filepath.Join(fixture, "data"), "TERM=dumb", "NO_COLOR=1",
				"JIRA_KEYRING=false", "JIRA_NO_UPDATE_CHECK=1", "JIRA_TEST_WINDOWS_PIPE_PROCESS=1",
			}
			var stdout, stderr bytes.Buffer
			child.Stdin, child.Stdout, child.Stderr = read, &stdout, &stderr
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
			for windowsQueuedInput(t, windows.Handle(read.Fd())) != 0 {
				select {
				case err := <-finished:
					waited = true
					t.Fatalf("production command exited before native input consumption: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				case <-observed.C:
					t.Fatal("production native input read did not enter")
				case <-poll.C:
				}
			}
			select {
			case err := <-finished:
				waited = true
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || child.ProcessState.ExitCode() != cli.ExitTimeout || ctx.Err() != nil {
					t.Fatalf("production native pipe deadline required forced cleanup or lost exit: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
			case <-time.After(4 * time.Second):
				t.Fatal("production native pipe deadline waited for writer closure")
			}
			var frame struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "timeout" || requests.Load() != 0 {
				t.Fatalf("production native input lost typed timeout or contacted Jira: stdout=%q stderr=%q requests=%d", stdout.String(), stderr.String(), requests.Load())
			}
			if _, err := write.WriteString("reuse"); err != nil {
				t.Fatal(err)
			}
			value := make([]byte, len("reuse"))
			if _, err := io.ReadFull(read, value); err != nil || string(value) != "reuse" {
				t.Fatalf("production child damaged caller's native input: %q %v", value, err)
			}
		})
	}
}
