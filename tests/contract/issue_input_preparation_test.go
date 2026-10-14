package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

const preparationScreen = `{"fields":{"customfield_1":{"key":"customfield_1","name":"Choice","required":false,"operations":["set"],"schema":{"type":"option","custom":"com.atlassian.jira.plugin.system.customfieldtypes:select"}},"customfield_2":{"key":"customfield_2","name":"Future","required":true,"operations":["set","add"],"schema":{"type":"future","custom":"example.vendor:select"}}}}`

// These authored and expected shapes are independent fixtures for the baseline
// families. Do not derive them from the production codec or example registry.
func TestIssueInputPreparationKnownFamiliesReachNativeWrite(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, token, input, want string }{
		{"string", "textfield", `"plain"`, `"plain"`},
		{"number", "float", `9007199254740993`, `9007199254740993`},
		{"date", "datepicker", `"2028-02-29"`, `"2028-02-29"`},
		{"datetime", "datetime", `"2026-10-01T12:00:00.000+0000"`, `"2026-10-01T12:00:00.000+0000"`},
		{"labels", "labels", `["one","two"]`, `["one","two"]`},
		{"select", "select", `"123"`, `{"value":"123"}`},
		{"multiselect", "multiselect", `["one",{"id":"2"}]`, `[{"value":"one"},{"id":"2"}]`},
		{"user", "userpicker", `"account-1"`, `{"accountId":"account-1"}`},
		{"multiuser", "multiuserpicker", `["account-1"]`, `[{"accountId":"account-1"}]`},
		{"group", "grouppicker", `"team"`, `{"name":"team"}`},
		{"multigroup", "multigrouppicker", `["team"]`, `[{"name":"team"}]`},
		{"components", "@components", `[{"name":"core"}]`, `[{"name":"core"}]`},
		{"version", "@versions", `[{"name":"v1"}]`, `[{"name":"v1"}]`},
		{"fixversions", "@fixVersions", `[{"name":"v1"}]`, `[{"name":"v1"}]`},
		{"versionpicker", "version", `"123"`, `{"name":"123"}`},
		{"multiversionpicker", "multiversion", `["v1"]`, `[{"name":"v1"}]`},
		{"projectpicker", "project", `"KEY"`, `{"key":"KEY"}`},
		{"parent", "@parent", `{"key":"KEY-123"}`, `{"key":"KEY-123"}`},
		{"cascadingselect", "cascadingselect", `{"value":"top","child":{"value":"child"}}`, `{"value":"top","child":{"value":"child"}}`},
		{"textarea", "textarea", `"**Bold**"`, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Bold","marks":[{"type":"strong"}]}]}]}`},
		{"numeric string", "float", `"1.00e+9"`, `1.00e+9`},
		{"vendor suffix", "example.vendor:select", `{"futureValue":"<>&\u2028","nullable":null,"empty":[]}`, `{"futureValue":"<>&\u2028","nullable":null,"empty":[]}`},
	}
	fields := map[string]json.RawMessage{}
	definitions := map[string]json.RawMessage{}
	wanted := map[string]json.RawMessage{}
	ids := map[string]string{}
	for i, tc := range cases {
		id := fmt.Sprintf("customfield_%d", 20000+i)
		schema := map[string]string{"type": "fixture", "custom": tc.token}
		if system, ok := strings.CutPrefix(tc.token, "@"); ok {
			id = system
			schema = map[string]string{"type": "fixture", "system": system}
		} else if !strings.Contains(tc.token, ":") {
			schema["custom"] = "com.atlassian.jira.plugin.system.customfieldtypes:" + tc.token
		}
		definition, err := json.Marshal(map[string]any{"key": id, "name": tc.name, "required": false, "operations": []string{"set"}, "schema": schema})
		if err != nil {
			t.Fatal(err)
		}
		fields[id], definitions[id], wanted[id], ids[tc.name] = json.RawMessage(tc.input), definition, json.RawMessage(tc.want), id
	}
	inputBytes, err := jira.MarshalNoEscape(map[string]any{"fields": fields})
	if err != nil {
		t.Fatal(err)
	}
	metadataBytes, err := jira.MarshalNoEscape(map[string]any{"fields": definitions})
	if err != nil {
		t.Fatal(err)
	}
	input, metadata := apiInput(t, inputBytes), apiInput(t, metadataBytes)
	var puts atomic.Int32
	var mu sync.Mutex
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-123" {
			t.Error("preparation pipeline changed its native write route")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		received = bytes.Clone(body)
		mu.Unlock()
		puts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	check := func(t *testing.T, raw []byte) {
		t.Helper()
		var payload struct {
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(raw, &payload) != nil || len(payload.Fields) != len(wanted) {
			t.Fatal("prepared native request lost its complete field set")
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if !bytes.Equal(payload.Fields[ids[tc.name]], wanted[ids[tc.name]]) {
					t.Fatalf("prepared native field changed: got %s want %s", payload.Fields[ids[tc.name]], wanted[ids[tc.name]])
				}
			})
		}
	}
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			before := puts.Load()
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--output="+mode)
			var prepared envelope.IssueInputPreparationOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "issue.input.prepare", true), &prepared) != nil || len(prepared.Fields) != len(cases) || puts.Load() != before {
				t.Fatal("local known-family preparation failed or opened Jira")
			}
			preparedInput := apiInput(t, prepared.Payload)
			for _, preview := range []bool{true, false} {
				args := []string{"issue", "edit", "KEY-123", "--json-input", preparedInput, "--no-input", "--output=" + mode}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var edited envelope.IssueEditOutput
				if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "issue.edit", true), &edited) != nil || edited.DryRun != preview || len(edited.Results) != 1 || !edited.Results[0].OK || !bytes.Equal(edited.Payload, prepared.Payload) {
					t.Fatal("prepared candidate changed through the native preview/write receipt")
				}
				if preview {
					if puts.Load() != before {
						t.Fatal("native preview sent a write")
					}
					continue
				}
				if puts.Load() != before+1 {
					t.Fatal("native write was omitted or replayed")
				}
				mu.Lock()
				wire := bytes.Clone(received)
				mu.Unlock()
				check(t, wire)
			}
		})
	}
	fields[ids["select"]] = json.RawMessage(`42`)
	invalid, err := jira.MarshalNoEscape(map[string]any{"fields": fields})
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "input", "prepare", "--input", apiInput(t, invalid), "--metadata", metadata, "--output=json")
	var refused frame
	if exit != 3 || json.Unmarshal([]byte(stdout), &refused) != nil || refused.OK || string(refused.Data) != "null" || puts.Load() != 2 {
		t.Fatal("malformed known field published a partial prepared request or entered mutation")
	}
}

