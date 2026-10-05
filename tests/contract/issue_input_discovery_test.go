package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/docent"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const inputDiscoveryField = `{"fieldId":"customfield_14002","key":"display-key","name":"Fixture \u001b[2Jchoice\u0007","schema":{"type":"future-plugin","custom":"fixture:choice","future":null},"required":true,"operations":["set"],"allowedValues":[{"id":"7001","future":{"n":9007199254740993}}],"defaultValue":{"id":"7001"},"future":"\u003c&"}`

func newInputDiscoveryServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("input discovery attempted a mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		user, _, authenticated := r.BasicAuth()
		if err != nil || len(body) != 0 || !authenticated || user != "person@example.com" {
			t.Error("input discovery bypassed authentication or sent an authored body")
		}
		switch r.URL.Path {
		case "/rest/api/3/project/KEY", "/rest/api/3/project/10402":
			_, _ = io.WriteString(w, `{"id":"10402","key":"KEY","future":null}`)
		case "/rest/api/3/issue/createmeta/10402/issuetypes":
			if r.URL.Query().Get("maxResults") != "50" {
				t.Error("type metadata changed its native page size")
			}
			if r.URL.Query().Get("startAt") == "0" {
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":2,"issueTypes":[{"id":"10001","name":"Bug"}]}`)
			} else if r.URL.Query().Get("startAt") == "1" {
				_, _ = io.WriteString(w, `{"startAt":1,"maxResults":50,"total":2,"issueTypes":[{"id":"10002","name":"Task","future":null}]}`)
			} else {
				t.Error("type metadata walked an unexpected offset")
				w.WriteHeader(http.StatusBadRequest)
			}
		case "/rest/api/3/issue/createmeta/10402/issuetypes/10002":
			if r.URL.Query().Get("maxResults") != "50" {
				t.Error("field metadata changed its native page size")
			}
			if r.URL.Query().Get("startAt") == "0" {
				_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":50,"total":2,"fields":[%s]}`, inputDiscoveryField)
			} else if r.URL.Query().Get("startAt") == "1" {
				_, _ = io.WriteString(w, `{"startAt":1,"maxResults":50,"total":2,"fields":[{"fieldId":"summary","key":"summary","name":"Summary","schema":{"type":"string"},"required":true,"operations":["set"]}]}`)
			} else {
				t.Error("field metadata walked an unexpected offset")
				w.WriteHeader(http.StatusBadRequest)
			}
		case "/rest/api/3/issue/KEY-1/editmeta":
			if r.URL.RawQuery != "" {
				t.Error("edit input enabled a screen or editability override")
			}
			_, _ = fmt.Fprintf(w, `{"fields":{"customfield_14002":%s},"future":null}`, inputDiscoveryField)
		case "/rest/api/3/issue/KEY-2/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"labels":{"key":"labels","name":"Labels","schema":{"type":"array","items":"string"},"required":false,"operations":["add","remove"]}},"future":[]}`)
		default:
			t.Errorf("input discovery used an unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestIssueCreateInputBinaryModesAndNativeMetadata(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := newInputDiscoveryServer(t, &calls)
			env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
			args := []string{"issue", "create", "--generate-json", "--project=KEY", "--type=task", "--color=never"}
			outputMode := mode
			if mode == "agent" {
				env = append(env, "AGENT=1")
				outputMode = "compact"
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" || calls.Load() != 5 {
				t.Fatalf("live metadata read failed, prompted, skipped pages or repeated requests: exit=%d calls=%d stderr=%q stdout=%q", exit, calls.Load(), stderr, stdout)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "placeholders") || !strings.Contains(stdout, "customfield_14002") || !strings.Contains(stdout, "Required") || !strings.Contains(stdout, "7001") || !strings.Contains(stdout, "Fixture choice") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatalf("human discovery lost native context or forwarded terminal controls: %q", stdout)
				}
				return
			}
			data := adfData(t, stdout, outputMode, "issue.create.input", true)
			var out envelope.IssueCreateInputOutput
			if json.Unmarshal(data, &out) != nil || !out.Complete || out.Fields == nil || len(*out.Fields) != 2 || len(out.TypePages) != 2 || len(out.FieldPages) != 2 || string((*out.Fields)["customfield_14002"]) != inputDiscoveryField {
				t.Fatalf("machine discovery lost whole native metadata: %s", data)
			}
			var payload map[string]map[string]any
			if json.Unmarshal(out.Payload, &payload) != nil || !reflect.DeepEqual(payload["fields"]["project"], map[string]any{"id": "10402"}) || !reflect.DeepEqual(payload["fields"]["issuetype"], map[string]any{"id": "10002"}) || len(payload["fields"]) != 4 || payload["fields"]["customfield_14002"] != nil {
				t.Fatalf("native skeleton guessed identities or plugin values: %s", out.Payload)
			}
			var members map[string]json.RawMessage
			if json.Unmarshal(data, &members) != nil || members["dry_run"] != nil || members["attempted"] != nil || members["applied"] != nil {
				t.Fatal("read output advertised a mutation receipt")
			}
		})
	}
}

