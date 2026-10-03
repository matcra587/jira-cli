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

type transitionReceiptFrame struct {
	OK     bool                            `json:"ok"`
	Data   *envelope.IssueTransitionOutput `json:"data"`
	Errors []envelope.Error                `json:"errors"`
}

func transitionReceiptArgs(mode string, keys ...string) []string {
	args := append([]string{"issue", "transition"}, keys...)
	args = append(args, "--transition=31", "--no-input", "--parallelism=1")
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	if mode == "json" || mode == "compact" {
		args = append(args, "--jq", `error("failed transitions bypass jq")`)
	}
	return args
}

func TestTransitionTotalFailureRetainsOrderedBinaryOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
				var request struct{ Transition struct{ ID string } }
				if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&request) != nil || request.Transition.ID != "31" {
					t.Error("transition changed its native request")
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
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), transitionReceiptArgs(mode, "KEY-1", "KEY-2")...)
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			if exit != cli.ExitAuth || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) || strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatalf("transition changed ordering, classification or replay: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.Contains(stdout, ": moved") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatal("failed transition discarded human outcomes or claimed a move")
				}
				return
			}
			var frame transitionReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || frame.Data.Transition != "31" || len(frame.Data.Results) != 2 {
				t.Fatal("total transition failure discarded its ordered receipt")
			}
			for index, code := range []string{"jira_not_found", "jira_forbidden"} {
				row := frame.Data.Results[index]
				if row.OK || row.Issue.Key != []string{"KEY-1", "KEY-2"}[index] || row.Err == nil || row.Err.Code != code || row.Data == nil || !row.Data.Attempted || row.Data.Applied || row.Data.Transition == nil || row.Data.Transition.ID != "31" {
					t.Fatal("transition failure reordered keys or invented success")
				}
			}
		})
	}
}

func TestTransitionInterruptRetainsCompletedAndQueuedBinaryOutcomes(t *testing.T) {
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
				key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
				var request struct{ Transition struct{ ID string } }
				if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&request) != nil || request.Transition.ID != "31" {
					t.Error("transition interruption changed the write method")
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
					t.Error("queued transition reached the wire after interruption")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnvStarted(t, append(editReceiptEnv(server.URL), "AGENT=1"), func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("transition did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, transitionReceiptArgs(mode, "KEY-1", "KEY-2", "KEY-3")...)
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			if exit != cli.ExitCanceled || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) {
				t.Fatalf("transition interruption changed classification or replayed: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: moved") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || strings.Contains(stdout, "KEY-2: moved") || strings.Contains(stdout, "KEY-3: moved") {
					t.Fatal("interrupted transition discarded human outcomes or invented a move")
				}
				return
			}
			var frame transitionReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Results) != 3 {
				t.Fatal("transition interruption discarded its joined outcomes")
			}
			for index, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[index] {
					t.Fatal("interrupted transition reordered targets")
				}
				if index == 0 {
					if !row.OK || row.Data == nil || row.Err != nil || row.Data.Transition == nil || row.Data.Transition.ID != "31" || !row.Data.Attempted || !row.Data.Applied {
						t.Fatal("transition lost its completed native204 acknowledgement")
					}
				} else if row.OK || row.Err == nil || row.Err.Code != cli.CodeCanceled || (index == 1 && (row.Data == nil || !row.Data.Attempted || row.Data.Applied)) || (index == 2 && row.Data != nil) {
					t.Fatal("transition claimed an unacknowledged move")
				}
			}
		})
	}
}

func TestTransitionUnexpectedSuccessRequiresInspectionWithoutReplay(t *testing.T) {
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
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/transitions" {
					t.Error("unexpected transition acknowledgement changed its target")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), transitionReceiptArgs("json", "KEY-1")...)
			var frame transitionReceiptFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "transition_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "Inspect") || frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK {
				t.Fatalf("unexpected transition success invited replay or claimed a move: exit=%d calls=%d stdout=%s", exit, got, stdout)
			}
		})
	}
}

