package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCommentDeleteUnexpectedSuccessRequiresInspection(t *testing.T) {
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
				if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/issue/KEY-1/comment/10042" {
					t.Error("comment deletion changed its explicit resource")
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "delete", "KEY-1", "10042", "--force", "--no-input", "--output=json")
			var frame struct {
				OK     bool             `json:"ok"`
				Data   json.RawMessage  `json:"data"`
				Errors []envelope.Error `json:"errors"`
			}
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") {
				t.Fatalf("unexpected comment deletion acknowledgement changed uncertainty or replayed work: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestCommentDeleteInterruptDoesNotInventAcknowledgement(t *testing.T) {
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
				if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/issue/KEY-1/comment/10042" {
					t.Error("interrupted comment deletion changed its exact resource")
				}
				once.Do(func() { close(started) })
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"issue", "comment", "delete", "KEY-1", "10042", "--force", "--no-input"}
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
					t.Fatal("comment deletion did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, args...)
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitCanceled || got != 1 || strings.Contains(stdout, "Deleted comment") {
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