func TestIssueInputPreparationBinaryModesRemainLocalAndPreserveNative(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	metadata := apiInput(t, []byte(preparationScreen))
	const payload = `{"fields":{"custom_fields":{"customfield_1":"123"},"customfield_2":{"futureCamel":"<>&\u2028","number":9007199254740993,"null":null,"empty":[]}},"future":{"rawNumber":1.00e+9}}`
	input := apiInput(t, []byte(payload))
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=fixture@example.test", "JIRA_API_TOKEN=synthetic-preparation-fixture", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true", "AGENT=1"}
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnv(t, env, "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("local preparation failed: exit=%d stderr=%q stdout=%s", exit, stderr, stdout)
			}
			var out envelope.IssueInputPreparationOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "issue.input.prepare", true), &out); err != nil {
				t.Fatal(err)
			}
			var prepared struct {
				Fields map[string]json.RawMessage `json:"fields"`
				Future json.RawMessage            `json:"future"`
			}
			if err := json.Unmarshal(out.Payload, &prepared); err != nil {
				t.Fatal(err)
			}
			if len(prepared.Fields) != 2 || string(prepared.Fields["customfield_1"]) != `{"value":"123"}` || !bytes.Contains(prepared.Fields["customfield_2"], []byte(`9007199254740993`)) || !bytes.Contains(prepared.Fields["customfield_2"], []byte(`"<>&\u2028"`)) || !bytes.Contains(prepared.Future, []byte(`1.00e+9`)) {
				t.Fatalf("preparation guessed an ID, lost the wrapper or normalized native input: %s", out.Payload)
			}
			if len(out.Fields) != 2 || out.Fields[0].Field != "customfield_1" || out.Fields[0].Handling != "encoded" || out.Fields[1].Field != "customfield_2" || out.Fields[1].Handling != "unverified" || out.Diagnostics == nil {
				t.Fatalf("handling receipt lost ordering or overclaimed validation: %+v", out)
			}
		})
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--jq", ".data.payload.fields.customfield_1.value")
	if exit != 0 || stderr != "" || stdout != "123\n" {
		t.Fatalf("shared jq output did not expose the prepared identity: %d %q %q", exit, stderr, stdout)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "issue", "input", "prepare", "--input", input, "--metadata", metadata)
	if exit != 0 || stderr != "" {
		t.Fatalf("agent default preparation failed: %d %q %s", exit, stderr, stdout)
	}
	var agentData envelope.IssueInputPreparationOutput
	if err := json.Unmarshal(adfData(t, stdout, "compact", "issue.input.prepare", true), &agentData); err != nil || len(agentData.Fields) != 2 || agentData.Fields[0].Handling != "encoded" {
		t.Fatalf("agent default did not use the shared compact result: %v %s", err, stdout)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Prepared issue input") || !strings.Contains(stdout, "unverified") || !strings.Contains(stdout, "permissions") || requests.Load() != 0 {
		t.Fatalf("human local preparation lost its scope or opened Jira: %d %q %q requests=%d", exit, stderr, stdout, requests.Load())
	}
}

