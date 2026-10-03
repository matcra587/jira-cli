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

const moveNativeInput = `{"sendBulkNotification":false,"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["102","101"],"inferFieldDefaults":false,"future":{"n":9007199254740993,"markup":"<>&","empty":[],"null":null}}}}`

func TestMoveBinaryNativeSubmissionAndMixedOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var posts, polls, identities atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
					identities.Add(1)
					key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
					id := map[string]string{"KEY-1": "101", "KEY-2": "102"}[key]
					if id == "" || r.URL.Query().Get("fields") != "summary" {
						t.Errorf("unexpected identity request: %s", r.URL)
						w.WriteHeader(400)
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"key":%q,"fields":{"summary":"Move fixture"}}`, id, key)
				case r.Method == "POST" && r.URL.Path == "/rest/api/3/bulk/issues/move":
					posts.Add(1)
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != moveNativeInput || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
						t.Errorf("native submission changed: body=%s query=%s err=%v", body, r.URL.RawQuery, err)
						w.WriteHeader(400)
						return
					}
					w.WriteHeader(201)
					_, _ = fmt.Fprint(w, `{"taskId":"100","future":{"n":9007199254740993,"markup":"<>&"}}`)
				case r.Method == "GET" && r.URL.Path == "/rest/api/3/bulk/queue/100":
					if polls.Add(1) == 1 {
						_, _ = fmt.Fprint(w, `{"taskId":"100","status":"RUNNING","progressPercent":100}`)
						return
					}
					_, _ = fmt.Fprint(w, `{"taskId":"100","status":"COMPLETE","totalIssueCount":2,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[101],"failedAccessibleIssues":{"102":["Destination unavailable"]},"progressPercent":100,"created":9007199254740993,"updated":null,"future":{"markup":"<>&"}}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), moveNativeInput, nil, "issue", "move", "KEY-2", "KEY-1", "--json-input=-", "--force", "--wait", "--max-polls=3", "--poll-interval=1ms", "--no-input", "--output="+mode)
			var out envelope.IssueMoveOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "issue.move", true), &out) != nil || posts.Load() != 1 || polls.Load() != 2 || identities.Load() != 2 || !out.Acknowledged || !out.Terminal || !out.BindingsValidated || out.Task == nil || out.Task.ID != "100" || out.Polls != 2 || out.PollLimitReached {
				t.Fatalf("move binary lost job facts: exit=%d stdout=%s stderr=%q posts=%d polls=%d identities=%d", exit, stdout, stderr, posts.Load(), polls.Load(), identities.Load())
			}
			if len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-2" || out.Results[0].OK || out.Results[0].Err.Code != "bulk_operation_failed" || out.Results[1].Issue.Key != "KEY-1" || !out.Results[1].OK || !out.Results[1].Data.Applied || out.Results[1].Data.Outcome != "processed" || string(out.Receipt.Extra["future"]) != `{"n":9007199254740993,"markup":"<>&"}` || string(out.Progress.Created.Value) != "9007199254740993" || !out.Progress.Updated.Null {
				t.Fatalf("mixed/native facts changed: %s", stdout)
			}
		})
	}
}

func TestMoveBinaryLocalPreviewAndAliasesNeedNoCredentials(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_READ_ONLY=true", "AGENT=1", "NO_PROXY=127.0.0.1,localhost"}
	input := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"],"future":{"n":9007199254740993,"markup":"<>&"}}}}`
	for _, route := range []string{"--json-input=-", "--from-json=-", "--body-adf=-"} {
		stdout, stderr, exit := runBinaryEnvInputStarted(t, env, input, nil, "issue", "move", "KEY-1", route, "--dry-run", "--output=json")
		var out envelope.IssueMoveOutput
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "issue.move", true), &out) != nil || !out.DryRun || !out.BindingsValidated || out.RemoteValidated || out.Attempted || out.Acknowledged || out.Task != nil || out.Results[0].Data.Applied || calls.Load() != 0 || !strings.Contains(stdout, "9007199254740993") {
			t.Fatalf("local preview/alias contacted Jira or invented outcomes: route=%s exit=%d stdout=%s stderr=%q calls=%d", route, exit, stdout, stderr, calls.Load())
		}
	}
}

func TestMoveBinaryStatusIsReadOnlyAndNeverSubmits(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/rest/api/3/bulk/queue/100" {
			t.Errorf("status attempted a mutation or another task: %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		_, _ = fmt.Fprint(w, `{"taskId":"100","status":"FAILED","future":{"markup":"<>&"}}`)
	}))
	defer server.Close()
	env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "move", "status", "100", "--wait", "--poll-interval=1ms", "--max-polls=3", "--output=json", "--no-input")
	var out envelope.IssueMoveStatusOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "issue.move.status", true), &out) != nil || calls.Load() != 1 || out.Polls != 1 || !out.Terminal || out.Task.ID != "100" || out.Progress.Status.Value != "FAILED" {
		t.Fatalf("read-only status cannot resume task: exit=%d stdout=%s stderr=%q calls=%d", exit, stdout, stderr, calls.Load())
	}
}

