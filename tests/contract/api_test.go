package contract

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	apicmd "github.com/matcra587/jira-cli/internal/cli/api"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type apiFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command string `json:"command"`
	} `json:"meta"`
	Data   json.RawMessage  `json:"data"`
	Errors []envelope.Error `json:"errors"`
}

func TestAPIMethodsKeepExactRequestAndResponse(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				t.Parallel()
				body := []byte(" \n{\"unknownField\":9007199254740993,\"text\":\"<&>\u2028\"}\n")
				response := []byte(" {\"unknownField\":9007199254740993,\"text\":\"<&>\u2028\"}\n")
				var calls atomic.Int32
				const match = `"tag,with,commas", W/"weak"`
				const date = "Thu, 01 Jan 1970 00:00:00 GMT"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					got, err := io.ReadAll(req.Body)
					if err != nil || !bytes.Equal(got, body) || req.Method != method || req.URL.Path != "/rest/agile/1.0/fixture" || req.Header.Get("Content-Type") != "application/vnd.fixture+json" {
						t.Error("raw request changed method, scope, content type or exact native bytes")
					}
					if req.Header.Get("If-Match") != match || req.Header.Get("If-Unmodified-Since") != date {
						t.Error("raw request lost or changed HTTP preconditions")
					}
					q := req.URL.Query()["field"]
					if len(q) != 2 || q[0] != "summary,status" || q[1] != "a=b &+" || req.URL.Query().Get("empty") != "" {
						t.Error("query parameters were CSV-split, reordered or encoded as path data")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Set-Cookie", "fixture-private")
					w.Header().Set("X-Auth-Token", "fixture-private")
					w.Header().Add("X-RateLimit-Fixture", "first")
					w.Header().Add("X-RateLimit-Fixture", "second")
					w.WriteHeader(http.StatusOK)
					if method != "HEAD" {
						_, _ = w.Write(response)
					}
				}))
				t.Cleanup(server.Close)
				args := []string{"api", method, "/rest/agile/1.0/fixture", "--input", apiInput(t, body), "--content-type=application/vnd.fixture+json", "--query=field=summary,status", "--query=field=a=b &+", "--query=empty=", "--if-match=" + match, "--if-unmodified-since=" + date, "--output=" + mode}
				mutation := method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
				if mutation {
					args = append(args, "--force")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != 0 || calls.Load() != 1 {
					t.Fatalf("raw request failed or replayed: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
				}
				if strings.Contains(stdout, "fixture-private") {
					t.Fatal("response exposed excluded authentication or cookie headers")
				}
				if mode == "human" {
					if !strings.Contains(stdout, method+" /rest/agile/1.0/fixture") || !strings.Contains(stdout, "HTTP 200") || !strings.Contains(stdout, "If-Match: "+match) || !strings.Contains(stdout, "If-Unmodified-Since: "+date) {
						t.Fatal("human response omitted method, endpoint or HTTP status")
					}
					return
				}
				data := apiData(t, stdout, mode, true, "api."+strings.ToLower(method))
				var out envelope.APIOutput
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatal(err)
				}
				if out.Request.BodyBase64 == nil || *out.Request.BodyBase64 != base64.StdEncoding.EncodeToString(body) || out.Request.MaxResponseBytes != 16<<20 || out.Response == nil || out.Response.StatusCode != 200 || out.Response.Attempts != 1 || out.Response.Truncated {
					t.Fatal("request or response receipt lost native bytes or transport facts")
				}
				if out.Request.IfMatch != match || out.Request.IfUnmodifiedSince != date {
					t.Fatal("raw receipt dropped exact HTTP preconditions")
				}
				wantResponse := response
				if method == "HEAD" {
					wantResponse = nil
				}
				if out.Response.BodyBase64 != base64.StdEncoding.EncodeToString(wantResponse) {
					t.Fatal("response receipt changed exact captured bytes")
				}
				if method != "HEAD" && !bytes.Contains(out.Response.JSON, []byte("9007199254740993")) {
					t.Fatal("response JSON lost an unknown large integer")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if raw, present := fields["dry_run"]; present != mutation || (present && string(raw) != "false") {
					t.Fatal("read and mutation commands disagree with their declared dry-run contract")
				}
			})
		}
	}
}

