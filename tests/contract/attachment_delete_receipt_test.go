package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAttachmentReadAndDeleteBrokenPipeUsesOutputExitContract(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	t.Parallel()
	for _, operation := range []string{"list", "delete"} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if operation == "list" {
						if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1" {
							t.Error("failed attachment read changed its native request")
						}
						w.WriteHeader(http.StatusForbidden)
					} else {
						if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/attachment/10042" {
							t.Error("attachment deletion changed its native request")
						}
						w.WriteHeader(http.StatusNoContent)
					}
				}))
				t.Cleanup(server.Close)
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = writer.Close() }()
				args := []string{"issue", "attachment", operation}
				if operation == "list" {
					args = append(args, "KEY-1")
				} else {
					args = append(args, "10042", "--force")
				}
				args = append(args, "--no-input")
				env := editReceiptEnv(server.URL)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				dir := t.TempDir()
				cmd := exec.CommandContext(t.Context(), jiraBinary(t), args...)
				cmd.Dir = dir
				cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb"}, env...)
				var stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = writer, &stderr
				err = cmd.Run()
				failure, ok := errors.AsType[*exec.ExitError](err)
				if !ok || failure.ExitCode() != cli.ExitOutput || calls.Load() != 1 {
					t.Fatalf("attachment output failure bypassed exit8 or replayed work: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
				}
			})
		}
	}
}

func TestAttachmentDeleteUnexpectedSuccessRequiresInspection(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/attachment/10042" {
					t.Error("attachment deletion changed its explicit resource")
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "delete", "10042", "--force", "--no-input", "--output=json")
			var frame struct {
				OK     bool             `json:"ok"`
				Data   json.RawMessage  `json:"data"`
				Errors []envelope.Error `json:"errors"`
			}
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != "attachment_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") {
				t.Fatalf("unexpected attachment deletion acknowledgement changed uncertainty or replayed work: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestAttachmentDeleteInterruptDoesNotInventAcknowledgement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var once sync.Once
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/attachment/10042" {
					t.Error("interrupted attachment deletion changed its exact resource")
				}
				once.Do(func() { close(started) })
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"issue", "attachment", "delete", "10042", "--force", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("unacknowledged deletion bypasses jq")`)
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("attachment deletion did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, args...)
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitCanceled || got != 1 || strings.Contains(stdout, "Deleted attachment") {
				t.Fatalf("unacknowledged deletion claimed completion or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
			if mode == "human" {
				return
			}
			var frame struct {
				OK     bool             `json:"ok"`
				Data   json.RawMessage  `json:"data"`
				Errors []envelope.Error `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled {
				t.Fatal("interrupted deletion invented an acknowledgement or lost its full failure frame")
			}
		})
	}
}
