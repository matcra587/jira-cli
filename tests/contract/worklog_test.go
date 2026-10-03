package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

const worklogADF = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"<>&"}]}]}`

func TestWorklogBinaryNativeWritesAndIncompleteReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			// The shared serializer compacts JSON formatting. String spelling,
			// large integers, null, zero and empty values remain unchanged.
			const properties = `[{"key":"future","value":{"n":9007199254740993,"empty":[],"null":null,"false":false,"markup":"<>&"}}]`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				want := `{"comment":` + worklogADF + `,"properties":` + properties + `,"started":"2026-01-02T12:30:00.000+0000","timeSpentSeconds":60}`
				if err != nil || r.Method != "POST" || r.URL.RawQuery != "notifyUsers=false" || string(body) != want {
					t.Errorf("worklog method/query/native bytes changed: %s %s %s %v", r.Method, r.URL, body, err)
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(201)
				switch r.URL.Path {
				case "/rest/api/3/issue/KEY-1/worklog":
					_, _ = fmt.Fprint(w, `{"id":"100","author":null,"timeSpentSeconds":60,"comment":`+worklogADF+`,"future":{"n":9007199254740993,"markup":"<>&"}}`)
				case "/rest/api/3/issue/KEY-2/worklog":
					_, _ = fmt.Fprint(w, `{}`)
				default:
					t.Errorf("unowned target: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			input := `{"timeSpentSeconds":60,"started":"2026-01-02T12:30:00Z","comment":` + worklogADF + `,"properties":` + properties + `}`
			stdout, stderr, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), input, nil, "worklog", "add", "KEY-1:2", "--json-input=-", "--notify=false", "--parallelism=1", "--no-input", "--output="+mode)
			var out envelope.WorklogAddOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "worklog.add", true), &out) != nil || calls.Load() != 2 || len(out.Results) != 2 || out.DryRun || out.Notify || out.BodyRoute != "adf" {
				t.Fatalf("worklog binary lost native creation receipts: exit=%d %s stderr=%q calls=%d", exit, stdout, stderr, calls.Load())
			}
			first, second := out.Results[0], out.Results[1]
			if first.Issue.Key != "KEY-1" || !first.OK || !first.Data.Applied || first.Data.Worklog.ID != "100" || !first.Data.Worklog.Fields.Author.Null || string(first.Data.Worklog.Fields.Extra["future"]) != `{"n":9007199254740993,"markup":"<>&"}` || second.Issue.Key != "KEY-2" || !second.OK || !second.Data.Applied || second.Data.Worklog != nil || second.Data.ReceiptError.Code != "worklog_response_invalid" {
				t.Fatalf("acknowledged creation or native record facts changed: %s", stdout)
			}
			if mode == "json" {
				var f frame
				var warning envelope.Warning
				if json.Unmarshal([]byte(stdout), &f) != nil || len(f.Warnings) != 1 || json.Unmarshal(f.Warnings[0], &warning) != nil || warning.Type != "worklog_receipt_incomplete" || warning.Lossy {
					t.Fatalf("incomplete receipt lacks a truthful warning: %s", stdout)
				}
			}
		})
	}
}

func TestWorklogBinaryPreviewNeedsNoCredentials(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_READ_ONLY=true", "JIRA_WORKDAY_SECONDS=21600", "AGENT=1"}
	stdout, stderr, exit := runBinaryEnv(t, env, "worklog", "add", "KEY-2,KEY-1:2", "--time-spent=1d", "--started=2026-01-02", "--markdown=investigated", "--dry-run", "--output=json")
	var out envelope.WorklogAddOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "worklog.add", true), &out) != nil || !out.DryRun || out.WorkdaySeconds != 21600 || !strings.Contains(string(out.Payload), `"timeSpentSeconds":21600`) || out.BodyRoute != "markdown" || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-2" || out.Results[1].Issue.Key != "KEY-1" || requests.Load() != 0 {
		t.Fatalf("local preview required authentication, ignored workday or contacted Jira: exit=%d %s stderr=%q requests=%d", exit, stdout, stderr, requests.Load())
	}
	for _, row := range out.Results {
		if !row.OK || row.Data.Applied || row.Data.Worklog != nil || row.Data.ReceiptError != nil {
			t.Fatalf("preview invented server state: %+v", row)
		}
	}
}

func TestWorklogBinaryReadOnlyAndFailedPostsNeverReplay(t *testing.T) {
	t.Parallel()
	for _, readOnly := range []bool{true, false} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" {
					t.Error("failed write caused verification read")
				}
				w.WriteHeader(503)
			}))
			defer server.Close()
			env := append(editReceiptEnv(server.URL), fmt.Sprintf("JIRA_READ_ONLY=%t", readOnly))
			stdout, _, exit := runBinaryEnv(t, env, "worklog", "add", "KEY-1", "--time-spent=1m", "--started=2026-01-02", "--no-input", "--output=json")
			var f frame
			wantCode, wantExit, wantCalls := "jira_server_error", 5, int32(1)
			if readOnly {
				wantCode, wantExit, wantCalls = "read_only_blocked", 3, 0
			}
			var failure struct {
				Code string `json:"code"`
			}
			if json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != wantCode || exit != wantExit || calls.Load() != wantCalls {
				t.Fatalf("write refusal replayed or lost classification: exit=%d %s calls=%d", exit, stdout, calls.Load())
			}
			var out envelope.WorklogAddOutput
			if json.Unmarshal(f.Data, &out) != nil || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Err.Code != wantCode || out.Results[0].Data != nil || len(out.Payload) == 0 {
				t.Fatalf("failed write lost ordered request receipt: %s", stdout)
			}
		})
	}
}

func TestWorklogBinaryListRetainsPrefixAndNativeRecord(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Query().Get("maxResults") != "100" {
					t.Error("worklog list request changed")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == "/rest/api/3/issue/KEY-2/worklog" {
					w.WriteHeader(404)
					return
				}
				if r.URL.Path != "/rest/api/3/issue/KEY-1/worklog" {
					t.Error("unknown list target")
					w.WriteHeader(400)
					return
				}
				if r.URL.Query().Get("startAt") == "0" {
					_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":100,"total":2,"worklogs":[{"id":"100","author":null,"timeSpentSeconds":0,"comment":`+worklogADF+`,"future":{"n":9007199254740993}}]}`)
				} else {
					_, _ = fmt.Fprint(w, `{"startAt":1,"maxResults":100,"total":2,"worklogs":[{"id":"100"}]}`)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "worklog", "list", "KEY-1:2", "--all", "--parallelism=1", "--output="+mode)
			var out envelope.WorklogListOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "worklog.list", true), &out) != nil || len(out.Results) != 2 || calls.Load() != 3 {
				t.Fatalf("read partial outcome lost: exit=%d %s stderr=%q calls=%d", exit, stdout, stderr, calls.Load())
			}
			first, second := out.Results[0], out.Results[1]
			if first.Issue.Key != "KEY-1" || !first.OK || first.Data.Complete || !first.Data.Truncated || first.Data.TruncatedReason != "read_failed" || first.Data.ReadError.Code != "worklog_response_invalid" || first.Data.Pagination.Returned != 1 || first.Data.Pagination.Total != 2 || len(first.Data.Worklogs) != 1 || string(first.Data.Worklogs[0].Fields.Comment.Value) != worklogADF || !first.Data.Worklogs[0].Fields.Author.Null || string(first.Data.Worklogs[0].Fields.Extra["future"]) != `{"n":9007199254740993}` || second.Issue.Key != "KEY-2" || second.OK || second.Err.Code != "jira_not_found" {
				t.Fatalf("native prefix, duplicate refusal or key failure lost: %s", stdout)
			}
		})
	}
}

