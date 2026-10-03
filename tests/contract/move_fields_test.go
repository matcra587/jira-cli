package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

const fieldMoveNativeInput = `{"fields":{"issuetype":{"id":"10001"},"customfield_10011":{"number":9007199254740993,"text":"<>&","null":null}},"update":{"labels":[{"add":"moved"}]},"futureCamelField":false}`

func TestMoveFieldsBinaryNativeAndOrderedPartialOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var good, failed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				var got, want map[string]any
				decoder := json.NewDecoder(strings.NewReader(string(body)))
				decoder.UseNumber()
				decodeErr := decoder.Decode(&got)
				decoder = json.NewDecoder(strings.NewReader(fieldMoveNativeInput))
				decoder.UseNumber()
				wantErr := decoder.Decode(&want)
				var native struct {
					Fields map[string]json.RawMessage `json:"fields"`
				}
				var authored struct {
					Fields map[string]json.RawMessage `json:"fields"`
				}
				nativeErr := json.Unmarshal(body, &native)
				authoredErr := json.Unmarshal([]byte(fieldMoveNativeInput), &authored)
				if r.Method != http.MethodPut || r.URL.RawQuery != "" || err != nil || decodeErr != nil || wantErr != nil || nativeErr != nil || authoredErr != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(native.Fields, authored.Fields) {
					t.Errorf("field move did not preserve native PUT: %s %s %s", r.Method, r.URL, body)
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case "/rest/api/3/issue/KEY-1":
					good.Add(1)
					w.WriteHeader(204)
				case "/rest/api/3/issue/KEY-2":
					failed.Add(1)
					w.WriteHeader(503)
				default:
					t.Errorf("unexpected issue target: %s", r.URL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), fieldMoveNativeInput, nil, "issue", "move", "fields", "KEY-2,KEY-1", "KEY-1", "--json-input=-", "--force", "--no-input", "--output="+mode)
			var out envelope.IssueEditOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "issue.move.fields", true), &out) != nil || good.Load() != 1 || failed.Load() != 1 || len(out.Results) != 2 {
				t.Fatalf("ordered partial field moves lost: exit=%d good=%d failed=%d stdout=%s stderr=%s", exit, good.Load(), failed.Load(), stdout, stderr)
			}
			if out.Results[0].Issue.Key != "KEY-2" || out.Results[0].OK || out.Results[1].Issue.Key != "KEY-1" || !out.Results[1].OK || !out.Notify || out.DryRun || !strings.Contains(stdout, "9007199254740993") {
				t.Fatalf("field move receipts or native values changed: %s", stdout)
			}
		})
	}
}

func TestMoveFieldsBinaryPreviewsAndRefusalsNeverRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, tt := range []struct {
		name, input string
		flags       []string
		exit        int
		code        string
	}{
		{name: "local preview", input: fieldMoveNativeInput, flags: []string{"--json-input=-", "--dry-run"}},
		{name: "old alias", input: fieldMoveNativeInput, flags: []string{"--from-json=-", "--dry-run"}},
		{name: "ADF alias", input: fieldMoveNativeInput, flags: []string{"--body-adf=-", "--dry-run"}},
		{name: "force required", input: fieldMoveNativeInput, flags: []string{"--json-input=-"}, exit: 3, code: "flag_required"},
		{name: "read only", input: fieldMoveNativeInput, flags: []string{"--json-input=-", "--force"}, exit: 3, code: "read_only_blocked"},
		{name: "bulk shape", input: moveNativeInput, flags: []string{"--json-input=-", "--dry-run"}, exit: 3, code: "flag_value_invalid"},
		{name: "mixed bulk shape", input: `{"fields":{"summary":"Unsafe"},"targetToSourcesMapping":{}}`, flags: []string{"--json-input=-", "--force"}, exit: 3, code: "flag_value_invalid"},
		{name: "empty", input: `{}`, flags: []string{"--json-input=-", "--dry-run"}, exit: 3, code: "flag_value_invalid"},
		{name: "malformed", input: `{"fields":null}`, flags: []string{"--json-input=-", "--force"}, exit: 3, code: "flag_value_invalid"},
		{name: "missing route", flags: []string{"--dry-run"}, exit: 3, code: "flag_required"},
		{name: "web", input: fieldMoveNativeInput, flags: []string{"--json-input=-", "--web", "--dry-run"}, exit: 3, code: "flag_not_applicable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"issue", "move", "fields", "KEY-1..KEY-2", "--no-input", "--output=json"}, tt.flags...)
			env := editReceiptEnv(server.URL)
			if tt.exit == 0 {
				env = []string{"JIRA_BASE_URL=" + server.URL, "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=false", "AGENT=1", "NO_PROXY=127.0.0.1,localhost"}
			}
			if tt.name == "read only" {
				env = append(env, "JIRA_READ_ONLY=true")
			}
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, tt.input, nil, args...)
			if exit != tt.exit || calls.Load() != 0 {
				t.Fatalf("field preview/refusal made request: exit=%d want=%d calls=%d stdout=%s stderr=%s", exit, tt.exit, calls.Load(), stdout, stderr)
			}
			if tt.code != "" {
				var frame struct {
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Errors) == 0 || frame.Errors[0].Code != tt.code {
					t.Fatalf("refusal taxonomy lost: %s", stdout)
				}
			} else {
				var out envelope.IssueEditOutput
				if json.Unmarshal(adfData(t, stdout, "json", "issue.move.fields", true), &out) != nil || !out.DryRun || len(out.Results) != 2 || !out.Results[0].OK || out.Results[0].Issue.Key != "KEY-1" || out.Results[1].Issue.Key != "KEY-2" || out.Results[0].Data.Verified != nil || !strings.Contains(stdout, "9007199254740993") {
					t.Fatalf("preview lost native input or invented a remote fact: %s", stdout)
				}
			}
		})
	}
}