func TestAPIIndependentPreconditionsKeepWireAndPreview(t *testing.T) {
	t.Parallel()
	const match = `"snapshot"`
	const date = "Thu, 01 Jan 1970 00:00:00 GMT"
	for _, tc := range []struct{ name, match, date string }{
		{name: "absent"},
		{name: "match only", match: match},
		{name: "date only", date: date},
		{name: "both", match: match, date: date},
	} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					for name, want := range map[string]string{"If-Match": tc.match, "If-Unmodified-Since": tc.date} {
						values := req.Header.Values(name)
						if want == "" {
							if len(values) != 0 {
								t.Errorf("unset %s was sent: %q", name, values)
							}
						} else if len(values) != 1 || values[0] != want {
							t.Errorf("%s changed: %q, want %q", name, values, want)
						}
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				for _, preview := range []bool{false, true} {
					args := []string{"api", "put", "/rest/api/3/fixture", "--output=" + mode}
					if tc.match != "" {
						args = append(args, "--if-match="+tc.match)
					}
					if tc.date != "" {
						args = append(args, "--if-unmodified-since="+tc.date)
					}
					env := editReceiptEnv(server.URL)
					if preview {
						args = append(args, "--dry-run")
						env = append(env, "JIRA_API_TOKEN=", "JIRA_EMAIL=")
					} else {
						args = append(args, "--force")
					}
					stdout, stderr, exit := runBinaryEnv(t, env, args...)
					if exit != 0 || calls.Load() != 1 {
						t.Fatalf("optional conditions failed or preview reached HTTP: preview=%t exit=%d calls=%d stderr=%q", preview, exit, calls.Load(), stderr)
					}
					data := apiData(t, stdout, mode, true, "api.put")
					var out envelope.APIMutationOutput
					if err := json.Unmarshal(data, &out); err != nil {
						t.Fatal(err)
					}
					if out.DryRun != preview || out.Attempted == preview || (out.Response == nil) != preview || out.Request.IfMatch != tc.match || out.Request.IfUnmodifiedSince != tc.date {
						t.Fatal("optional conditions or preview transport facts changed")
					}
					var fields struct {
						Request map[string]json.RawMessage `json:"request"`
					}
					if err := json.Unmarshal(data, &fields); err != nil {
						t.Fatal(err)
					}
					for name, want := range map[string]string{"if_match": tc.match, "if_unmodified_since": tc.date} {
						if _, present := fields.Request[name]; present != (want != "") {
							t.Errorf("%s presence changed in preview=%t", name, preview)
						}
					}
				}
			})
		}
	}
}

func TestAPIInvalidPreconditionsPrecedeInputAndHTTP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ flag, value string }{
		{"if-match", ""},
		{"if-match", "unquoted"},
		{"if-match", "\"tag\"\r\nX-Extra: value"},
		{"if-unmodified-since", ""},
		{"if-unmodified-since", "2026-10-02T00:00:00Z"},
	} {
		t.Run(tc.flag+"/"+tc.value, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "api", "put", "/rest/api/3/fixture", "--"+tc.flag+"="+tc.value, "--input", filepath.Join(t.TempDir(), "missing"), "--force", "--output=json")
			var frame apiFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != cli.ExitValidation || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagValueInvalid || frame.Errors[0].Field != tc.flag || calls.Load() != 0 {
				t.Fatalf("invalid precondition reached input/client or named the wrong fault: exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAPIPreconditionRefusalKeepsReceiptWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const stale = `"old-snapshot"`
			const date = "Thu, 01 Jan 1970 00:00:00 GMT"
			const response = `{"errorMessages":["resource changed"]}`
			var calls atomic.Int32
			var applied atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Header.Get("If-Match") != stale || req.Header.Get("If-Unmodified-Since") != date {
					t.Error("conditional write dropped its precondition")
				}
				if req.Header.Get("If-Match") == "" {
					applied.Store(true)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("ETag", `"current-snapshot"`)
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "api", "put", "/rest/api/3/fixture", "--if-match="+stale, "--if-unmodified-since="+date, "--force", "--output="+mode)
			if exit != cli.ExitValidation || calls.Load() != 1 || applied.Load() {
				t.Fatalf("stale conditional write applied, replayed or changed exit: %d, %d, %q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "HTTP 412") || !strings.Contains(stdout, "If-Match: "+stale) || !strings.Contains(stderr, "reconcile edits") {
					t.Fatal("human refusal omitted its precondition, status or recovery hint")
				}
				return
			}
			var frame apiFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeJiraPreconditionFailed || !strings.Contains(frame.Errors[0].Hint, "reconcile edits") {
				t.Fatal("precondition refusal lost its typed recovery instruction")
			}
			var out envelope.APIOutput
			if json.Unmarshal(frame.Data, &out) != nil || !out.Attempted || out.Request.IfMatch != stale || out.Request.IfUnmodifiedSince != date || out.Response == nil || out.Response.StatusCode != 412 || out.Response.Attempts != 1 || out.Response.BodyBase64 != base64.StdEncoding.EncodeToString([]byte(response)) {
				t.Fatal("precondition refusal discarded its exact request or response facts")
			}
		})
	}
}

