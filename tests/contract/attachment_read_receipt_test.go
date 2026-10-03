package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type attachmentReadFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data   *envelope.IssueAttachmentListOutput `json:"data"`
	Errors []envelope.Error                    `json:"errors"`
}

func TestAttachmentListUnexpectedSuccessUsesProtocolTaxonomy(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusCreated, http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1" || req.URL.Query().Get("fields") != "attachment" {
					t.Error("attachment read changed its explicit narrowed request")
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "list", "KEY-1", "--output=json", "--no-input")
			var frame attachmentReadFrame
			if exit != cli.ExitServer || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeAttachmentResponseInvalid || frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != cli.CodeAttachmentResponseInvalid {
				t.Fatalf("unexpected attachment read acknowledgement changed protocol classification or fabricated resources: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestAttachmentListRefusesUnusableNativeObservations(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null issue":                   `null`,
		"missing identity":             `{"fields":{"attachment":[]}}`,
		"null identity":                `{"id":null,"key":null,"fields":{"attachment":[]}}`,
		"blank identity":               `{"id":" ","key":"KEY-1","fields":{"attachment":[]}}`,
		"missing fields":               `{"id":"10001","key":"KEY-1"}`,
		"null fields":                  `{"id":"10001","key":"KEY-1","fields":null}`,
		"missing array":                `{"id":"10001","key":"KEY-1","fields":{}}`,
		"null array":                   `{"id":"10001","key":"KEY-1","fields":{"attachment":null}}`,
		"missing attachment identity":  `{"id":"10001","key":"KEY-1","fields":{"attachment":[{"filename":"report.txt"}]}}`,
		"null attachment identity":     `{"id":"10001","key":"KEY-1","fields":{"attachment":[{"id":null}]}}`,
		"blank attachment identity":    `{"id":"10001","key":"KEY-1","fields":{"attachment":[{"id":" "}]}}`,
		"repeated attachment identity": `{"id":"10001","key":"KEY-1","fields":{"attachment":[{"id":"10042"},{"id":"10042"}]}}`,
		"trailing object":              `{"id":"10001","key":"KEY-1","fields":{"attachment":[]}} {}`,
		"trailing garbage":             `{"id":"10001","key":"KEY-1","fields":{"attachment":[]}} x`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1" || req.URL.Query().Get("fields") != "attachment" {
					t.Error("attachment read changed its explicit narrowed request")
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "list", "KEY-1", "--output=json", "--no-input")
			var frame attachmentReadFrame
			if exit != cli.ExitServer || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeAttachmentResponseInvalid {
				t.Fatalf("unusable native attachment observation became ordinary success or changed classification: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil || frame.Data.Results[0].Issue.Key != "KEY-1" || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != cli.CodeAttachmentResponseInvalid {
				t.Fatal("unusable attachment observation invented resources or lost its keyed failure")
			}
		})
	}
}

func TestAttachmentListAcceptsCanonicalIssueAndExplicitEmptyArray(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/OLD-1" || req.URL.Query().Get("fields") != "attachment" {
			t.Error("attachment read changed its requested issue alias")
		}
		_, _ = w.Write([]byte(`{"id":"10001","key":"NEW-2","fields":{"attachment":[]},"extra":{"large":9007199254740993}}`))
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "list", "OLD-1", "--output=json", "--no-input")
	var frame attachmentReadFrame
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || frame.Data.Results[0].Issue.ID != "10001" || frame.Data.Results[0].Issue.Key != "NEW-2" || frame.Data.Results[0].Data == nil || frame.Data.Results[0].Data.Attachments == nil || len(frame.Data.Results[0].Data.Attachments) != 0 {
		t.Fatalf("attachment list refused canonical issue identity or changed explicit empty array: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestAttachmentListTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Query().Get("fields") != "attachment" {
					t.Error("attachment list changed its narrowed issue read")
				}
				key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
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
			args := []string{"issue", "attachment", "list", "KEY-1", "KEY-2", "--parallelism=1", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("failed attachment reads bypass jq")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitAuth || len(got) != 2 || got[0] != "KEY-1" || got[1] != "KEY-2" || strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatalf("attachment read changed requests, taxonomy or sanitization: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatal("attachment read failure discarded its sanitized human outcomes")
				}
				return
			}
			var frame attachmentReadFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || len(frame.Data.Results) != 2 {
				t.Fatal("attachment read failure discarded ordered outcomes or its full failure frame")
			}
			for i, code := range []string{"jira_not_found", "jira_forbidden"} {
				row := frame.Data.Results[i]
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatal("attachment read failure fabricated data or changed request order")
				}
			}
		})
	}
}

func TestAttachmentListInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
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
				if req.Method != http.MethodGet || req.URL.Query().Get("fields") != "attachment" {
					t.Error("interrupted attachment list changed its read boundary")
				}
				key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				switch key {
				case "KEY-1":
					_, _ = w.Write([]byte(`{"id":"10001","key":"KEY-1","fields":{"attachment":[{"id":"10042","filename":"report.txt","size":3,"mimeType":"text/plain"}]}}`))
				case "KEY-2":
					once.Do(func() { close(started) })
					<-req.Context().Done()
				default:
					t.Error("interrupted attachment list requested a queued issue")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"issue", "attachment", "list", "KEY-1", "KEY-2", "KEY-3", "--parallelism=1", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("attachment list did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, args...)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "KEY-1" || got[1] != "KEY-2" {
				t.Fatalf("attachment list changed cancellation or restarted reads: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || !strings.Contains(stdout, "report.txt") {
					t.Fatal("interrupted attachment read discarded its observed attachment or target verdicts")
				}
				return
			}
			var frame attachmentReadFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Results) != 3 {
				t.Fatal("interrupted attachment list discarded joined outcomes or its full failure frame")
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
					t.Fatal("interrupted attachment list changed requested order")
				}
				if i == 0 {
					if !row.OK || row.Err != nil || row.Data == nil || len(row.Data.Attachments) != 1 || row.Data.Attachments[0].ID != "10042" || row.Data.Attachments[0].Filename != "report.txt" || row.Data.Attachments[0].Size != 3 {
						t.Fatal("interrupted attachment list changed observed metadata")
					}
				} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
					t.Fatal("interrupted attachment list invented a completed read")
				}
			}
		})
	}
}