func TestIssueEditInputBinaryKeepsEveryIssueContext(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := newInputDiscoveryServer(t, &calls)
			env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
			args := []string{"issue", "edit", "KEY-1", "KEY-2", "--generate-json", "--parallelism=1", "--color=never"}
			outputMode := mode
			if mode == "agent" {
				env = append(env, "AGENT=1")
				outputMode = "compact"
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" || calls.Load() != 2 {
				t.Fatalf("edit input read failed or used mutation policy: exit=%d calls=%d stderr=%q stdout=%q", exit, calls.Load(), stderr, stdout)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "placeholders") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatalf("human discovery lost issue contexts or sanitation: %q", stdout)
				}
				return
			}
			data := adfData(t, stdout, outputMode, "issue.edit.input", true)
			var out envelope.IssueEditInputOutput
			if json.Unmarshal(data, &out) != nil || len(out.Results) != 2 || out.Selector != nil {
				t.Fatalf("edit input lost ordered contexts: %s", data)
			}
			for i, row := range out.Results {
				if row.Issue.Key != fmt.Sprintf("KEY-%d", i+1) || !row.OK || row.Data == nil || len(row.Data.Fields) != 1 || len(row.Data.Metadata) == 0 {
					t.Fatal("edit input merged or invented issue contexts")
				}
				var payload map[string]json.RawMessage
				if json.Unmarshal(row.Data.Payload, &payload) != nil {
					t.Fatal("edit skeleton is not native JSON")
				}
				if i == 0 && payload["update"] != nil {
					t.Fatal("settable field acquired unrelated update operations")
				}
				if i == 1 && string(payload["update"]) != `{"labels":[{"add":null},{"remove":null}]}` {
					t.Fatal("native update operations or their order were changed")
				}
			}
		})
	}
}