func TestIssueInputPreparationBinaryRefusalsPublishNoPartialCandidate(t *testing.T) {
	t.Parallel()
	metadata := apiInput(t, []byte(preparationScreen))
	for _, tc := range []struct{ name, payload string }{
		{"known number", `{"fields":{"customfield_1":42}}`},
		{"wrapper collision", `{"fields":{"customfield_1":"Low","custom_fields":{"customfield_1":"High"}}}`},
		{"wrapper null", `{"fields":{"custom_fields":null}}`},
		{"absent screen field", `{"fields":{"missing":"High"}}`},
		{"conflicting containers", `{"fields":{"customfield_1":"High"},"update":{"customfield_1":[{"set":{"id":"1"}}]}}`},
		{"duplicate input", `{"fields":{"customfield_1":"High","customfield_1":"Low"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := apiInput(t, []byte(tc.payload))
			for _, mode := range []string{"json", "compact"} {
				stdout, stderr, exit := runBinary(t, "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--output="+mode)
				var frame apiFrame
				if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
					t.Fatal(err)
				}
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Meta.Command != "issue.input.prepare" || string(frame.Data) != "null" {
					t.Fatalf("refusal was unframed or retained a partial candidate: %d %q %s", exit, stderr, stdout)
				}
			}
		})
	}
}

func TestIssueInputPreparationBinaryStdinAndCreateContext(t *testing.T) {
	t.Parallel()
	metadata := apiInput(t, []byte(`{"fields":{}}`))
	const payload = `{"fields":{"project":"123","issuetype":"456"},"future":null}`
	stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, payload, nil, "issue", "input", "prepare", "--metadata", metadata, "--create", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("create stdin preparation failed: %d %q %s", exit, stderr, stdout)
	}
	var out envelope.IssueInputPreparationOutput
	if err := json.Unmarshal(adfData(t, stdout, "json", "issue.input.prepare", true), &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Payload) != `{"fields":{"issuetype":{"name":"456"},"project":{"key":"123"}},"future":null}` {
		t.Fatalf("create preparation guessed IDs or lost native null: %s", out.Payload)
	}
	input := apiInput(t, []byte(payload))
	stdout, stderr, exit = runBinaryEnvInputStarted(t, nil, `{"fields":{}}`, nil, "issue", "input", "prepare", "--input", input, "--metadata=-", "--create", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("metadata stdin preparation failed: %d %q %s", exit, stderr, stdout)
	}
	var got envelope.IssueInputPreparationOutput
	if err := json.Unmarshal(adfData(t, stdout, "json", "issue.input.prepare", true), &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, out.Payload) {
		t.Fatalf("metadata stdin changed the candidate: %s", got.Payload)
	}
}

func TestIssueInputPreparationBinaryValidatesFlagsBeforeReading(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--input=missing"},
		{"--metadata=", "--input=missing"},
		{"--input=", "--metadata=missing"},
		{"--input=-", "--metadata=-"},
		{"--input=missing", "--metadata=missing", "--web=false"},
	} {
		stdout, stderr, exit := runBinary(t, append([]string{"issue", "input", "prepare", "--output=json"}, args...)...)
		var frame apiFrame
		if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
			t.Fatal(err)
		}
		if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code == "local_io" || strings.Contains(frame.Errors[0].Message, "is not a file") || string(frame.Data) != "null" {
			t.Fatalf("pure flag validation read missing files or accepted invalid selection: %d %q %s", exit, stderr, stdout)
		}
	}
}

func TestIssueInputPreparationBinaryBoundsAndValidatesMetadata(t *testing.T) {
	t.Parallel()
	validInput := apiInput(t, []byte(`{"fields":{"customfield_1":"High"}}`))
	validMetadata := apiInput(t, []byte(preparationScreen))
	oversized := apiInput(t, []byte(strings.Repeat(" ", 1<<20+1)))
	for _, tc := range []struct{ name, input, metadata, field, message string }{
		{"large input", oversized, validMetadata, "input", "larger than"},
		{"large metadata", validInput, oversized, "metadata", "larger than"},
		{"duplicate metadata", validInput, apiInput(t, []byte(`{"fields":{},"fields":{}}`)), "metadata", "unique member"},
		{"missing required metadata", validInput, apiInput(t, []byte(`{"fields":{"customfield_1":{"name":"Choice","schema":{"type":"option"}}}}`)), "metadata", "explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinary(t, "issue", "input", "prepare", "--input", tc.input, "--metadata", tc.metadata, "--output=json")
			var frame apiFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Field != tc.field || !strings.Contains(frame.Errors[0].Message, tc.message) || string(frame.Data) != "null" {
				t.Fatalf("input guard did not name its own boundary or retained partial data: %d %q %s", exit, stderr, stdout)
			}
		})
	}
}

func TestIssueInputPreparationBinaryRetainsMarkdownSourceDiagnostics(t *testing.T) {
	t.Parallel()
	metadata := apiInput(t, []byte(`{"fields":{"customfield_1":{"key":"customfield_1","name":"Notes","required":false,"operations":["set"],"schema":{"type":"string","custom":"textarea"}}}}`))
	input := apiInput(t, []byte(`{"fields":{"customfield_1":"![preview](https://example.invalid/pic.png)"}}`))
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinary(t, "issue", "input", "prepare", "--input", input, "--metadata", metadata, "--output="+mode)
		if exit != 0 || stderr != "" {
			t.Fatalf("non-lossy authoring failed: %d %q %s", exit, stderr, stdout)
		}
		var out envelope.IssueInputPreparationOutput
		if err := json.Unmarshal(adfData(t, stdout, mode, "issue.input.prepare", true), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Diagnostics) == 0 || out.Diagnostics[0].Lossy || out.Diagnostics[0].Field != "customfield_1" || out.Diagnostics[0].Path == "" || out.Diagnostics[0].NodeType == "" || len(out.Fields) != 1 || out.Fields[0].Handling != "encoded" || !bytes.Contains(out.Payload, []byte(`https://example.invalid/pic.png`)) {
			t.Fatalf("authoring hid a shape change or lost its field/source context: %+v", out)
		}
	}
}

