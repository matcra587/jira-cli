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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueDeleteBrokenPipeUsesOutputExitWithoutReplay(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	t.Parallel()
	for _, status := range []int{http.StatusNoContent, http.StatusForbidden} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(http.StatusText(status)+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/issue/KEY-1" || req.URL.Query().Get("deleteSubtasks") != "false" {
						t.Error("issue deletion output failure changed its native request")
					}
					w.WriteHeader(status)
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
				args := []string{"issue", "delete", "KEY-1", "--force", "--no-input"}
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
					t.Fatalf("issue deletion output failure bypassed exit8 or replayed: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
				}
			})
		}
	}
}

type issueDeleteReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data   *envelope.IssueDeleteOutput `json:"data"`
	Errors []envelope.Error            `json:"errors"`
}

func issueDeleteReceiptArgs(mode string, keys ...string) []string {
	args := append([]string{"issue", "delete"}, keys...)
	args = append(args, "--force", "--no-input", "--parallelism=1", "--cascade")
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	if mode == "json" || mode == "compact" {
		args = append(args, "--jq", `error("failed deletions bypass jq")`)
	}
	return args
}

func TestIssueDeleteTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
				if req.Method != http.MethodDelete || req.URL.Query().Get("deleteSubtasks") != "true" {
					t.Error("issue deletion changed its native method or explicit cascade")
				}
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				if key == "KEY-1" {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = w.Write([]byte(`{"errorMessages":["fixture denied\u001b[31m\u0007"]}`))
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, issueDeleteReceiptArgs(mode, "KEY-1", "KEY-2")...)
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			if exit != cli.ExitAuth || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) || strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatalf("issue deletion changed failure classification, ordering or replay: exit=%d calls=%v stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.Contains(stdout, ": deleted.") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatal("issue deletion discarded sanitized failed human outcomes")
				}
				return
			}
			var frame issueDeleteReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || !frame.Data.DeleteSubtasks || frame.Data.DryRun || len(frame.Data.Results) != 2 {
				t.Fatal("issue deletion discarded its total failure outcomes or cascade")
			}
			for i, code := range []string{"jira_not_found", "jira_forbidden"} {
				row := frame.Data.Results[i]
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatal("issue deletion reordered failures or invented an acknowledgement")
				}
			}
		})
	}
}

func TestIssueDeleteInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
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
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
				if req.Method != http.MethodDelete || req.URL.Query().Get("deleteSubtasks") != "true" {
					t.Error("interrupted issue deletion changed its explicit request")
				}
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				switch key {
				case "KEY-1":
					w.WriteHeader(http.StatusNoContent)
				case "KEY-2":
					once.Do(func() { close(started) })
					<-req.Context().Done()
				default:
					t.Error("interrupted issue deletion sent a queued request")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("issue deletion did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, issueDeleteReceiptArgs(mode, "KEY-1", "KEY-2", "KEY-3")...)
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			if exit != cli.ExitCanceled || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) {
				t.Fatalf("interrupted issue deletion changed classification or replayed: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: deleted.") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || strings.Contains(stdout, "KEY-2: deleted.") || strings.Contains(stdout, "KEY-3: deleted.") {
					t.Fatal("interrupted issue deletion discarded human outcomes or claimed unacknowledged deletions")
				}
				return
			}
			var frame issueDeleteReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || !frame.Data.DeleteSubtasks || frame.Data.DryRun || len(frame.Data.Results) != 3 {
				t.Fatal("interrupted issue deletion discarded joined outcomes or cascade")
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
					t.Fatal("interrupted issue deletion reordered targets")
				}
				if i == 0 {
					if !row.OK || row.Data == nil || row.Err != nil {
						t.Fatal("interrupted issue deletion lost its native204 acknowledgement")
					}
				} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
					t.Fatal("interrupted issue deletion invented an acknowledgement")
				}
			}
		})
	}
}

func TestIssueDeleteUnexpectedSuccessRequiresInspection(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
				if req.Method != http.MethodDelete || req.URL.Query().Get("deleteSubtasks") != "false" {
					t.Error("issue deletion changed its explicit noncascade request")
				}
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				if key == "KEY-1" {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(status)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "delete", "KEY-1", "KEY-2", "--force", "--no-input", "--parallelism=1", "--output=json")
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			var frame issueDeleteReceiptFrame
			if exit != cli.ExitServer || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_delete_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") || frame.Data == nil || frame.Data.DeleteSubtasks || len(frame.Data.Results) != 2 {
				t.Fatalf("uncertain issue deletion lost its protocol classification or replayed: exit=%d calls=%v stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
			for i, code := range []string{"jira_not_found", "issue_delete_response_invalid"} {
				row := frame.Data.Results[i]
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code || row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] {
					t.Fatal("uncertain issue deletion fabricated a receipt or reordered targets")
				}
			}
		})
	}
}