func TestIssueEditInputSelectorRequestsIssueKeys(t *testing.T) {
	t.Parallel()
	var searches, edits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			searches.Add(1)
			var request struct {
				Fields []string `json:"fields"`
				Cursor string   `json:"nextPageToken"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("selector did not send its declared search read")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Jira's enhanced-search default returns IDs only. A caller that
			// consumes keys must explicitly request them on every cursor page.
			if len(request.Fields) != 1 || request.Fields[0] != "key" {
				_, _ = io.WriteString(w, `{"isLast":true,"issues":[{"id":"10001"}]}`)
				return
			}
			switch request.Cursor {
			case "":
				_, _ = io.WriteString(w, `{"isLast":false,"nextPageToken":"next","issues":[{"id":"10001","key":"KEY-1"}]}`)
			case "next":
				_, _ = io.WriteString(w, `{"isLast":true,"issues":[{"id":"10002","key":"KEY-2"}]}`)
			default:
				t.Error("selector changed the native cursor")
				w.WriteHeader(http.StatusBadRequest)
			}
		case "/rest/api/3/issue/KEY-1/editmeta", "/rest/api/3/issue/KEY-2/editmeta":
			edits.Add(1)
			if r.Method != http.MethodGet {
				t.Error("input discovery submitted an edit")
			}
			_, _ = io.WriteString(w, `{"fields":{}}`)
		default:
			t.Error("selector escaped its explicit read context")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "issue", "edit", "--generate-json", "--jql=project = KEY", "--output=json")
	data := adfData(t, stdout, "json", "issue.edit.input", true)
	var out envelope.IssueEditInputOutput
	if exit != 0 || stderr != "" || json.Unmarshal(data, &out) != nil || len(out.Results) != 2 || searches.Load() != 2 || edits.Load() != 2 {
		t.Fatalf("key-only selector could not enumerate complete native search pages: exit=%d searches=%d edits=%d stderr=%q", exit, searches.Load(), edits.Load(), stderr)
	}
	for i, row := range out.Results {
		if !row.OK || row.Issue.Key != fmt.Sprintf("KEY-%d", i+1) || row.Data == nil {
			t.Fatal("key-only selector changed its ordered targets")
		}
	}
}

func TestIssueInputDiscoveryUsesExactNativeFactKeys(t *testing.T) {
	t.Parallel()
	const native = `{"fieldId":"customfield_14002","key":"display","name":"Exact native name","Name":"Future name","required":true,"Required":false,"schema":{"type":"string"},"Schema":{"type":"number"},"operations":["set"],"Operations":["remove"],"allowedValues":[{"id":"7001"}],"AllowedValues":[{"id":"9999"}]}`
	for _, verb := range []string{"create", "edit"} {
		for _, mode := range []string{"json", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("native fact discovery became a mutation")
					}
					switch r.URL.Path {
					case "/rest/api/3/project/KEY":
						_, _ = io.WriteString(w, `{"id":"10402","key":"KEY"}`)
					case "/rest/api/3/issue/createmeta/10402/issuetypes":
						_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10001","name":"Task"}]}`)
					case "/rest/api/3/issue/createmeta/10402/issuetypes/10001":
						_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":50,"total":1,"fields":[%s]}`, native)
					case "/rest/api/3/issue/KEY-1/editmeta":
						_, _ = fmt.Fprintf(w, `{"fields":{"customfield_14002":%s}}`, native)
					default:
						t.Error("native fact read escaped its declared context")
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", verb, "--generate-json", "--output=" + mode, "--color=never"}
				if verb == "create" {
					args = append(args, "--project=KEY", "--type=Task")
				} else {
					args = append(args, "KEY-1")
				}
				stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("exact native facts were refused: exit=%d stderr=%q", exit, stderr)
				}
				if mode == "human" {
					required := false
					for line := range strings.SplitSeq(stdout, "\n") {
						words := strings.Fields(line)
						if len(words) >= 2 && words[0] == "Required:" && words[len(words)-1] == "true" {
							required = true
						}
					}
					if !required || !strings.Contains(stdout, "Exact native name") || !strings.Contains(stdout, `"type":"string"`) || !strings.Contains(stdout, "7001") || strings.Contains(stdout, "Future name") || strings.Contains(stdout, `"type":"number"`) || strings.Contains(stdout, "9999") {
						t.Fatalf("human projection used case-variant future facts: %q", stdout)
					}
					return
				}
				data := adfData(t, stdout, "json", "issue."+verb+".input", true)
				var payload json.RawMessage
				var fields map[string]json.RawMessage
				if verb == "create" {
					var out envelope.IssueCreateInputOutput
					if json.Unmarshal(data, &out) != nil || out.Fields == nil {
						t.Fatal("create discovery lost the native field map")
					}
					payload, fields = out.Payload, *out.Fields
				} else {
					var out envelope.IssueEditInputOutput
					if json.Unmarshal(data, &out) != nil || len(out.Results) != 1 || out.Results[0].Data == nil {
						t.Fatal("edit discovery lost the native field map")
					}
					payload, fields = out.Results[0].Data.Payload, out.Results[0].Data.Fields
				}
				var skeleton struct {
					Fields map[string]json.RawMessage `json:"fields"`
					Update map[string]json.RawMessage `json:"update"`
				}
				if string(fields["customfield_14002"]) != native || json.Unmarshal(payload, &skeleton) != nil || string(skeleton.Fields["customfield_14002"]) != "null" || skeleton.Update["customfield_14002"] != nil {
					t.Fatal("unknown metadata member replaced a native operation or was dropped")
				}
			})
		}
	}
}

