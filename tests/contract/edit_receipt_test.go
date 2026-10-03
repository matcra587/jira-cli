package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type editReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	} `json:"meta"`
	Data   envelope.IssueEditOutput `json:"data"`
	Errors []envelope.Error         `json:"errors"`
}

func TestIssueEditTotalFailureRetainsReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var requests []string
			const hostile = "fixture pause \x1b[2Jdisplay\x1b[0m\a"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				if req.Method != http.MethodPut || req.URL.Query().Get("notifyUsers") != "false" || !bytes.Contains(body, []byte("9007199254740993")) {
					t.Error("edit changed its method, notification request or native payload")
				}
				mu.Lock()
				requests = append(requests, req.URL.Path)
				mu.Unlock()
				if req.URL.Path == "/rest/api/3/issue/KEY-123" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"errorMessages":["fixture issue absent"]}`)
					return
				}
				if req.URL.Path != "/rest/api/3/issue/KEY-124" {
					t.Error("edit addressed an unexpected resource")
				}
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				if err := json.NewEncoder(w).Encode(map[string]any{"errorMessages": []string{hostile}, "errors": map[string]string{"summary": "fixture detail"}}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"issue", "edit", "KEY-123", "KEY-124", "--json-input", editReceiptInput(t), "--notify=false", "--parallelism=1", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("must never filter a failed command")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != cli.ExitRateLimit {
				t.Fatalf("edit exit=%d, want rate limit; stderr=%q stdout=%q", exit, stderr, stdout)
			}
			mu.Lock()
			got := append([]string(nil), requests...)
			mu.Unlock()
			if len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-123" || got[1] != "/rest/api/3/issue/KEY-124" {
				t.Fatalf("failed edits were reordered, omitted or replayed: %v", got)
			}
			if strings.ContainsAny(stderr, "\x1b\a") {
				t.Error("failure diagnostics forwarded terminal controls from Jira")
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-123") || !strings.Contains(stdout, "KEY-124") || !strings.Contains(stdout, "fixture pause display") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatalf("human failure lost its ordered, sanitized outcomes: %q", stdout)
				}
				return
			}
			var f editReceiptFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatalf("failure is not one full frame: %v; stdout=%q", err, stdout)
			}
			if f.OK || f.Meta.Command != "issue.edit" || f.Meta.ExitCode == nil || *f.Meta.ExitCode != cli.ExitRateLimit || len(f.Errors) != 1 || f.Errors[0].Code != "jira_rate_limited" || f.Errors[0].Attempts != 1 || f.Errors[0].RetryAfterSeconds != 1 || f.Errors[0].Field != "summary" || !strings.Contains(f.Errors[0].Message, hostile) || !strings.Contains(f.Errors[0].Hint, "verify mutation outcomes before retrying") {
				t.Fatal("retained failure changed the original classification, recovery or transport facts")
			}
			if f.Data.DryRun || f.Data.Notify || !bytes.Contains(f.Data.Payload, []byte("9007199254740993")) || len(f.Data.Results) != 2 || f.Data.Results[0].Issue.Key != "KEY-123" || f.Data.Results[1].Issue.Key != "KEY-124" || f.Data.Results[0].OK || f.Data.Results[1].OK || f.Data.Results[0].Err == nil || f.Data.Results[0].Err.Code != "jira_not_found" || f.Data.Results[1].Err == nil || f.Data.Results[1].Err.Code != "jira_rate_limited" {
				t.Fatal("total failure discarded its native request or per-key verdicts")
			}
		})
	}
}

func TestIssueEditPreconditionFailureRetainsSeverityAndReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, err := io.ReadAll(req.Body)
				if err != nil || req.Method != http.MethodPut || !bytes.Contains(body, []byte("9007199254740993")) {
					t.Error("edit changed its method or native payload")
				}
				mu.Lock()
				requests = append(requests, req.URL.Path)
				mu.Unlock()
				switch req.URL.Path {
				case "/rest/api/3/issue/KEY-123":
					w.WriteHeader(http.StatusPreconditionFailed)
					_, _ = io.WriteString(w, `{"errorMessages":["fixture resource changed"]}`)
				case "/rest/api/3/issue/KEY-124":
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"errorMessages":["fixture issue absent"]}`)
				default:
					t.Error("edit addressed an unexpected resource")
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "edit", "KEY-123", "KEY-124", "--json-input", editReceiptInput(t), "--parallelism=1", "--no-input", "--output="+mode)
			mu.Lock()
			got := append([]string(nil), requests...)
			mu.Unlock()
			if exit != cli.ExitValidation || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-123" || got[1] != "/rest/api/3/issue/KEY-124" {
				t.Fatalf("conditional refusal lost severity or replayed writes: exit=%d requests=%v stderr=%q", exit, got, stderr)
			}
			var f editReceiptFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if f.OK || f.Meta.ExitCode == nil || *f.Meta.ExitCode != cli.ExitValidation || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeJiraPreconditionFailed || !strings.Contains(f.Errors[0].Hint, "reconcile edits") {
				t.Fatal("first refusal did not retain its classification and recovery hint")
			}
			if !bytes.Contains(f.Data.Payload, []byte("9007199254740993")) || len(f.Data.Results) != 2 || f.Data.Results[0].Issue.Key != "KEY-123" || f.Data.Results[1].Issue.Key != "KEY-124" || f.Data.Results[0].OK || f.Data.Results[1].OK || f.Data.Results[0].Err == nil || f.Data.Results[1].Err == nil || f.Data.Results[0].Err.Code != cli.CodeJiraPreconditionFailed || f.Data.Results[1].Err.Code != "jira_not_found" {
				t.Fatal("conditional refusal discarded native input or ordered per-key outcomes")
			}
		})
	}
}

func TestIssueEditInterruptKeepsCompletedOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			secondStarted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				if req.Method != http.MethodPut {
					t.Error("edit used an unexpected method")
				}
				switch req.URL.Path {
				case "/rest/api/3/issue/KEY-123":
					w.WriteHeader(http.StatusNoContent)
				case "/rest/api/3/issue/KEY-124":
					close(secondStarted)
					<-req.Context().Done()
				default:
					t.Error("edit addressed an unexpected resource")
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
				select {
				case <-secondStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("binary did not start the second edit")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, "issue", "edit", "KEY-123", "KEY-124", "--summary", "receipt", "--parallelism=1", "--no-input", "--output="+mode)
			if exit != cli.ExitCanceled {
				t.Fatalf("interrupted edit exit=%d, want canceled; stderr=%q stdout=%q", exit, stderr, stdout)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-123: updated.") || !strings.Contains(stdout, "KEY-124:") {
					t.Fatalf("human cancellation lost its completed edit: %q", stdout)
				}
				return
			}
			var f editReceiptFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatalf("interrupted command lost its frame: %v; stdout=%q", err, stdout)
			}
			if f.OK || f.Meta.ExitCode == nil || *f.Meta.ExitCode != cli.ExitCanceled || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeCanceled || len(f.Data.Results) != 2 || f.Data.Results[0].Issue.Key != "KEY-123" || !f.Data.Results[0].OK || f.Data.Results[0].Data == nil || f.Data.Results[1].Issue.Key != "KEY-124" || f.Data.Results[1].OK || f.Data.Results[1].Err == nil || f.Data.Results[1].Err.Code != cli.CodeCanceled {
				t.Fatal("cancellation discarded or misclassified a completed write")
			}
		})
	}
}

func TestIssueEditPrevalidationHasNoReceiptOrRequest(t *testing.T) {
	t.Parallel()
	server := newExampleJira(t)
	stdout, stderr, exit := runBinaryEnv(t, server.env(), "issue", "edit", "KEY-123", "--summary", "receipt", "--parallelism=0", "--output=json")
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitValidation || f.OK || string(f.Data) != "null" || len(server.requests()) != 0 {
		t.Fatalf("invalid edit published an operation receipt or contacted Jira: exit=%d stderr=%q", exit, stderr)
	}
}

func editReceiptInput(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"fields":{"summary":"receipt <&>","customfield_99999":{"unknown":9007199254740993}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func editReceiptEnv(base string) []string {
	return []string{"JIRA_BASE_URL=" + base, "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=example-fixture-token", "JIRA_KEYRING=false", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "NO_PROXY=127.0.0.1,localhost"}
}
