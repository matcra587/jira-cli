package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type commentReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data   *envelope.IssueCommentAddOutput `json:"data"`
	Errors []envelope.Error                `json:"errors"`
}

func TestCommentPublishedHelpProjectionsExecute(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		methods = append(methods, req.Method)
		mu.Unlock()
		if req.Method != http.MethodPost && req.Method != http.MethodPut {
			t.Error("comment help projection performed an unrelated read or write")
		}
		if req.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte(`{"id":"10042","updated":"2026-01-02T03:04:05.000+0000"}`))
	}))
	t.Cleanup(server.Close)
	for _, verb := range []string{"add", "edit"} {
		help, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", verb, "--help")
		if exit != 0 {
			t.Fatalf("comment help did not render: %d %q", exit, stderr)
		}
		found := 0
		for _, line := range strings.Split(help, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(args, "--jq") {
				continue
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if exit != 0 || strings.TrimSpace(stdout) == "" || strings.TrimSpace(stdout) == "null" {
				t.Fatalf("literal comment help projection failed: verb=%s exit=%d stdout=%q stderr=%q", verb, exit, stdout, stderr)
			}
			if verb == "add" {
				decoder := json.NewDecoder(strings.NewReader(stdout))
				for _, key := range []string{"KEY-123", "KEY-124"} {
					var receipt struct {
						Key string `json:"key"`
						ID  string `json:"id"`
					}
					if decoder.Decode(&receipt) != nil || receipt.Key != key || receipt.ID != "10042" {
						t.Fatal("published add projection lost ordered per-target comment identities")
					}
				}
				var trailing json.RawMessage
				if !errors.Is(decoder.Decode(&trailing), io.EOF) {
					t.Fatal("published add projection produced an extra outcome")
				}
			} else if !strings.Contains(stdout, "2026-01-02T03:04:05.000+0000") {
				t.Fatal("published edit projection did not report the stored update timestamp")
			}
			found++
		}
		if found != 1 {
			t.Fatal("published comment help omitted its executable jq recipe")
		}
	}
	mu.Lock()
	got := append([]string(nil), methods...)
	mu.Unlock()
	if len(got) != 3 || got[0] != http.MethodPost || got[1] != http.MethodPost || got[2] != http.MethodPut {
		t.Fatal("published help projections changed their request count or verbs")
	}
}

func commentReceiptArgs(mode string, keys ...string) []string {
	args := append([]string{"issue", "comment", "add"}, keys...)
	args = append(args, "--markdown", "Authored **comment**.", "--parallelism=1", "--no-input")
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func TestCommentWriteUnexpectedSuccessRequiresInspection(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "edit"} {
		statuses := []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent}
		if verb == "edit" {
			statuses[0] = http.StatusCreated
		}
		for _, status := range statuses {
			t.Run(verb+"/"+http.StatusText(status), func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					w.WriteHeader(status)
					if status != http.StatusNoContent {
						_, _ = w.Write([]byte(`{"id":"10042"}`))
					}
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "comment", verb, "KEY-1"}
				if verb == "edit" {
					args = append(args, "10042")
				}
				args = append(args, "--markdown", "Authored body", "--output=json", "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				mu.Lock()
				got := calls
				mu.Unlock()
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitServer || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") || got != 1 {
					t.Fatalf("unexpected success status misreported a safe refusal or replayed the write: exit=%d calls=%d stderr=%q", exit, got, stderr)
				}
			})
		}
	}
}

func TestCommentWriteLocalRefusalPublishesNoReceipt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, verb := range []string{"add", "edit"} {
		args := []string{"issue", "comment", verb, "KEY-1"}
		if verb == "edit" {
			args = append(args, "10042")
		}
		stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, "--output=json", "--no-input")...)
		var frame struct {
			OK     bool             `json:"ok"`
			Data   json.RawMessage  `json:"data"`
			Errors []envelope.Error `json:"errors"`
		}
		if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitValidation || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagRequired || string(frame.Data) != "null" || calls.Load() != 0 {
			t.Fatalf("missing comment body invented a receipt or reached HTTP: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
		}
	}
}

func TestCommentFailureReceiptBrokenPipeUsesOutputExitContract(t *testing.T) {
	t.Parallel()
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNotFound)
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
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), jiraBinary(t), commentReceiptArgs(mode, "KEY-1")...)
			cmd.Dir = dir
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb"}, editReceiptEnv(server.URL)...)
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = writer, &stderr
			err = cmd.Run()
			var failure *exec.ExitError
			if !errors.As(err, &failure) || failure.ExitCode() != cli.ExitOutput || calls.Load() != 1 {
				t.Fatalf("failed comment receipt bypassed exit8 or replayed work: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
			}
		})
	}
}

