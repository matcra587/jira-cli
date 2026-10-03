package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type cloneReceiptFrame struct {
	OK     bool                       `json:"ok"`
	Data   *envelope.IssueCloneOutput `json:"data"`
	Errors []envelope.Error           `json:"errors"`
}

func TestIssueCloneUncertainCreationOutranksMissingSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, code string }{
		{name: "unusable identity", body: `{}`, code: "issue_create_response_invalid"},
		{name: "failed followup", body: `{"id":"9002","key":"NEW-2","transition":{"status":400}}`, code: "issue_create_followup_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-1" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-2" {
					_, _ = w.Write([]byte(`{"key":"KEY-2","fields":{"summary":"KEY-2","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`))
					return
				}
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
					t.Error("clone changed its native dispatch")
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "clone", "KEY-1", "KEY-2", "--parallelism=1", "--output=json", "--no-input")
			var frame cloneReceiptFrame
			if exit != cli.ExitServer || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || frame.Data == nil || len(frame.Data.Results) != 2 || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != "jira_not_found" || frame.Data.Results[1].Err == nil || frame.Data.Results[1].Err.Code != tc.code || frame.Data.Results[1].Data == nil || !frame.Data.Results[1].Data.Applied {
				t.Fatal("missing source hid a more severe uncertain creation or failed followup")
			}
		})
	}
}

func cloneReceiptArgs(mode string) []string {
	args := []string{"issue", "clone", "KEY-1", "KEY-2", "KEY-3", "--parallelism=1", "--no-input"}
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	if mode == "json" || mode == "compact" {
		args = append(args, "--jq", `error("failed clone bypasses jq")`)
	}
	return args
}

func TestIssueCloneInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
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
				mu.Lock()
				calls = append(calls, req.Method+" "+req.URL.Path)
				mu.Unlock()
				if req.Method == http.MethodGet {
					key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
					if !slices.Contains([]string{"KEY-1", "KEY-2"}, key) {
						t.Error("clone sent a queued source read after interruption")
					}
					_, _ = w.Write([]byte(`{"id":"10001","key":"` + key + `","fields":{"summary":"` + key + `","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`))
					return
				}
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
					t.Error("clone changed its native create request")
				}
				var payload struct {
					Fields struct {
						Summary string `json:"summary"`
					} `json:"fields"`
				}
				if json.NewDecoder(req.Body).Decode(&payload) != nil {
					t.Error("clone did not send native fields")
				}
				switch payload.Fields.Summary {
				case "KEY-1":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"9001","key":"NEW-1","future":{"zero":0}}`))
				case "KEY-2":
					once.Do(func() { close(started) })
					<-req.Context().Done()
				default:
					t.Error("clone created a queued source after interruption")
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
					t.Fatal("clone did not reach independent interruption stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, cloneReceiptArgs(mode)...)
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			want := []string{"GET /rest/api/3/issue/KEY-1", "POST /rest/api/3/issue", "GET /rest/api/3/issue/KEY-2", "POST /rest/api/3/issue"}
			if exit != cli.ExitCanceled || !slices.Equal(got, want) {
				t.Fatalf("clone changed cancellation classification or replayed: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1 → NEW-1") || !strings.Contains(stdout, "KEY-2: ") || !strings.Contains(stdout, "KEY-3: ") || strings.Contains(stdout, "KEY-2 →") || strings.Contains(stdout, "KEY-3 →") {
					t.Fatal("interrupted clone discarded human outcomes or invented identities")
				}
				return
			}
			var frame cloneReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || len(frame.Data.Results) != 3 || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled {
				t.Fatal("interrupted clone discarded ordered outcomes")
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
					t.Fatal("interrupted clone reordered source keys")
				}
				if i == 0 {
					if !row.OK || row.Data == nil || !row.Data.Applied || row.Data.Clone == nil || row.Data.Clone.ID != "9001" || row.Data.Clone.Key != "NEW-1" || row.Err != nil || !strings.Contains(string(row.Data.Receipt), `"future"`) {
						t.Fatal("interrupted clone lost its native acknowledgement, identity or response")
					}
				} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
					t.Fatal("interrupted clone invented a creation acknowledgement")
				}
			}
		})
	}
}

func TestIssueCloneTotalFailureRetainsNativeCreationAcknowledgements(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var created []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
					_, _ = w.Write([]byte(`{"key":"` + key + `","fields":{"summary":"` + key + `","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`))
					return
				}
				var payload struct {
					Fields struct {
						Summary string `json:"summary"`
					} `json:"fields"`
				}
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" || json.NewDecoder(req.Body).Decode(&payload) != nil {
					t.Error("clone changed its native request")
				}
				mu.Lock()
				created = append(created, payload.Fields.Summary)
				mu.Unlock()
				w.WriteHeader(http.StatusCreated)
				switch payload.Fields.Summary {
				case "KEY-1":
					_, _ = w.Write([]byte(`{}`))
				case "KEY-2":
					_, _ = w.Write([]byte(`{"id":"9002","key":"NEW-2","transition":{"status":400}}`))
				case "KEY-3":
					_, _ = w.Write([]byte(`null`))
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, cloneReceiptArgs(mode)...)
			mu.Lock()
			got := slices.Clone(created)
			mu.Unlock()
			if exit != cli.ExitServer || !slices.Equal(got, []string{"KEY-1", "KEY-2", "KEY-3"}) {
				t.Fatalf("failed clone replayed or changed classification: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: Jira acknowledged creation") || !strings.Contains(stdout, "KEY-2 → NEW-2") || !strings.Contains(stdout, "transition returned HTTP 400") || !strings.Contains(stdout, "KEY-3: Jira acknowledged creation") {
					t.Fatal("failed clone lost acknowledged creation or followup facts in human output")
				}
				return
			}
			var frame cloneReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || len(frame.Data.Results) != 3 || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_create_response_invalid" {
				t.Fatal("clone discarded all-failed creation receipts")
			}
			for i, row := range frame.Data.Results {
				code := "issue_create_response_invalid"
				if i == 1 {
					code = "issue_create_followup_failed"
				}
				if row.OK || row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] || row.Data == nil || !row.Data.Applied || row.Err == nil || row.Err.Code != code || (row.Data.Clone != nil) != (i == 1) {
					t.Fatal("clone fabricated or dropped a native acknowledgement or usable identity")
				}
			}
		})
	}
}