func TestIssueInputPreparationPublishedHelpExamplesExecuteForAgents(t *testing.T) {
	t.Parallel()
	const definition = `{"fieldId":"customfield_1","key":"customfield_1","name":"Choice","required":false,"operations":["set"],"schema":{"type":"option","custom":"select"}}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Error("published authoring example attempted a write")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/rest/api/3/issue/KEY-123/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"customfield_1":`+definition+`}}`)
		case "/rest/api/3/project/KEY":
			_, _ = io.WriteString(w, `{"id":"10402","key":"KEY"}`)
		case "/rest/api/3/issue/createmeta/10402/issuetypes":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10044","name":"Story"}]}`)
		case "/rest/api/3/issue/createmeta/10402/issuetypes/10044":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"fields":[`+definition+`]}`)
		default:
			t.Errorf("published example requested an unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "authored.json")
	if err := os.WriteFile(input, []byte(`{"fields":{"customfield_1":"123"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(editReceiptEnv(server.URL), "AGENT=1", "JIRA_READ_ONLY=true")
	help, stderr, exit := runBinaryEnv(t, env, "issue", "input", "prepare", "--help", "--color=never")
	if exit != 0 || stderr != "" || requests.Load() != 0 {
		t.Fatal("local help failed or opened Jira")
	}
	count := 0
	for line := range strings.SplitSeq(help, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		commandText, destination, redirects := strings.Cut(strings.TrimPrefix(line, "$ jira "), " > ")
		args, err := shell.Split(commandText)
		if err != nil {
			t.Fatal(err)
		}
		for i, arg := range args {
			switch arg {
			case "authored.json", "editmeta.json", "createmeta.json":
				args[i] = filepath.Join(dir, arg)
			}
		}
		before := requests.Load()
		stdout, stderr, exit := runBinaryEnv(t, env, args...)
		if exit != 0 || stderr != "" {
			t.Fatalf("published authoring example failed: %q exit=%d stderr=%q", line, exit, stderr)
		}
		if redirects {
			if destination != "editmeta.json" && destination != "createmeta.json" && destination != "prepared.json" {
				t.Fatal("published example redirects to an unowned name")
			}
			if err := os.WriteFile(filepath.Join(dir, destination), []byte(stdout), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var value map[string]json.RawMessage
		if json.Unmarshal([]byte(stdout), &value) != nil || value == nil {
			t.Fatalf("published agent extraction did not produce its named JSON object: %q stdout=%q", line, stdout)
		}
		if args[1] == "input" {
			if requests.Load() != before {
				t.Fatal("local preparation example opened Jira")
			}
			payload := []byte(stdout)
			if !redirects {
				var out envelope.IssueInputPreparationOutput
				if err := json.Unmarshal(adfData(t, stdout, "json", "issue.input.prepare", true), &out); err != nil {
					t.Fatal(err)
				}
				payload = out.Payload
			}
			if string(bytes.TrimSpace(payload)) != `{"fields":{"customfield_1":{"value":"123"}}}` {
				t.Fatalf("published example lost the prepared identity: %s", payload)
			}
		} else if len(value) != 1 || value["fields"] == nil {
			t.Fatal("published metadata extraction did not produce the preparation snapshot")
		}
		count++
	}
	if count != 4 || requests.Load() != 4 {
		t.Fatalf("published examples changed without execution coverage: examples=%d requests=%d", count, requests.Load())
	}
}
