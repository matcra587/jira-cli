package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheIssueKeysBinaryLocalReadNeedsNoCredentialsOrStorage(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "absent-cache")
			env := []string{"XDG_CACHE_HOME=" + dir, "JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}
			args := []string{"cache", "issuekeys"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 {
				t.Fatalf("local issue keys(%s) exit=%d stdout=%q stderr=%q, want 0 without credentials", mode, exit, stdout, stderr)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("local history read created storage: %v", err)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "no recent issue keys") {
					t.Fatalf("human output=%q, want explicit empty history", stdout)
				}
				return
			}
			var data map[string]any
			if mode == "json" {
				var f frame
				if err := json.Unmarshal([]byte(stdout), &f); err != nil || !f.OK || f.Meta.Command != "cache.issuekeys" {
					t.Fatalf("history frame=%+v err=%v", f, err)
				}
				if err := json.Unmarshal(f.Data, &data); err != nil {
					t.Fatal(err)
				}
			} else if err := json.Unmarshal([]byte(stdout), &data); err != nil {
				t.Fatal(err)
			}
			keys, ok := data["issue_keys"].([]any)
			if !ok || len(keys) != 0 || data["count"] != float64(0) || data["cache_state"] != "missing" {
				t.Fatalf("history=%v, want empty array, zero count and missing provenance", data)
			}
		})
	}
}