func TestCommentEditRejectsAnotherCommentReceipt(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPut || req.URL.Path != "/rest/api/3/issue/KEY-1/comment/10042" {
					t.Error("comment edit addressed another resource")
				}
				_, _ = w.Write([]byte(`{"id":"10043"}`))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "edit", "KEY-1", "10042", "--markdown", "Authored body", "--output="+mode, "--no-input")
			if exit != cli.ExitServer || calls.Load() != 1 || strings.Contains(stdout, "comment edited") {
				t.Fatalf("comment edit published another identity or replayed work: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				return
			}
			var frame struct {
				OK     bool             `json:"ok"`
				Data   json.RawMessage  `json:"data"`
				Errors []envelope.Error `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" {
				t.Fatal("comment edit fabricated stored state from another comment's receipt")
			}
		})
	}
}

func TestCommentAddTotalFailureRetainsOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost {
					t.Error("comment failure changed the request method")
				}
				mu.Lock()
				calls = append(calls, req.URL.Path)
				mu.Unlock()
				if strings.Contains(req.URL.Path, "KEY-1") {
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
			args := commentReceiptArgs(mode, "KEY-1", "KEY-2")
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("failed comments bypass jq")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitAuth || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-1/comment" || got[1] != "/rest/api/3/issue/KEY-2/comment" {
				t.Fatalf("comment total failure changed severity, targets or replay: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatal("comment diagnostics forwarded terminal controls")
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatal("comment failure discarded sanitized human outcomes")
				}
				return
			}
			var frame commentReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || frame.Data.DryRun || len(frame.Data.Body) == 0 || frame.Data.BodyRoute != "markdown" || len(frame.Data.Results) != 2 {
				t.Fatal("comment total failure discarded its authored request or ordered receipts")
			}
			for i, code := range []string{"jira_not_found", "jira_forbidden"} {
				row := frame.Data.Results[i]
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code || row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] {
					t.Fatal("comment total failure changed target verdicts or fabricated a comment")
				}
			}
		})
	}
}

func TestCommentUncertainReceiptOutranksMissingTarget(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.URL.Path == "/rest/api/3/issue/KEY-1/comment" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), commentReceiptArgs("json", "KEY-1", "KEY-2")...)
	var frame commentReceiptFrame
	if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitServer || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" || frame.Data == nil || len(frame.Data.Results) != 2 || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != "jira_not_found" || frame.Data.Results[1].Err == nil || frame.Data.Results[1].Err.Code != "comment_response_invalid" || calls.Load() != 2 {
		t.Fatalf("mixed comment failure hid the uncertain stored-write outcome: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
	}
}

func TestCommentAddInterruptRetainsCreatedAndQueuedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var once sync.Once
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls = append(calls, req.URL.Path)
				mu.Unlock()
				switch req.URL.Path {
				case "/rest/api/3/issue/KEY-1/comment":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"10042","body":{"type":"doc","version":1,"content":[]}}`))
				case "/rest/api/3/issue/KEY-2/comment":
					once.Do(func() { close(started) })
					<-req.Context().Done()
				default:
					t.Error("interrupted comment batch requested a queued target")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("comment batch did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, commentReceiptArgs(mode, "KEY-1", "KEY-2", "KEY-3")...)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-1/comment" || got[1] != "/rest/api/3/issue/KEY-2/comment" {
				t.Fatalf("interrupted comment batch lost cancellation or replayed work: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || !strings.Contains(stdout, "comment added") {
					t.Fatal("interrupted comment batch discarded human outcomes")
				}
				return
			}
			var frame commentReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Body) == 0 || len(frame.Data.Results) != 3 {
				t.Fatal("interrupted comment batch discarded its authored request and joined outcomes")
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
					t.Fatal("interrupted comment batch reordered targets")
				}
				if i == 0 {
					if !row.OK || row.Data == nil || row.Data.Comment == nil || row.Data.Comment.ID != "10042" || row.Err != nil {
						t.Fatal("interrupted comment batch discarded the created comment identity")
					}
				} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
					t.Fatal("interrupted comment batch invented a completed comment")
				}
			}
		})
	}
}

func TestCommentWriteRejectsUnusableAcknowledgements(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "edit"} {
		for name, response := range map[string]string{
			"null": "null", "missing": `{}`, "null identity": `{"id":null}`,
			"empty identity": `{"id":""}`, "blank identity": `{"id":" \t"}`,
			"malformed": `{`, "trailing object": `{"id":"10042"} {}`, "trailing garbage": `{"id":"10042"} broken`,
		} {
			t.Run(verb+"/"+name, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					method, path, status := http.MethodPost, "/rest/api/3/issue/KEY-1/comment", http.StatusCreated
					if verb == "edit" {
						method, path, status = http.MethodPut, path+"/10042", http.StatusOK
					}
					if req.Method != method || req.URL.Path != path {
						t.Error("comment acknowledgement test changed the target or method")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(response))
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "comment", verb, "KEY-1"}
				if verb == "edit" {
					args = append(args, "10042")
				}
				args = append(args, "--markdown", "Authored body", "--output=json", "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				mu.Lock()
				got := calls
				mu.Unlock()
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitServer || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") || got != 1 {
					t.Fatalf("unusable comment acknowledgement published success or encouraged replay: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
				}
			})
		}
	}
}
