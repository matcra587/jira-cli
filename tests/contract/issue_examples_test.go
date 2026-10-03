package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli/issue"
)

// TestIssueHelpJSONExamples executes the published examples, rather than a
// separately maintained invocation that could remain green while help breaks.
func TestIssueHelpJSONExamples(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		match    string
		want     string
		requests []string
	}{
		{name: "create-preview", command: "create", match: "> issue.json"},
		{name: "create-key", command: "create", match: "--summary 'Ship it' --output=json", want: "KEY-900\n", requests: []string{"POST /rest/api/3/issue"}},
		{name: "edit-failures", command: "edit", match: "--output=json --jq", requests: []string{"PUT /rest/api/3/issue/KEY-123", "PUT /rest/api/3/issue/KEY-124"}},
		{name: "clone-keys", command: "clone", match: "--output=json --jq", want: "KEY-901\nKEY-902\n", requests: []string{"GET /rest/api/3/issue/KEY-123", "GET /rest/api/3/issue/KEY-124", "POST /rest/api/3/issue", "POST /rest/api/3/issue"}},
		{name: "transition-comment-outcomes", command: "transition", match: "--output=json --jq", want: "KEY-124\n", requests: []string{"GET /rest/api/3/issue/KEY-123/transitions", "GET /rest/api/3/issue/KEY-124/transitions", "POST /rest/api/3/issue/KEY-123/transitions", "POST /rest/api/3/issue/KEY-124/transitions", "POST /rest/api/3/issue/KEY-124/comment"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newExampleJira(t)
			args := issueExampleArgs(t, tc.command, tc.match)
			stdout, stderr, exit := runBinaryEnv(t, stub.env(), args...)
			if exit != 0 {
				t.Fatalf("example exit = %d, stderr = %q, stdout = %q", exit, stderr, stdout)
			}
			var gotRequests []string
			for _, request := range stub.requests() {
				gotRequests = append(gotRequests, request.Method+" "+request.Path)
			}
			slices.Sort(gotRequests)
			slices.Sort(tc.requests)
			if !slices.Equal(gotRequests, tc.requests) {
				t.Fatalf("example requests = %v, want %v", gotRequests, tc.requests)
			}
			switch tc.name {
			case "create-preview":
				var payload struct {
					Fields struct {
						Summary string `json:"summary"`
						Project struct {
							Key string `json:"key"`
						} `json:"project"`
						IssueType struct {
							Name string `json:"name"`
						} `json:"issuetype"`
					} `json:"fields"`
				}
				if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
					t.Fatalf("preview payload: %v; stdout = %q", err, stdout)
				}
				if payload.Fields.Summary != "Draft" || payload.Fields.Project.Key != "ENG" || payload.Fields.IssueType.Name != "Task" {
					t.Fatalf("preview did not produce a reusable issue payload: %q", stdout)
				}
				path := filepath.Join(t.TempDir(), "issue.json")
				if err := os.WriteFile(path, []byte(stdout), 0o600); err != nil {
					t.Fatal(err)
				}
				args = issueExampleArgs(t, "create", "--json-input issue.json")
				args[len(args)-1] = path
				args = append(args, "--output=json")
				_, stderr, exit = runBinaryEnv(t, stub.env(), args...)
				if exit != 0 {
					t.Fatalf("saved payload exit = %d, stderr = %q", exit, stderr)
				}
				requests := stub.requests()
				if len(requests) != 1 || requests[0].Method != http.MethodPost || requests[0].Path != "/rest/api/3/issue" {
					t.Fatalf("saved payload requests = %+v", requests)
				}
				var sent, preview any
				if err := json.Unmarshal(requests[0].Body, &sent); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(stdout), &preview); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(sent, preview) {
					t.Fatalf("saved payload changed before submission: got %s, preview %s", requests[0].Body, stdout)
				}
			case "edit-failures":
				var result struct {
					OK    bool `json:"ok"`
					Issue struct {
						Key string `json:"key"`
					} `json:"issue"`
				}
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("failed-key projection: %v; stdout = %q", err, stdout)
				}
				if result.OK || result.Issue.Key != "KEY-124" {
					t.Fatalf("failed-key projection = %q", stdout)
				}
			default:
				if stdout != tc.want {
					t.Fatalf("example stdout = %q, want %q; stderr = %q", stdout, tc.want, stderr)
				}
			}
		})
	}
}

func TestTransitionNativePayloadHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			t.Parallel()
			match := "--json-input transition.json --dry-run --output=json"
			if remote {
				match = "--json-input transition.json --dry-run --validate-remote --output=json"
			}
			args := issueExampleArgs(t, "transition", match)
			for i, arg := range args {
				if arg == "transition.json" {
					args[i] = transitionPayloadFile(t, `{"transition":{"name":"Done"},"fields":{},"update":{},"historyMetadata":{"type":"example"}}`)
				}
			}
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/KEY-123/transitions" || r.URL.Query().Get("expand") != "transitions.fields" {
					t.Error("published transition preview changed its read boundary")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false,"fields":{}}]}`)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
			var f nativeTransitionFrame
			var payload map[string]json.RawMessage
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || json.Unmarshal(f.Data.Payload, &payload) != nil || string(payload["fields"]) != "{}" || string(payload["update"]) != "{}" || string(payload["historyMetadata"]) != `{"type":"example"}` || len(f.Data.Results) != 1 || !f.Data.Results[0].OK || f.Data.Results[0].Data.Attempted || f.Data.Results[0].Data.Applied {
				t.Fatalf("native help recipe lost its preview: exit=%d stderr=%q", exit, stderr)
			}
			wantReads := int32(0)
			if remote {
				wantReads = 1
				if json.Unmarshal(f.Data.Results[0].Data.Payload, &payload) != nil || string(payload["transition"]) != `{"id":"31"}` {
					t.Fatal("native help recipe lost the resolved per-key target")
				}
			}
			if reads.Load() != wantReads {
				t.Fatal("native help recipe crossed its read-only preview boundary")
			}
		})
	}
}

// A projection failure occurs after Jira has answered. Keep that receipt so a
// caller can distinguish a completed create from a request that needs retrying.
func TestCreateFilterFailureRetainsReceipt(t *testing.T) {
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stub := newExampleJira(t)
			expression := ".issue.key, .missing[]"
			if mode == "json" {
				expression = ".data.issue.key, .data.missing[]"
			}
			stdout, stderr, exit := runBinaryEnv(t, stub.env(), "issue", "create",
				"--project", "KEY", "--type", "Task", "--summary", "Receipt",
				"--output="+mode, "--jq", expression)
			if exit != 3 {
				t.Fatalf("exit = %d, want 3; stderr = %q", exit, stderr)
			}
			var receipt struct {
				OK   bool `json:"ok"`
				Data struct {
					Issue struct {
						Key string `json:"key"`
					} `json:"issue"`
				} `json:"data"`
				Errors []struct {
					Code string `json:"code"`
					Hint string `json:"hint"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
				t.Fatalf("stdout must be one error frame without partial jq output: %v; stdout = %q", err, stdout)
			}
			if receipt.OK || len(receipt.Errors) != 1 || receipt.Errors[0].Code != "jq_eval_failed" {
				t.Fatalf("filter failure frame = %q", stdout)
			}
			if receipt.Data.Issue.Key != "KEY-900" {
				t.Fatalf("completed create receipt lost: %q", stdout)
			}
			if !strings.Contains(receipt.Errors[0].Hint, "before retrying") {
				t.Fatalf("hint must prevent blind mutation replay: %q", receipt.Errors[0].Hint)
			}
			requests := stub.requests()
			if len(requests) != 1 || requests[0].Method != http.MethodPost || requests[0].Path != "/rest/api/3/issue" {
				t.Fatalf("create must run exactly once: %+v", requests)
			}
		})
	}
}

func TestPreviewFilterFailureRetainsWarnings(t *testing.T) {
	stub := newExampleJira(t)
	stdout, stderr, exit := runBinaryEnv(t, stub.env(), "issue", "create",
		"--project", "KEY", "--type", "Task", "--summary", "Preview",
		"--assignee", "@me", "--dry-run", "--output=json", "--jq", ".data.payload, error(\"stop\")")
	if exit != 3 {
		t.Fatalf("exit = %d, want 3; stderr = %q", exit, stderr)
	}
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatalf("stdout must contain one error frame: %v; stdout = %q", err, stdout)
	}
	if len(f.Warnings) == 0 || !strings.Contains(string(f.Data), "@me") {
		t.Fatalf("unresolved account warning or preview lost: %q", stdout)
	}
	if len(stub.requests()) != 0 {
		t.Fatal("local preview contacted Jira")
	}
}

func issueExampleArgs(t *testing.T, name, match string) []string {
	t.Helper()
	cmd, _, err := issue.New(issue.Deps{}).Find([]string{name})
	if err != nil {
		t.Fatal(err)
	}
	var matches []string
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if strings.HasPrefix(line, "$ jira ") && strings.Contains(line, match) {
			matches = append(matches, line)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("issue %s: found %d examples matching %q", name, len(matches), match)
	}
	line, _, _ := strings.Cut(matches[0], " > ")
	args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
	if err != nil {
		t.Fatal(err)
	}
	return args
}

type exampleRequest struct {
	Method string
	Path   string
	Body   json.RawMessage
}

type exampleJira struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []exampleRequest
}

func newExampleJira(t *testing.T) *exampleJira {
	t.Helper()
	s := new(exampleJira)
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *exampleJira) env() []string {
	return []string{
		"JIRA_BASE_URL=" + s.server.URL,
		"JIRA_EMAIL=person@example.com",
		"JIRA_API_TOKEN=example-fixture-token",
		"JIRA_KEYRING=false",
		"DBUS_SESSION_BUS_ADDRESS=/dev/null",
		"NO_PROXY=127.0.0.1,localhost",
	}
}

func (s *exampleJira) requests() []exampleRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

func (s *exampleJira) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.seen = append(s.seen, exampleRequest{Method: r.Method, Path: r.URL.Path, Body: body})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/rest/api/3/issue" && r.Method == http.MethodPost {
		key := "KEY-900"
		if strings.Contains(string(body), "Source KEY-123") {
			key = "KEY-901"
		} else if strings.Contains(string(body), "Source KEY-124") {
			key = "KEY-902"
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"10900","key":"`+key+`"}`)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/rest/api/3/issue/")
	if !ok {
		http.Error(w, "unmodeled fixture request", http.StatusNotFound)
		return
	}
	key, tail, _ := strings.Cut(rest, "/")
	switch {
	case tail == "" && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `{"id":"10100","key":"`+key+`","fields":{"summary":"Source `+key+`","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`)
	case tail == "" && r.Method == http.MethodPut:
		if key == "KEY-124" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"errorMessages":["fixture refuses this edit"]}`)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case tail == "transitions" && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Done","hasScreen":`+strconv.FormatBool(key == "KEY-123")+`,"isAvailable":true,"to":{"id":"5","name":"Done"}}]}`)
	case tail == "transitions" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusNoContent)
	case tail == "comment" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["fixture refuses the follow-up comment"]}`)
	default:
		http.Error(w, "unmodeled fixture request", http.StatusNotFound)
	}
}