func TestIssueHistoryBinaryRecordsAcrossProcessesAndScopesWithoutChangingPreviews(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/rest/api/3/search/jql" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"issues":[{"id":"3","key":"DEMO-3","fields":{"summary":"Third"}},{"id":"1","key":"DEMO-1","fields":{"summary":"First"}}],"isLast":true}`))
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/DEMO-") {
			key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
			_, _ = fmt.Fprintf(w, `{"id":"1","key":%q,"fields":{"summary":"A native issue"}}`, key)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+dir, "JIRA_NO_UPDATE_CHECK=1")
	for _, args := range [][]string{
		{"issue", "view", "DEMO-1", "--output=json"},
		{"issue", "view", "DEMO-2", "--output=compact"},
		{"issue", "list", "--jql=project = DEMO", "--limit=2", "--output=json"},
	} {
		stdout, stderr, exit := runBinaryEnv(t, env, args...)
		if exit != 0 {
			t.Fatalf("operation %v exit=%d stdout=%q stderr=%q", args, exit, stdout, stderr)
		}
	}
	local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true")
	read := func(extra, want []string) {
		t.Helper()
		stdout, stderr, exit := runBinaryEnv(t, append(slices.Clone(local), extra...), "cache", "issuekeys", "--output=compact")
		var out envelope.CacheIssueKeysOutput
		if err := json.Unmarshal([]byte(stdout), &out); err != nil || exit != 0 || !slices.Equal(out.IssueKeys, want) || out.Count != len(want) {
			t.Fatalf("history exit=%d err=%v out=%+v stderr=%q, want %v", exit, err, out, stderr, want)
		}
	}
	read(nil, []string{"DEMO-3", "DEMO-1", "DEMO-2"})
	for _, mode := range []string{"json", "compact", "human"} {
		stdout, stderr, exit := runBinaryEnv(t, local, "issue", "edit", "DEMO-8", "--summary=Preview", "--dry-run", "--output="+mode)
		if exit != 0 {
			t.Fatalf("preview exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
		read(nil, []string{"DEMO-3", "DEMO-1", "DEMO-2"})
	}
	read([]string{"JIRA_EMAIL=other@example.com"}, []string{})
	stdout, stderr, exit := runBinaryEnv(t, local, "--@complete=issuekey")
	if exit != 0 || stdout != "DEMO-3\nDEMO-1\nDEMO-2\n" {
		t.Fatalf("completion exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "clear", "issuekeys", "--dry-run", "--output=json")
	if exit != 0 || !strings.Contains(stdout, `"would_remove":1`) {
		t.Fatalf("clear preview exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	read(nil, []string{"DEMO-3", "DEMO-1", "DEMO-2"})
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "clear", "issuekeys", "--force", "--output=json")
	if exit != 0 || !strings.Contains(stdout, `"removed":1`) {
		t.Fatalf("clear exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	read(nil, []string{})
	if requests.Load() != 3 {
		t.Fatalf("local history, completion or previews issued Jira requests: %d, want 3", requests.Load())
	}
	blocked := filepath.Join(t.TempDir(), "file-not-directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit = runBinaryEnv(t, append(slices.Clone(env), "XDG_CACHE_HOME="+blocked), "issue", "view", "DEMO-9", "--output=json")
	if exit != 0 || !strings.Contains(stdout, `"key":"DEMO-9"`) {
		t.Fatalf("history storage failure changed read: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if requests.Load() != 4 {
		t.Fatalf("history storage failure replayed read: %d requests", requests.Load())
	}
}

func TestIssueInputDiscoveryBinaryRecordsHistoryOnlyAfterReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode    string
		selector      bool
		localRefused  bool
		authRefused   bool
		nativeRefused bool
		nativeStatus  int
		nativeCode    string
		renderRefused bool
		wantExit      int
		wantRequests  int32
		wantKeys      []string
	}{
		{name: "json", mode: "json", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "compact", mode: "compact", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "human", mode: "human", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "agent", mode: "agent", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "selector", mode: "json", selector: true, wantRequests: 3, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "local refusal", mode: "json", localRefused: true, wantExit: 3},
		{name: "credential configuration refusal", mode: "json", authRefused: true, wantExit: 3},
		{name: "native refusal", mode: "json", nativeRefused: true, wantExit: 2, wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "native bad request", mode: "json", nativeStatus: http.StatusBadRequest, nativeCode: "jira_bad_request", wantExit: 3, wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "native precondition failure", mode: "json", nativeStatus: http.StatusPreconditionFailed, nativeCode: "jira_precondition_failed", wantExit: 3, wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "render refusal", mode: "json", renderRefused: true, wantExit: 3, wantRequests: 1, wantKeys: []string{"DEMO-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && (r.URL.Path == "/rest/api/3/issue/DEMO-1/editmeta" || r.URL.Path == "/rest/api/3/issue/DEMO-2/editmeta"):
					if tc.nativeStatus != 0 {
						w.WriteHeader(tc.nativeStatus)
						_, _ = w.Write([]byte(`{"errorMessages":["native metadata read refused"]}`))
						return
					}
					if tc.nativeRefused {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"errorMessages":["issue does not exist"]}`))
						return
					}
					_, _ = w.Write([]byte(`{"fields":{}}`))
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
					var query struct {
						JQL    string   `json:"jql"`
						Fields []string `json:"fields"`
					}
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.JQL != "project = DEMO" || !slices.Equal(query.Fields, []string{"key"}) {
						t.Error("history discovery did not preserve its key-only authored selector")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_, _ = w.Write([]byte(`{"issues":[{"id":"1","key":"DEMO-1"},{"id":"2","key":"DEMO-2"}],"isLast":true}`))
				default:
					t.Errorf("history discovery issued an unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(server.Close)
			cacheDir := filepath.Join(t.TempDir(), "cache")
			env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+cacheDir, "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1")
			args := []string{"issue", "edit", "DEMO-1"}
			if tc.selector {
				args = []string{"issue", "edit", "--jql=project = DEMO"}
			}
			args = append(args, "--generate-json", "--parallelism=1")
			if tc.mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+tc.mode)
			}
			if tc.localRefused {
				args = append(args, "--summary=")
			}
			if tc.authRefused {
				env = append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
			}
			if tc.renderRefused {
				args = append(args, `--jq=error("filter refused")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != tc.wantExit || requests.Load() != tc.wantRequests {
				t.Fatalf("discovery exit=%d requests=%d, want exit=%d requests=%d; stdout=%q stderr=%q", exit, requests.Load(), tc.wantExit, tc.wantRequests, stdout, stderr)
			}
			if strings.Contains(stdout, "ReadStarted") || strings.Contains(stdout, "read_started") {
				t.Fatal("internal history observation leaked into machine output")
			}
			if tc.nativeCode != "" {
				var f struct {
					Errors []envelope.Error              `json:"errors"`
					Data   envelope.IssueEditInputOutput `json:"data"`
				}
				if err := json.Unmarshal([]byte(stdout), &f); err != nil || len(f.Errors) != 1 || f.Errors[0].Code != tc.nativeCode || len(f.Data.Results) != 1 || f.Data.Results[0].Err == nil || f.Data.Results[0].Err.Code != tc.nativeCode {
					t.Fatalf("native refusal lost its retained typed row or taxonomy: err=%v want=%s stdout=%q", err, tc.nativeCode, stdout)
				}
			}
			if tc.localRefused || tc.authRefused {
				var f struct {
					Errors []envelope.Error `json:"errors"`
				}
				wantCode := "flag_not_applicable"
				if tc.authRefused {
					wantCode = "validation_failed"
				}
				if err := json.Unmarshal([]byte(stdout), &f); err != nil || len(f.Errors) != 1 || f.Errors[0].Code != wantCode {
					t.Fatalf("pre-read refusal lost its established taxonomy: err=%v want=%s stdout=%q", err, wantCode, stdout)
				}
				if tc.authRefused && !strings.Contains(f.Errors[0].Message, "api_token not set") {
					t.Fatal("credential control refused something other than the missing token")
				}
			}
			local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
			stdout, stderr, exit = runBinaryEnv(t, local, "cache", "issuekeys", "--output=compact")
			var out envelope.CacheIssueKeysOutput
			if err := json.Unmarshal([]byte(stdout), &out); err != nil || exit != 0 || !slices.Equal(out.IssueKeys, tc.wantKeys) || out.Count != len(tc.wantKeys) {
				t.Fatalf("discovery history exit=%d err=%v out=%+v stderr=%q, want %v", exit, err, out, stderr, tc.wantKeys)
			}
			if requests.Load() != tc.wantRequests {
				t.Fatal("local history read contacted Jira")
			}
			if len(tc.wantKeys) == 0 {
				if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
					t.Fatalf("pre-read refusal opened history storage: %v", err)
				}
			}
		})
	}
}

func TestIssueHistoryObservationSchemaStaysLocal(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"issue edit", "issue move", "issue transition", "epic remove", "issue clone"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=false"}, "agent", "schema", "--path="+path, "--output=compact")
			if exit != 0 || stderr != "" {
				t.Fatalf("local observation schema failed: exit=%d stderr=%q", exit, stderr)
			}
			tree, _ := readSchema(t, stdout, "compact")
			schemas, _ := tree.Extensions["operation_schemas"].(map[string]any)
			if tree.OutputSchema == nil || path == "issue edit" && schemas["issue.edit.input"] == nil {
				t.Fatal("local schema omitted the actual observed operation")
			}
			if strings.Contains(stdout, "ReadStarted") || strings.Contains(stdout, "read_started") || strings.Contains(stdout, "JiraResponse") || strings.Contains(stdout, "jira_response") {
				t.Fatal("internal observation state leaked into the public schema")
			}
		})
	}
}

func TestIssueHistoryMixedTransitionBinaryPreservesEveryNativeFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, wantCode string
		target         string
		keys           []string
	}{
		{name: "local first", keys: []string{"DEMO-1", "DEMO-2"}, wantCode: "transition_screen_required"},
		{name: "native first", keys: []string{"DEMO-2", "DEMO-1"}, wantCode: "jira_bad_request"},
		{name: "screenless read then local refusal", keys: []string{"DEMO-1"}, wantCode: "transition_screen_required"},
		{name: "unknown target after read", keys: []string{"DEMO-1"}, target: "32", wantCode: "arg_value_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.URL.Query().Get("expand") != "transitions.fields" {
					t.Error("mixed failure history issued a write or skipped native screen metadata")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/rest/api/3/issue/DEMO-1/transitions":
					_, _ = w.Write([]byte(`{"transitions":[{"id":"31","name":"Finish","hasScreen":false,"to":{"id":"2","name":"Done"}}]}`))
				case "/rest/api/3/issue/DEMO-2/transitions":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"errorMessages":["native transition metadata refused"]}`))
				default:
					t.Error("mixed history used an unexpected native route")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			cacheDir := filepath.Join(t.TempDir(), "cache")
			env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+cacheDir, "JIRA_NO_UPDATE_CHECK=1")
			args := append([]string{"issue", "transition"}, tc.keys...)
			args = append(args, "--json-input=-", "--parallelism=1", "--no-input", "--output=json")
			target := tc.target
			if target == "" {
				target = "31"
			}
			payload := fmt.Sprintf(`{"transition":{"id":%q},"fields":{"summary":"Authored"}}`, target)
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, payload, nil, args...)
			var f struct {
				Errors []envelope.Error               `json:"errors"`
				Data   envelope.IssueTransitionOutput `json:"data"`
			}
			if exit != 3 || requests.Load() != int32(len(tc.keys)) || json.Unmarshal([]byte(stdout), &f) != nil || len(f.Errors) != 1 || f.Errors[0].Code != tc.wantCode || len(f.Data.Results) != len(tc.keys) {
				t.Fatalf("mixed transition changed joined failures: exit=%d requests=%d wantcode=%s stdout=%q stderr=%q", exit, requests.Load(), tc.wantCode, stdout, stderr)
			}
			for i, row := range f.Data.Results {
				wantCode := "transition_screen_required"
				if tc.target == "32" {
					wantCode = "arg_value_invalid"
				}
				if row.Issue.Key == "DEMO-2" {
					wantCode = "jira_bad_request"
				}
				if row.OK || row.Issue.Key != tc.keys[i] || row.Err == nil || row.Err.Code != wantCode {
					t.Fatal("mixed transition lost its ordered local/native failure facts")
				}
			}
			if strings.Contains(stdout, "JiraResponse") || strings.Contains(stdout, "jira_response") {
				t.Fatal("joined native response fact leaked into machine output")
			}
			local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true")
			stdout, stderr, exit = runBinaryEnv(t, local, "cache", "issuekeys", "--output=compact")
			var history envelope.CacheIssueKeysOutput
			if json.Unmarshal([]byte(stdout), &history) != nil || exit != 0 || !slices.Equal(history.IssueKeys, tc.keys) || history.Count != len(tc.keys) || requests.Load() != int32(len(tc.keys)) {
				t.Fatalf("mixed history depends on selected error: exit=%d history=%+v want=%v requests=%d stderr=%q", exit, history, tc.keys, requests.Load(), stderr)
			}
		})
	}
}

func TestIssueHistoryOrdinaryBinaryRetainsNativeValidationFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code   string
		status       int
		localRefused bool
		authRefused  bool
		readOnly     bool
		wantRequests int32
		wantKeys     []string
	}{
		{name: "native bad request", status: http.StatusBadRequest, code: "jira_bad_request", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "native precondition failure", status: http.StatusPreconditionFailed, code: "jira_precondition_failed", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "native conflict", status: http.StatusConflict, code: "jira_request_failed", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "native unprocessable entity", status: http.StatusUnprocessableEntity, code: "jira_request_failed", wantRequests: 1, wantKeys: []string{"DEMO-1"}},
		{name: "local body refusal", localRefused: true, code: "flag_required"},
		{name: "credential configuration refusal", authRefused: true, code: "validation_failed"},
		{name: "read only refusal", readOnly: true, code: "read_only_blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue/DEMO-1/comment" {
					t.Error("ordinary history command used an unexpected native route")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var request struct {
					Body json.RawMessage `json:"body"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || !strings.Contains(string(request.Body), "Authored comment") {
					t.Error("ordinary history command did not dispatch the authored native body")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"errorMessages":["native comment refused"]}`))
			}))
			t.Cleanup(server.Close)
			cacheDir := filepath.Join(t.TempDir(), "cache")
			env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+cacheDir, "JIRA_READ_ONLY="+fmt.Sprint(tc.readOnly), "JIRA_NO_UPDATE_CHECK=1")
			if tc.authRefused {
				env = append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
			}
			body := "Authored comment"
			if tc.localRefused {
				body = ""
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "issue", "comment", "add", "DEMO-1", "--markdown="+body, "--force", "--no-input", "--output=json")
			var f struct {
				Errors []envelope.Error               `json:"errors"`
				Data   envelope.IssueCommentAddOutput `json:"data"`
			}
			if exit != 3 || requests.Load() != tc.wantRequests || json.Unmarshal([]byte(stdout), &f) != nil || len(f.Errors) != 1 || f.Errors[0].Code != tc.code {
				t.Fatalf("ordinary native refusal exit=%d requests=%d want=%d code=%s; stdout=%q stderr=%q", exit, requests.Load(), tc.wantRequests, tc.code, stdout, stderr)
			}
			if tc.wantRequests != 0 && (len(f.Data.Results) != 1 || f.Data.Results[0].Err == nil || f.Data.Results[0].Err.Code != tc.code) {
				t.Fatal("ordinary native refusal lost its dispatched keyed outcome")
			}
			local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true")
			stdout, stderr, exit = runBinaryEnv(t, local, "cache", "issuekeys", "--output=compact")
			var history envelope.CacheIssueKeysOutput
			if json.Unmarshal([]byte(stdout), &history) != nil || exit != 0 || !slices.Equal(history.IssueKeys, tc.wantKeys) || history.Count != len(tc.wantKeys) || requests.Load() != tc.wantRequests {
				t.Fatalf("ordinary native history exit=%d requests=%d history=%+v want=%v; stderr=%q", exit, requests.Load(), history, tc.wantKeys, stderr)
			}
			if len(tc.wantKeys) == 0 {
				if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
					t.Fatalf("ordinary pre-dispatch refusal opened storage: %v", err)
				}
			}
		})
	}
}

func TestIssueHistoryPostReadLocalBinaryRetainsObservedIdentities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, response, payload, wantCode string
		clone, parent, preview            bool
	}{
		{name: "epic subtask", response: `{"parent":null,"issuetype":{"subtask":true}}`, wantCode: "arg_value_invalid"},
		{name: "epic unknown type", response: `{"parent":null,"issuetype":{}}`, wantCode: "arg_value_invalid"},
		{name: "epic malformed parent", response: `{"parent":42,"issuetype":{"subtask":false}}`, wantCode: "arg_value_invalid"},
		{name: "epic non epic parent", response: `{"parent":{"key":"DEMO-2"},"issuetype":{"subtask":false}}`, parent: true, wantCode: "arg_value_invalid"},
		{name: "epic already unparented", response: `{"parent":null,"issuetype":{"subtask":false}}`},
		{name: "epic preview", preview: true},
		{name: "clone replacement after read", clone: true, response: `{"summary":"a"}`, payload: `{"fields":{"summary":42}}`, wantCode: "validation_failed"},
		{name: "clone replacement local refusal", clone: true, payload: `{"fields":{"summary":42}}`, preview: true, wantCode: "flag_value_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					t.Error("post-read history guard issued a write")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/rest/api/3/issue/DEMO-1":
					fields := "parent,issuetype"
					if tc.clone {
						fields = "*all"
					}
					if r.URL.Query().Get("fields") != fields {
						t.Error("post-read history skipped the required native fields")
					}
					_, _ = fmt.Fprintf(w, `{"id":"1","key":"DEMO-1","fields":%s}`, tc.response)
				case "/rest/api/3/issue/DEMO-2":
					if !tc.parent || r.URL.Query().Get("fields") != "issuetype" {
						t.Error("unexpected parent history read")
					}
					_, _ = w.Write([]byte(`{"id":"2","key":"DEMO-2","fields":{"issuetype":{"subtask":false,"hierarchyLevel":0}}}`))
				default:
					t.Error("post-read history used an unexpected native route")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			cacheDir := filepath.Join(t.TempDir(), "cache")
			env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+cacheDir, "JIRA_NO_UPDATE_CHECK=1")
			args := []string{"epic", "remove", "DEMO-1"}
			if tc.clone {
				args = []string{"issue", "clone", "DEMO-1", "--replace=a=b", "--json-input=-"}
				if tc.preview {
					args[3] = "--replace=invalid"
				}
			} else if tc.preview {
				args = append(args, "--dry-run")
			}
			args = append(args, "--no-input", "--output=json")
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, tc.payload, nil, args...)
			var f struct {
				Errors []envelope.Error `json:"errors"`
				Data   struct {
					Results []envelope.KeyedResult[json.RawMessage] `json:"results"`
				} `json:"data"`
			}
			wantExit, wantRequests := 0, int32(1)
			if tc.wantCode != "" {
				wantExit = 3
			}
			if tc.parent {
				wantRequests = 2
			}
			if tc.preview {
				wantRequests = 0
			}
			if exit != wantExit || requests.Load() != wantRequests || json.Unmarshal([]byte(stdout), &f) != nil {
				t.Fatalf("post-read operation exit=%d want=%d requests=%d want=%d stdout=%q stderr=%q", exit, wantExit, requests.Load(), wantRequests, stdout, stderr)
			}
			if tc.wantCode != "" && (len(f.Errors) != 1 || f.Errors[0].Code != tc.wantCode) {
				t.Fatalf("post-read error changed: %+v", f.Errors)
			}
			if !tc.preview && (len(f.Data.Results) != 1 || f.Data.Results[0].Issue.Key != "DEMO-1" || (tc.wantCode != "" && (f.Data.Results[0].Err == nil || f.Data.Results[0].Err.Code != tc.wantCode))) {
				t.Fatal("post-read operation lost its typed outcome")
			}
			if strings.Contains(stdout, "JiraResponse") || strings.Contains(stdout, "jira_response") {
				t.Fatal("post-read fact leaked into machine output")
			}
			local := append(slices.Clone(env), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true")
			stdout, stderr, exit = runBinaryEnv(t, local, "cache", "issuekeys", "--output=compact")
			var history envelope.CacheIssueKeysOutput
			wantKeys := []string{"DEMO-1"}
			if tc.preview {
				wantKeys = nil
			}
			if json.Unmarshal([]byte(stdout), &history) != nil || exit != 0 || !slices.Equal(history.IssueKeys, wantKeys) || history.Count != len(wantKeys) || requests.Load() != wantRequests {
				t.Fatalf("post-read history exit=%d history=%+v want=%v requests=%d stderr=%q", exit, history, wantKeys, requests.Load(), stderr)
			}
			if tc.preview {
				if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
					t.Fatalf("local refusal or preview opened storage: %v", err)
				}
			}
		})
	}
}