func TestMoveBinaryLocalRefusalsHaveActionableFields(t *testing.T) {
	t.Parallel()
	input := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	for _, tc := range []struct {
		args        []string
		field, code string
	}{
		{[]string{"issue", "move", "KEY-1"}, "json-input", "flag_required"},
		{[]string{"issue", "move", "KEY-1", "--json-input=-"}, "force", "flag_required"},
		{[]string{"issue", "move", "KEY-1", "--json-input=-", "--wait", "--max-polls=0"}, "max-polls", "flag_value_invalid"},
		{[]string{"issue", "move", "KEY-1", "--json-input=-", "--wait", "--poll-interval=0s"}, "poll-interval", "flag_value_invalid"},
		{[]string{"issue", "move", "KEY-1", "--json-input=-", "--max-polls=1"}, "max-polls", "flag_not_applicable"},
		{[]string{"issue", "move", "KEY-1", "--json-input=-", "--poll-interval=1ms"}, "poll-interval", "flag_not_applicable"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			args := append(tc.args, "--output=json", "--no-input")
			stdout, _, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false"}, input, nil, args...)
			var frame struct {
				Errors []struct{ Code, Field string } `json:"errors"`
			}
			if exit != 3 || json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || frame.Errors[0].Field != tc.field {
				t.Fatalf("local refusal lacks actionable field: args=%v exit=%d stdout=%s", args, exit, stdout)
			}
		})
	}
}

func TestMoveBinaryOpaqueTaskIDCannotBecomeAFlag(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/rest/api/3/bulk/queue/--help" {
			t.Errorf("opaque task became a flag or different request: %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		_, _ = fmt.Fprint(w, `{"taskId":"--help","status":"FAILED"}`)
	}))
	defer server.Close()
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "--output=json", "--no-input", "issue", "move", "status", "--wait", "--", "--help")
	var out envelope.IssueMoveStatusOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "issue.move.status", true), &out) != nil || calls.Load() != 1 || out.Task.ID != "--help" || !out.Terminal {
		t.Fatalf("recovery recipe cannot read an opaque task: exit=%d stdout=%s stderr=%q calls=%d", exit, stdout, stderr, calls.Load())
	}
}

func TestMoveBinaryHumanRefusalDoesNotClaimAcceptance(t *testing.T) {
	t.Parallel()
	input := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
	stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false"}, input, nil, "issue", "move", "KEY-1", "--json-input=-", "--output=human", "--no-input")
	if exit != 3 || !strings.Contains(stdout, "not submitted") || strings.Contains(stdout, "accepted") || !strings.Contains(stderr, "force") {
		t.Fatalf("human refusal invented a move acknowledgement: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestMoveBinaryFailuresRetainAcceptanceWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"write-503", "unusable-201", "contradictory-progress", "null-failure-reason"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			var posts, polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /rest/api/3/issue/KEY-1":
					_, _ = fmt.Fprint(w, `{"id":"101","key":"KEY-1"}`)
				case "POST /rest/api/3/bulk/issues/move":
					posts.Add(1)
					if failure == "write-503" {
						w.WriteHeader(503)
						_, _ = fmt.Fprint(w, `{"errorMessages":["Unavailable"]}`)
						return
					}
					w.WriteHeader(201)
					if failure == "unusable-201" {
						_, _ = fmt.Fprint(w, `{"taskId":".."}`)
						return
					}
					_, _ = fmt.Fprint(w, `{"taskId":"100"}`)
				case "GET /rest/api/3/bulk/queue/100":
					if polls.Add(1) == 1 && failure == "contradictory-progress" {
						_, _ = fmt.Fprint(w, `{"taskId":"100","status":"RUNNING","progressPercent":40}`)
						return
					}
					if failure == "null-failure-reason" {
						_, _ = fmt.Fprint(w, `{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[],"failedAccessibleIssues":{"101":null}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"taskId":"OTHER","status":"RUNNING"}`)
					}
				default:
					t.Errorf("failure recovery made an unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			input := `{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}}}`
			stdout, _, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), input, nil, "issue", "move", "KEY-1", "--json-input=-", "--force", "--wait", "--max-polls=3", "--poll-interval=1ms", "--output=json", "--no-input")
			var frame struct {
				OK     bool                     `json:"ok"`
				Data   envelope.IssueMoveOutput `json:"data"`
				Errors []struct{ Code string }  `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || posts.Load() != 1 || !frame.Data.Attempted || len(frame.Data.Results) != 1 {
				t.Fatalf("failure lost receipt or replayed a move: %s exit=%d posts=%d stdout=%s", failure, exit, posts.Load(), stdout)
			}
			out := frame.Data
			switch failure {
			case "write-503":
				if exit != 5 || frame.OK || out.Acknowledged || out.Task != nil || polls.Load() != 0 {
					t.Fatal("unsafe write was replayed or fabricated acknowledgement")
				}
			case "unusable-201":
				if exit != 0 || !frame.OK || !out.Acknowledged || out.ReceiptError == nil || out.Task != nil || polls.Load() != 0 || out.Results[0].Data.Applied {
					t.Fatal("unusable receipt lost acknowledgement or invented application")
				}
			case "contradictory-progress":
				if exit != 5 || frame.OK || !out.Acknowledged || out.Task == nil || out.Task.ID != "100" || polls.Load() != 2 || out.Progress == nil || out.Progress.Status.Value != "RUNNING" || out.Progress.ProgressPercent.Value != 40 || out.Terminal || len(frame.Errors) != 1 || frame.Errors[0].Code != "bulk_operation_response_invalid" {
					t.Fatal("later contradictory progress erased accepted task or prior observation")
				}
			case "null-failure-reason":
				if exit != 5 || frame.OK || !out.Acknowledged || !out.Terminal || polls.Load() != 1 || out.Results[0].OK || out.Results[0].Err.Code != "bulk_operation_failed" {
					t.Fatal("explicit native failure was lost because its reasons were null")
				}
			}
		})
	}
}
