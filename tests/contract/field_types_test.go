package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func fieldTypesOutput(t *testing.T, stdout, mode string) envelope.FieldTypesOutput {
	t.Helper()
	var out envelope.FieldTypesOutput
	payload := adfData(t, stdout, mode, "agent.fieldtypes", true)
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFieldTypesBinaryModesAndHandling(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_READ_ONLY=true", "AGENT=1"}
	var first *envelope.FieldTypesOutput
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "agent", "fieldtypes", "--output="+mode)
		if exit != 0 || stderr != "" {
			t.Fatalf("field discovery failed: exit=%d stderr=%q", exit, stderr)
		}
		out := fieldTypesOutput(t, stdout, mode)
		if !reflect.DeepEqual(out.Types, jira.FieldTypeExamples()) || out.Metadata == nil || len(out.Metadata) != 3 || out.OfficialURL == "" {
			t.Fatal("field discovery lost native examples or metadata routes")
		}
		h := out.Handling
		if h.InputFlag != "json-input" || h.Encoding != "native-json" || !h.PreserveValues || h.TypeValidation || h.ScalarConversion || h.UnknownTypes != "native-json" || h.NullValues != "explicit-null" || h.EmptyArrays != "empty-array" {
			t.Fatal("field discovery claimed unavailable conversion/validation or lost native handling")
		}
		if first == nil {
			first = &out
		} else if !reflect.DeepEqual(*first, out) {
			t.Fatal("machine modes disagree on field handling")
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "agent", "fieldtypes")
	if exit != 0 || stderr != "" {
		t.Fatal("agent field discovery failed")
	}
	if got := fieldTypesOutput(t, stdout, "compact"); !reflect.DeepEqual(got, *first) {
		t.Fatal("agent default changed native discovery")
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "agent", "fieldtypes", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Native field values") || !strings.Contains(stdout, "cascadingselect") || !strings.Contains(stdout, "Jira validates") || requests.Load() != 0 {
		t.Fatal("human field discovery lost its scope or contacted Jira")
	}
}

func TestFieldTypesExamplesPreserveNativeValuesThroughWrites(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "fieldtypes", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatal("discovery failed before native-value tests")
	}
	out := fieldTypesOutput(t, stdout, "json")
	fields := make(map[string]json.RawMessage)
	for _, row := range out.Types {
		// Family names deliberately serve as unmodeled keys. The native owner
		// must not require its own catalog or a tenant-specific field ID.
		fields["fixture_"+row.Name] = row.Example
	}
	for name, raw := range map[string]string{
		"fixture_null": "null", "fixture_empty": "[]", "fixture_boolean": "false",
		"fixture_number": "9007199254740993", "fixture_scalar_select": `"unwrapped"`,
		"fixture_invalid_date": `"2026-02-30"`,
		"fixture_unknown":      `{"futureValue":{"raw":"<>&\u2028","line":"\u2029"},"items":[null,[],false,0]}`,
	} {
		fields[name] = json.RawMessage(raw)
	}
	request := jira.IssueWriteRequest{Fields: fields, Update: map[string]json.RawMessage{"fixture_update": json.RawMessage(`[{"set":{"unknownValue":9007199254740993}},{"remove":null}]`)}}
	body, err := jira.MarshalNoEscape(request)
	if err != nil {
		t.Fatal(err)
	}
	input := apiInput(t, body)
	// Raw native JSON retains its authored string spelling on both the wire
	// and the machine receipt; ordinary Go strings still emit literal separators.
	receipt := body
	var puts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-123" {
			t.Error("native edit changed its request route")
		}
		if !bytes.Equal(got, body) {
			t.Error("native edit changed raw field values, null/empty distinctions or update operations")
		}
		puts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, mode := range []string{"json", "compact"} {
		for _, preview := range []bool{true, false} {
			args := []string{"issue", "edit", "KEY-123", "--json-input", input, "--no-input", "--output=" + mode}
			if preview {
				args = append(args, "--dry-run")
			}
			before := puts.Load()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("native examples failed: preview=%t exit=%d stderr=%q", preview, exit, stderr)
			}
			var edited envelope.IssueEditOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "issue.edit", true), &edited); err != nil {
				t.Fatal(err)
			}
			if edited.DryRun != preview || !bytes.Equal(edited.Payload, receipt) || len(edited.Results) != 1 || !edited.Results[0].OK || bytes.Contains(edited.Payload, []byte("fixture_absent")) {
				t.Fatalf("native preview/write receipt changed the advertised field values: mode=%s preview=%t", mode, preview)
			}
			want := before
			if !preview {
				want++
			}
			if puts.Load() != want {
				t.Fatal("preview wrote to Jira or native write was omitted/replayed")
			}
		}
	}
}

func TestFieldTypesHelpExamplesRefusalsAndMetadataRoutes(t *testing.T) {
	t.Parallel()
	help, _, exit := runBinary(t, "agent", "fieldtypes", "--help", "--color=never")
	if exit != 0 {
		t.Fatal("field discovery help failed")
	}
	count := 0
	for line := range strings.SplitSeq(help, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinary(t, args...)
		if exit != 0 || stderr != "" {
			t.Fatalf("published field discovery example failed: %q", line)
		}
		if strings.Contains(line, "--jq") {
			if stdout != "{\"accountId\":\"example-account-id\"}\n" {
				t.Fatal("field example extraction changed Jira's native object shape")
			}
		} else {
			_ = fieldTypesOutput(t, stdout, "json")
		}
		count++
	}
	if count != 2 {
		t.Fatal("field discovery examples changed without execution coverage")
	}
	for _, mode := range []string{"json", "compact"} {
		stdout, _, exit := runBinary(t, "agent", "fieldtypes", "unexpected", "--output="+mode)
		var failed frame
		var failure envelope.Error
		if exit != 3 || json.Unmarshal([]byte(stdout), &failed) != nil || failed.OK || failed.Meta.Command != "agent.fieldtypes" || len(failed.Errors) != 1 || json.Unmarshal(failed.Errors[0], &failure) != nil || failure.Code != "arg_count_invalid" {
			t.Fatal("field discovery accepted an operand or lost the full refusal frame")
		}
	}
	stdout, _, exit := runBinary(t, "agent", "schema", "--path=agent fieldtypes", "--output=json")
	if exit != 0 || !strings.Contains(stdout, "native_shape") || !strings.Contains(stdout, "type_validation") || !strings.Contains(stdout, "scalar_conversion") {
		t.Fatal("runtime schema lost the field discovery shape")
	}
	stdout, _, exit = runBinary(t, "agent", "fieldtypes", "--output=json")
	if exit != 0 {
		t.Fatal("field discovery failed before metadata execution")
	}
	out := fieldTypesOutput(t, stdout, "json")
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("metadata discovery attempted a mutation")
		}
		switch r.URL.Path {
		case "/rest/api/3/field", "/rest/api/3/issue/KEY-123/editmeta", "/rest/api/3/issue/createmeta/KEY/issuetypes/10044":
		default:
			t.Error("field discovery published an unexpected metadata route")
		}
		reads.Add(1)
		_, _ = io.WriteString(w, `{"fixtureMetadata":true}`)
	}))
	defer server.Close()
	for _, route := range out.Metadata {
		path := strings.ReplaceAll(strings.ReplaceAll(route.Path, "PROJECT", "KEY"), "TYPE_ID", "10044")
		stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "api", "get", path, "--output=json")
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "fixtureMetadata") {
			t.Fatal("published metadata route failed through the read-only API owner")
		}
	}
	if reads.Load() != 3 {
		t.Fatal("metadata examples were omitted or repeated")
	}
}
