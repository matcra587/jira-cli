//go:build windows

package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/internal/cli"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestWindowsRegisteredPipeInputHonorsContext(t *testing.T) {
	for _, route := range []string{"token", "native payload", "markdown", "prepared input", "prepared metadata"} {
		for _, deadline := range []bool{false, true} {
			name := route + "/cancel"
			if deadline {
				name = route + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				read, write := windowsRegisteredInputPipe(t)
				defer read.Close()  //nolint:errcheck // test owns its pipe ends
				defer write.Close() //nolint:errcheck // test owns its pipe ends
				fixture := t.TempDir()
				metadata := filepath.Join(fixture, "metadata.json")
				payload := filepath.Join(fixture, "input.json")
				if err := os.WriteFile(metadata, []byte(`{"fields":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(payload, []byte(`{"fields":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"--output=json", "--no-input"}
				prefix := "unfinished native input"
				switch route {
				case "token":
					args = append(args, "auth", "login", "--site=https://example.invalid", "--email=fixture@example.invalid", "--with-token", "--dry-run")
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
				var clients, providers atomic.Int32
				deps := testDeps(t)
				deps.LoginSite = func(context.Context, authcmd.LoginDefaults) (authcmd.SiteInfo, error) {
					return authcmd.SiteInfo{BaseURL: "https://example.invalid", Email: "fixture@example.invalid"}, nil
				}
				deps.JiraClient = func(context.Context) (core.JiraClient, error) {
					clients.Add(1)
					return nil, errors.New("unexpected Jira factory entry")
				}
				deps.Credentials = func() (core.CredentialStore, error) {
					providers.Add(1)
					return nil, errors.New("unexpected credential factory entry")
				}
				deps.LoginCredentials = func(context.Context, core.CredentialLocation) (core.CredentialStore, error) {
					providers.Add(1)
					return nil, errors.New("unexpected login provider entry")
				}
				deps.JiraClientFor = func(string, string, string, string) (core.JiraClient, error) {
					clients.Add(1)
					return nil, errors.New("unexpected authenticated client entry")
				}
				deps.IdentitySaveClientFor = deps.JiraClientFor
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				program.Root.SetIn(read)
				ctx, cancel := context.WithCancel(t.Context())
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
				}
				defer cancel()
				finished := make(chan int, 1)
				go func() { finished <- execute(ctx, program, sink, args) }()
				// Peek independently observes consumption by the registered owner.
				// Keep the writer open, so cancellation cannot pass through EOF.
				observed := time.NewTimer(time.Second)
				defer observed.Stop()
				poll := time.NewTicker(time.Millisecond)
				defer poll.Stop()
				for windowsQueuedInput(t, windows.Handle(read.Fd())) != 0 {
					select {
					case exit := <-finished:
						t.Fatalf("registered command exited before consuming pipe input: %d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
					case <-observed.C:
						_ = write.Close()
						<-finished
						t.Fatal("registered native input read did not enter")
					case <-poll.C:
					}
				}
				if !deadline {
					cancel()
				}
				var exit int
				select {
				case exit = <-finished:
				case <-time.After(3 * time.Second):
					_ = write.Close()
					<-finished
					t.Fatal("registered canceled pipe input waited for writer closure")
				}
				wantExit, wantCode := cli.ExitCanceled, "canceled"
				if deadline {
					wantExit, wantCode = cli.ExitTimeout, "timeout"
				}
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if exit != wantExit || json.Unmarshal(stdout.Bytes(), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode || clients.Load() != 0 || providers.Load() != 0 {
					t.Fatalf("native input context lost typed refusal or entered adapters: exit=%d stdout=%q stderr=%q clients=%d providers=%d", exit, stdout.String(), stderr.String(), clients.Load(), providers.Load())
				}
				if _, err := write.WriteString("reuse"); err != nil {
					t.Fatal(err)
				}
				reused := make([]byte, len("reuse"))
				if _, err := io.ReadFull(read, reused); err != nil || string(reused) != "reuse" {
					t.Fatalf("registered cancellation damaged borrowed input: %q %v", reused, err)
				}
			})
		}
	}
}

func windowsQueuedInput(t *testing.T, handle windows.Handle) uint32 {
	t.Helper()
	var available uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")
	result, _, err := call.Call(uintptr(handle), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0) //nolint:gosec // native output points to this live uint32 for the synchronous call
	if result == 0 {
		t.Fatal("cannot observe owned native pipe input:", err)
	}
	return available
}

// A synchronous PeekNamedPipe can itself wait behind a pending synchronous
// read on the same file object. Overlapped input keeps this observer independent.
func windowsRegisteredInputPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\jira-registered-input-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		t.Fatal(err)
	}
	var overlapped windows.Overlapped
	if err = windows.ConnectNamedPipe(server, &overlapped); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = windows.CloseHandle(server)
		_ = windows.CloseHandle(client)
		t.Fatal(err)
	}
	return os.NewFile(uintptr(server), "registered native input"), os.NewFile(uintptr(client), "registered native writer")
}