func TestWorklogBinaryInvalidInputsRefuseBeforeRequests(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, args := range [][]string{
		{"worklog", "add", "KEY-1", "--time-spent=0m"},
		{"worklog", "add", "KEY-1:1001", "--time-spent=1m"},
		{"worklog", "list", "KEY-1", "--all", "--start=0"},
		{"worklog", "list", "KEY-1", "--limit=101"},
		{"worklog", "list", "KEY-1", "--parallelism=17"},
		{"worklog", "add", "KEY-1", "--json-input=-", "--markdown=conflict"},
	} {
		stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, "--output=json")...)
		var f frame
		if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 {
			t.Fatalf("invalid worklog invocation accepted or mistyped: %v exit=%d %s", args, exit, stdout)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input contacted Jira %d times", calls.Load())
	}
}

func TestWorklogBinaryHumanPageOffersClearResume(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/rest/api/3/issue/KEY-1/worklog" || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "1" {
			t.Errorf("unexpected worklog page request: %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":1,"total":2,"worklogs":[{"id":"100","timeSpentSeconds":60}]}`)
	}))
	defer server.Close()
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "worklog", "list", "KEY-1", "--limit=1", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "More worklogs remain.") || !strings.Contains(stdout, "--start=1") || strings.Contains(stdout, "()") {
		t.Fatalf("ordinary pagination lacks a clear resume hint: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}