func TestTransitionUncertainCommentRetainsMoveAndInspectionAdvice(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				requests = append(requests, req.Method+" "+req.URL.Path)
				mu.Unlock()
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/transitions"):
					_, _ = w.Write([]byte(`{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false}]}`))
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/transitions"):
					w.WriteHeader(http.StatusNoContent)
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/comment"):
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Error("compound transition attempted an extra request")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "transition", "KEY-1", "Done", "--comment=Authored comment", "--no-input"}
			if mode != "agent" {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), args...)
			mu.Lock()
			got := slices.Clone(requests)
			mu.Unlock()
			if exit != 0 || !slices.Equal(got, []string{"GET /rest/api/3/issue/KEY-1/transitions", "POST /rest/api/3/issue/KEY-1/transitions", "POST /rest/api/3/issue/KEY-1/comment"}) {
				t.Fatal("uncertain compound comment changed move success or replayed a write")
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: moved to Done") || !strings.Contains(stdout, "Comment not confirmed") || !strings.Contains(strings.ToLower(stdout+stderr), "inspect") {
					t.Fatal("human compound outcome lost its move or invited an unchecked duplicate")
				}
				return
			}
			format := mode
			if mode == "agent" {
				format = "compact"
			}
			var out envelope.IssueTransitionOutput
			if json.Unmarshal(adfData(t, stdout, format, "issue.transition", true), &out) != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil {
				t.Fatal("uncertain compound comment lost the acknowledged move")
			}
			data := out.Results[0].Data
			if !data.Applied || !data.Attempted || data.CommentPosted != envelope.CommentPostedFailed || data.CommentErrorCode != "comment_response_invalid" || !strings.Contains(data.CommentErrorHint, "Inspect") || data.CommentID != "" {
				t.Fatal("uncertain compound comment claimed delivery or lost inspection classification")
			}
		})
	}
}

func TestTransitionBrokenPipeUsesOutputExitWithoutReplay(t *testing.T) {
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
					if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/transitions" {
						t.Error("transition output failure changed its native request")
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
				args := []string{"issue", "transition", "KEY-1", "--transition=31", "--no-input"}
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
					t.Fatalf("transition output failure bypassed exit8 or replayed: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
				}
			})
		}
	}
}

func TestTransitionPreviewAndReadOnlyDoNotContactJira(t *testing.T) {
	t.Parallel()
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "preview", false: "read-only"}[dryRun], func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "transition", "KEY-1", "--transition=31", "--output=json", "--no-input"}
			env := editReceiptEnv(server.URL)
			if dryRun {
				args = append(args, "--dry-run")
			} else {
				env = append(env, "JIRA_READ_ONLY=true")
			}
			stdout, _, exit := runBinaryEnv(t, env, args...)
			var frame transitionReceiptFrame
			if calls.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil {
				t.Fatal("local transition policy contacted Jira or lost its frame")
			}
			if dryRun {
				if exit != 0 || !frame.OK || frame.Data == nil || !frame.Data.DryRun || len(frame.Data.Results) != 1 || frame.Data.Results[0].Data == nil || frame.Data.Results[0].Data.Attempted || frame.Data.Results[0].Data.Applied || frame.Data.Results[0].Data.CommentPosted != "" {
					t.Fatal("preview claimed an attempted or applied transition")
				}
			} else if exit != cli.ExitValidation || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
				t.Fatal("read-only transition was not refused")
			}
		})
	}
}

func TestTransitionFilterFailureRetainsAcknowledgementWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/transitions" {
					t.Error("output filter changed the transition request")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), transitionReceiptArgs(mode, "KEY-1")...)
			var frame transitionReceiptFrame
			if calls.Load() != 1 || exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jq_eval_failed" || frame.Data == nil || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || frame.Data.Results[0].Data == nil || !frame.Data.Results[0].Data.Applied || !frame.Data.Results[0].Data.Attempted {
				t.Fatal("filter failure discarded an acknowledged transition or replayed it")
			}
		})
	}
}