func TestIssueInputBinaryRefusesIgnoredOptionsAndAttributesReadFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, op, code, field string
		args                  []string
	}{
		{name: "create summary", op: "issue.create.input", code: cli.CodeFlagNotApplicable, field: "summary", args: []string{"issue", "create", "--generate-json", "--project=KEY", "--type=Task", "--summary="}},
		{name: "create local preview", op: "issue.create.input", code: cli.CodeFlagNotApplicable, field: "dry-run", args: []string{"issue", "create", "--generate-json", "--project=KEY", "--type=Task", "--dry-run=false"}},
		{name: "edit payload alias", op: "issue.edit.input", code: cli.CodeFlagNotApplicable, field: "json-input", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--from-json=/missing/input.json"}},
		{name: "edit body alias", op: "issue.edit.input", code: cli.CodeFlagNotApplicable, field: "markdown-file", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--template=-"}},
		{name: "edit force alias", op: "issue.edit.input", code: cli.CodeFlagNotApplicable, field: "force", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--yes=false"}},
		{name: "edit notification alias", op: "issue.edit.input", code: cli.CodeFlagNotApplicable, field: "notify", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--skip-notify=false"}},
		{name: "create context", op: "issue.create.input", code: cli.CodeFlagValueInvalid, field: "project", args: []string{"issue", "create", "--generate-json"}},
		{name: "create operand", op: "issue.create.input", code: cli.CodeArgCountInvalid, args: []string{"issue", "create", "extra", "--generate-json"}},
		{name: "edit missing targets", op: "issue.edit.input", code: cli.CodeArgCountInvalid, args: []string{"issue", "edit", "--generate-json"}},
		{name: "edit conflicting selectors", op: "issue.edit.input", code: cli.CodeFlagConflict, field: "jql", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--jql=project = KEY"}},
		{name: "edit invalid parallelism", op: "issue.edit.input", code: cli.CodeFlagValueInvalid, field: "parallelism", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--parallelism=0"}},
		{name: "edit web", op: "issue.edit.input", code: cli.CodeFlagNotApplicable, field: "web", args: []string{"issue", "edit", "KEY-1", "--generate-json", "--web=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := newInputDiscoveryServer(t, &calls)
			for _, mode := range []string{"json", "compact"} {
				args := append(append([]string(nil), tc.args...), "--output="+mode)
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var f frame
				var failure envelope.Error
				if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || f.Meta.Command != tc.op || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != tc.code || failure.Field != tc.field || string(f.Data) != "null" || calls.Load() != 0 {
					t.Fatalf("input refusal performed I/O, lost read attribution or full frame: exit=%d calls=%d stderr=%q stdout=%q", exit, calls.Load(), stderr, stdout)
				}
			}
		})
	}
}

func TestIssueCreateInputSkeletonExtractsAndRoundTripsThroughNativePreview(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := newInputDiscoveryServer(t, &calls)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "create", "--generate-json", "--project=10402", "--type=10002", "--output=compact", "--jq=.payload")
	if exit != 0 || stderr != "" || calls.Load() != 5 {
		t.Fatalf("published skeleton extraction failed: exit=%d stderr=%q", exit, stderr)
	}
	var payload struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	payload.Fields["summary"] = json.RawMessage(`"authored"`)
	payload.Fields["customfield_14002"] = json.RawMessage(`{"id":"7001","future":{"n":9007199254740993}}`)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "issue.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN="}, "issue", "create", "--json-input="+path, "--dry-run", "--generate-json=false", "--output=json")
	data := adfData(t, stdout, "json", "issue.create", true)
	var preview envelope.IssueCreateOutput
	if exit != 0 || stderr != "" || json.Unmarshal(data, &preview) != nil || !preview.DryRun || string(preview.Payload) != string(raw) || calls.Load() != 5 {
		t.Fatalf("filled metadata skeleton changed through native preview or required authentication: exit=%d stderr=%q data=%s", exit, stderr, data)
	}
}

func TestIssueInputDiscoverySchemaAndHelpStayLocal(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := newInputDiscoveryServer(t, &calls)
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN="}
	for _, verb := range []string{"create", "edit"} {
		op := "issue." + verb + ".input"
		stdout, stderr, exit := runBinaryEnv(t, env, "agent", "schema", "--path=issue "+verb, "--output=compact")
		if exit != 0 || stderr != "" {
			t.Fatalf("local read schema failed: exit=%d stderr=%q", exit, stderr)
		}
		tree, _ := readSchema(t, stdout, "compact")
		if tree.ReadOnly {
			t.Fatal("mixed write/read command advertised itself as wholly read-only")
		}
		variantFound, flagFound := false, false
		for _, flag := range tree.Flags {
			flagFound = flagFound || flag.Name == "generate-json"
		}
		operations := tree.Extensions["operations"].([]any)
		for _, raw := range operations {
			operation := raw.(map[string]any)
			if operation["op"] != op {
				continue
			}
			variantFound = true
			if operation["class"] != "read" || operation["variant"] != "--generate-json" || operation["has_output_schema"] != true {
				t.Fatal("live input variant omitted read classification or its real shape")
			}
		}
		schemas := tree.Extensions["operation_schemas"].(map[string]any)
		properties := schemas[op].(map[string]any)["properties"].(map[string]any)
		if !variantFound || !flagFound || properties["dry_run"] != nil || properties["payload"] == nil && verb == "create" || properties["results"] == nil && verb == "edit" {
			t.Fatal("selected operation reused the mutation shape or hid its selecting flag")
		}
		stdout, stderr, exit = runBinaryEnv(t, env, "issue", verb, "--generate-json", "--help", "--color=never")
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "--generate-json") || !strings.Contains(stdout, "placeholders") {
			t.Fatalf("read help constructed a client, required context or omitted contract: exit=%d stderr=%q", exit, stderr)
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "agent", "schema", "--output=compact")
	if exit != 0 || stderr != "" {
		t.Fatal("routing discovery failed")
	}
	tree, _ := readSchema(t, stdout, "compact")
	var walk func(docent.Command)
	walk = func(node docent.Command) {
		if node.Extensions["operation_schemas"] != nil {
			t.Fatal("bare routing tree embedded full read shape bodies")
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(tree)
	if calls.Load() != 0 {
		t.Fatal("help or schema opened live metadata without an executed read")
	}
}

func TestIssueCreateInputFailureRetainsOnlyEstablishedFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		exit       int
	}{
		{name: "unknown", code: cli.CodeIssueTypeUnknown, exit: cli.ExitValidation},
		{name: "ambiguous", code: cli.CodeIssueTypeAmbiguous, exit: cli.ExitValidation},
		{name: "malformed fields", code: cli.CodeIssueMetadataResponseInvalid, exit: cli.ExitServer},
		{name: "project404", code: "jira_not_found", exit: cli.ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Error("failed input discovery became a mutation")
				}
				switch r.URL.Path {
				case "/rest/api/3/project/KEY":
					if tc.name == "project404" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					_, _ = io.WriteString(w, `{"id":"10402","key":"KEY","future":null}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes":
					_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":2,"issueTypes":[{"id":"10001","name":"Task"},{"id":"10002","name":"TASK"}]}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes/10001":
					if tc.name != "malformed fields" {
						t.Error("unresolved type triggered field reads")
					}
					_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"fields":[{"fieldId":"summary","required":null}]}`)
				default:
					t.Error("failed discovery escaped its explicit context")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			selected := "Task"
			switch tc.name {
			case "unknown":
				selected = "missing"
			case "malformed fields":
				selected = "10001"
			}
			for _, mode := range []string{"json", "compact"} {
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "create", "--generate-json", "--project=KEY", "--type="+selected, "--output="+mode, "--jq", `error("failed metadata must bypass jq")`)
				var f frame
				var failure envelope.Error
				if exit != tc.exit || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || f.Meta.Command != "issue.create.input" || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != tc.code {
					t.Fatalf("read failure lost its full frame or taxonomy: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
				}
				if tc.name == "project404" {
					if string(f.Data) != "null" {
						t.Fatal("failed project read fabricated a native context")
					}
					continue
				}
				var out envelope.IssueCreateInputOutput
				if json.Unmarshal(f.Data, &out) != nil || out.Project == nil || out.Complete || out.Fields != nil || out.Payload != nil {
					t.Fatal("failed collection lost established project or invented complete input")
				}
				if (tc.name == "unknown" || tc.name == "ambiguous") && (out.IssueTypes == nil || len(*out.IssueTypes) != 2 || failure.Field != "type") {
					t.Fatal("local type refusal dropped actual disambiguation candidates")
				}
			}
			if tc.name == "unknown" || tc.name == "ambiguous" {
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "create", "--generate-json", "--project=KEY", "--type="+selected, "--output=human", "--color=never")
				if exit != tc.exit || !strings.Contains(stdout, "Create input discovery did not complete.") || !strings.Contains(stderr, "--output=json") || !strings.Contains(stderr, "data.issue_types") || !strings.Contains(stderr, "--type") {
					t.Fatalf("human type refusal omitted an executable candidate recovery: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
			}
		})
	}
}

func TestIssueEditInputFailureKeepsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Error("edit metadata failure used a mutation method")
				}
				if r.URL.Path == "/rest/api/3/issue/KEY-1/editmeta" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.URL.Path != "/rest/api/3/issue/KEY-2/editmeta" {
					t.Error("metadata failure changed target")
				}
				if partial {
					_, _ = io.WriteString(w, `{"fields":{}}`)
				} else {
					_, _ = io.WriteString(w, `{"fields":null}`)
				}
			}))
			t.Cleanup(server.Close)
			for _, mode := range []string{"json", "compact", "human"} {
				args := []string{"issue", "edit", "KEY-1", "KEY-2", "--generate-json", "--parallelism=1", "--output=" + mode}
				if !partial && mode != "human" {
					args = append(args, "--jq", `error("failed edit discovery must bypass jq")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				wantExit := cli.ExitServer
				if partial {
					wantExit = 0
				}
				if exit != wantExit {
					t.Fatalf("keyed discovery lost total/partial policy: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || strings.Contains(stdout, "updated") {
						t.Fatal("human read failure lost contexts or claimed an edit")
					}
					continue
				}
				data := adfData(t, stdout, mode, "issue.edit.input", partial)
				var out envelope.IssueEditInputOutput
				if json.Unmarshal(data, &out) != nil || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].OK || out.Results[0].Err == nil || out.Results[0].Err.Code != "jira_not_found" || out.Results[1].Issue.Key != "KEY-2" || out.Results[1].OK != partial {
					t.Fatalf("keyed read failure dropped or invented outcomes: %s", data)
				}
			}
			if calls.Load() != 6 {
				t.Fatal("failed discovery replayed, skipped or added requests")
			}
		})
	}
}