func TestAPIReadOnlyUsesTransportClassification(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/rest/api/3/search/jql", "/rest/api/3/search/approximate-count", "/rest/api/3/issue"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != `{"jql":"project = KEY"}` {
					t.Error("raw search body changed")
				}
				_, _ = io.WriteString(w, `{"fixture":true}`)
			}))
			t.Cleanup(server.Close)
			args := []string{"api", "post", path, "--input", apiInput(t, []byte(`{"jql":"project = KEY"}`)), "--output=json"}
			if path == "/rest/api/3/issue" {
				args = append(args, "--force")
			}
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
			var f apiFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if path == "/rest/api/3/issue" {
				if exit != cli.ExitValidation || calls.Load() != 0 || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeReadOnlyBlocked {
					t.Fatalf("force bypassed read-only mode: exit=%d stderr=%q", exit, stderr)
				}
			} else if exit != 0 || calls.Load() != 1 || !f.OK {
				t.Fatalf("known search POST was refused: exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAPIMutationRefusalAndLocalPreview(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"post", "put", "patch", "delete"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			t.Cleanup(server.Close)
			args := []string{"api", method, "/rest/api/3/issue", "--input", apiInput(t, []byte("native\x00bytes")), "--output=json", "--no-input"}
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), args...)
			var f apiFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if exit != cli.ExitValidation || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeFlagRequired || string(f.Data) != "null" || calls.Load() != 0 {
				t.Fatalf("unacknowledged raw mutation reached I/O: exit=%d stderr=%q", exit, stderr)
			}
			// No credentials or reachable site: a preview must not open its client.
			stdout, stderr, exit = runBinary(t, append(args, "--dry-run")...)
			data := apiData(t, stdout, "json", true, "api."+method)
			var out envelope.APIMutationOutput
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if exit != 0 || !out.DryRun || out.Response != nil || out.Request.BodyBase64 == nil || *out.Request.BodyBase64 != "bmF0aXZlAGJ5dGVz" || calls.Load() != 0 {
				t.Fatalf("preview changed bytes or constructed a client: exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAPIHTTPFailureAndCaptureBoundRetainReceipt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, method  string
		status, limit, exit int
		code                string
	}{
		{"http-write", `{"errorMessages":["fixture refused"],"unknownField":9007199254740993}`, "post", 400, 0, cli.ExitValidation, "jira_bad_request"},
		{"http-write-not-replayed", `{"errorMessages":["fixture server failure"]}`, "post", 500, 0, cli.ExitServer, "jira_server_error"},
		{"bounded-write", "null plus more", "post", 200, 4, cli.ExitOutput, cli.CodeAPIResponseIncomplete},
		{"opaque-read", "\xff\x00\x1b", "get", 200, 0, 0, ""},
		{"invalid-utf8-json", "{\"broken\":\"\xff\"}", "get", 200, 0, 0, ""},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, test.body)
				}))
				t.Cleanup(server.Close)
				args := []string{"api", test.method, "/rest/api/3/fixture", "--max-response-bytes=" + strconv.Itoa(test.limit), "--output=" + mode}
				if test.method == "post" {
					args = append(args, "--yes")
				}
				if test.exit != 0 && mode != "human" {
					args = append(args, "--jq", `error("failed receipts must bypass jq")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != test.exit || calls.Load() != 1 {
					t.Fatalf("failure was replayed or misclassified: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "HTTP "+strconv.Itoa(test.status)) || strings.ContainsAny(stdout, "\x00\x1b") {
						t.Fatal("human receipt lost status or forwarded terminal controls")
					}
					return
				}
				data := apiData(t, stdout, mode, test.exit == 0, "api."+test.method)
				var out envelope.APIOutput
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatal(err)
				}
				body := []byte(test.body)
				if test.limit > 0 {
					body = body[:test.limit]
				}
				if out.Response == nil || out.Response.StatusCode != test.status || out.Response.BodyBase64 != base64.StdEncoding.EncodeToString(body) || out.Response.Truncated != (test.limit > 0) || out.Response.Attempts != 1 {
					t.Fatal("failure discarded or silently changed its HTTP receipt")
				}
				if test.limit > 0 && len(out.Response.JSON) != 0 {
					t.Fatal("partial capture published a complete JSON view")
				}
				if test.name == "invalid-utf8-json" && len(out.Response.JSON) != 0 {
					t.Fatal("invalid UTF-8 was converted into a lossy JSON view")
				}
				if test.exit != 0 {
					var f apiFrame
					if err := json.Unmarshal([]byte(stdout), &f); err != nil {
						t.Fatal(err)
					}
					if len(f.Errors) != 1 || f.Errors[0].Code != test.code || !strings.Contains(f.Errors[0].Hint, "verify mutation outcomes before retrying") {
						t.Fatal("raw failure changed taxonomy or omitted mutation inspection")
					}
				}
			})
		}
	}
}

func apiData(t *testing.T, stdout, mode string, ok bool, op string) json.RawMessage {
	t.Helper()
	if ok && mode == "compact" {
		return json.RawMessage(stdout)
	}
	var f apiFrame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatalf("invalid API frame: %v", err)
	}
	if f.OK != ok || f.Meta.Command != op {
		t.Fatalf("API frame changed success state or canonical operation: %s", stdout)
	}
	return f.Data
}

func apiInput(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPICanceledMutationKeepsPreparedRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != "native request" {
					t.Error("canceled raw request changed its body")
				}
				close(started)
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("raw mutation never reached its server")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, "api", "post", "/rest/api/3/issue", "--input", apiInput(t, []byte("native request")), "--force", "--output="+mode)
			if exit != cli.ExitCanceled || calls.Load() != 1 {
				t.Fatalf("canceled raw write was replayed or misclassified: exit=%d stderr=%q", exit, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "POST /rest/api/3/issue") || !strings.Contains(stdout, "No HTTP response received") || !strings.Contains(stderr, "verify mutation outcomes before retrying") {
					t.Fatal("human cancellation omitted the attempted request")
				}
				return
			}
			data := apiData(t, stdout, mode, false, "api.post")
			var out envelope.APIMutationOutput
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if !out.Attempted || out.Response != nil || out.Request.BodyBase64 == nil || *out.Request.BodyBase64 != base64.StdEncoding.EncodeToString([]byte("native request")) {
				t.Fatal("cancellation discarded the request or fabricated an HTTP response")
			}
		})
	}
}

func TestAPIIncompleteBodyIsOutputFailureWithPartialReceipt(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "part")
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "api", "post", "/rest/api/3/fixture", "--force", "--output=json")
	var f apiFrame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	var out envelope.APIMutationOutput
	if err := json.Unmarshal(f.Data, &out); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitOutput || f.OK || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeAPIResponseIncomplete || out.Response == nil || !out.Response.Truncated || out.Response.BodyBase64 != "cGFydA==" || out.Response.Attempts != 1 {
		t.Fatalf("incomplete body was discarded or treated as input validation: exit=%d stderr=%q", exit, stderr)
	}
}

func TestAPIHelpGETAndPOSTExamplesExecute(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"get", "post"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			cmd, _, err := apicmd.New(apicmd.Deps{}).Find([]string{method})
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			for line := range strings.SplitSeq(cmd.Example, "\n") {
				if strings.HasPrefix(line, "$ jira ") {
					args, err = shell.Split(strings.TrimPrefix(line, "$ jira "))
					break
				}
			}
			if err != nil || len(args) == 0 {
				t.Fatal("published API workflow is not an executable invocation")
			}
			const body = `{"jql":"project = KEY","fields":["summary"],"maxResults":1}`
			for i, arg := range args {
				if arg == "search.json" {
					args[i] = apiInput(t, []byte(body))
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if method == "get" {
					if req.Method != "GET" || req.URL.Path != "/rest/api/3/issue/KEY-123" || req.URL.Query().Get("fields") != "summary,status" {
						t.Error("GET help workflow changed its request")
					}
				} else {
					got, err := io.ReadAll(req.Body)
					if err != nil || req.Method != "POST" || req.URL.Path != "/rest/api/3/search/jql" || string(got) != body {
						t.Error("POST help workflow changed its native search request")
					}
				}
				_, _ = io.WriteString(w, `{"fixture":true}`)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
			data := apiData(t, stdout, "json", true, "api."+method)
			var out envelope.APIOutput
			if err := json.Unmarshal(data, &out); err != nil || exit != 0 || out.Response == nil || string(out.Response.JSON) != `{"fixture":true}` {
				t.Fatalf("published API workflow failed: exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAPIBodyTimeoutRetainsPartialMutationReceipt(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				_, _ = io.WriteString(w, "part")
				w.(http.Flusher).Flush()
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "api", "post", "/rest/api/3/fixture", "--force", "--timeout=100ms", "--output="+mode)
			if exit != cli.ExitTimeout || calls.Load() != 1 {
				t.Fatalf("timed-out raw mutation was replayed or misclassified: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "HTTP 200") || !strings.Contains(stdout, "capture incomplete") || !strings.Contains(stdout, "part") {
					t.Fatal("human timeout discarded the partial HTTP receipt")
				}
				return
			}
			data := apiData(t, stdout, mode, false, "api.post")
			var out envelope.APIMutationOutput
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			var f apiFrame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if out.Response == nil || out.Response.StatusCode != 200 || !out.Response.Truncated || out.Response.BodyBase64 != "cGFydA==" || len(f.Errors) != 1 || f.Errors[0].Code != "timeout" || !strings.Contains(f.Errors[0].Hint, "verify mutation outcomes before retrying") {
				t.Fatal("timeout lost its original class, mutation remedy or captured bytes")
			}
		})
	}
}
