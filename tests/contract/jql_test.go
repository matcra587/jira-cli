package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	jqlParsedFixture    = `{"queries":[{"query":"normalized","errors":null,"warnings":["warning only"],"structure":{"future":{"large":9007199254740993,"text":"<&>"}}},{"query":"invalid","errors":["syntax error"],"warnings":[]},{"query":"normalized","errors":[]}],"future":{"large":9007199254740993,"text":"<&>"}}`
	jqlReferenceFixture = `{"visibleFieldNames":[{"value":"custom","displayName":"Custom field","cfid":"cf[12345]","searchable":"false","auto":null,"future":{"large":9007199254740993,"text":"<&>"}}],"visibleFunctionNames":[{"value":"currentUser()","displayName":"Current user","isList":"false"}],"jqlReservedWords":["and","or"],"future":false}`
)

func TestJQLValidateBinaryPreservesQueryDiagnosticsAndNative(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"strict", "warn", "none"} {
		for _, format := range []string{"json", "compact", "human"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				t.Parallel()
				var hits atomic.Int32
				queries := []string{"authored", "invalid", "authored"}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					var request struct {
						Queries []string `json:"queries"`
					}
					if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/jql/parse" || r.URL.Query().Get("validation") != mode || json.NewDecoder(r.Body).Decode(&request) != nil || !reflect.DeepEqual(request.Queries, queries) {
						t.Error("binary parser changed mode, order, duplicates or request route")
					}
					_, _ = io.WriteString(w, jqlParsedFixture)
				}))
				defer server.Close()
				args := append([]string{"jql", "validate"}, queries...)
				args = append(args, "--mode="+mode, "--output="+format, "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
				if exit != 0 || hits.Load() != 1 || stderr != "" {
					t.Fatalf("query errors became command failure: exit=%d calls=%d stderr=%q stdout=%s", exit, hits.Load(), stderr, stdout)
				}
				if format == "human" {
					for _, want := range []string{"Validation mode: " + mode, "errors: invalid", "error: syntax error", "warning: warning only", "none skips semantic validation"} {
						if !strings.Contains(stdout, want) {
							t.Fatalf("human diagnostics omitted %q: %s", want, stdout)
						}
					}
					return
				}
				data := adfData(t, stdout, format, "jql.validate", true)
				var out envelope.JQLValidateOutput
				if json.Unmarshal(data, &out) != nil || out.Mode != mode || !reflect.DeepEqual(out.SubmittedQueries, queries) || len(out.Queries) != 3 || !out.Queries[0].Valid || out.Queries[1].Valid || !out.Queries[2].Valid || !out.Queries[0].Errors.Null || out.Queries[1].Warnings.Value == nil || out.Queries[0].Query != "normalized" {
					t.Fatalf("binary diagnostics lost distinctions: %s", data)
				}
				if string(out.Parsed.Extra["future"]) != `{"large":9007199254740993,"text":"<&>"}` || string(out.Parsed.Queries.Value[0].Structure.Value) != `{"future":{"large":9007199254740993,"text":"<&>"}}` {
					t.Fatal("native parser response lost lexical value fidelity")
				}
			})
		}
	}
}

func TestJQLModeCompletionExecutesWithoutCredentials(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable for executing the generated completion script")
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("generating completion required credentials")
	}
	path := filepath.Join(t.TempDir(), "jira.bash")
	if err := os.WriteFile(path, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), bash, "--noprofile", "--norc", "-c", `source "$1"
COMP_WORDS=(jira jql validate --mode "")
COMP_CWORD=4
_jira jira "" --mode
printf '%s\n' "${COMPREPLY[@]}"`, "completion", path)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	result, err := cmd.CombinedOutput()
	if err != nil || string(result) != "strict\nwarn\nnone\n" {
		t.Fatalf("mode completion is empty or incorrect: %q %v", result, err)
	}
}

func TestJQLPublishedHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	var parses, references atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /rest/api/3/jql/parse":
			parses.Add(1)
			var input struct {
				Queries []string `json:"queries"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Queries) == 0 {
				t.Error("help example submitted no parser input")
				w.WriteHeader(400)
				return
			}
			queries := make([]map[string]any, len(input.Queries))
			for i, query := range input.Queries {
				errors := []string{}
				if query == "project = (" {
					errors = []string{"syntax error"}
				}
				queries[i] = map[string]any{"query": query, "errors": errors}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"queries": queries}); err != nil {
				t.Error(err)
			}
		case "GET /rest/api/3/jql/autocompletedata":
			references.Add(1)
			_, _ = io.WriteString(w, jqlReferenceFixture)
		case "GET /rest/agile/1.0/board/42/configuration":
			_, _ = io.WriteString(w, `{"id":42,"name":"Sprint","filter":{"id":"100"}}`)
		case "GET /rest/api/3/filter/100":
			_, _ = io.WriteString(w, `{"id":"100","jql":"project = KEY ORDER BY Rank ASC"}`)
		default:
			t.Error("help example reached an unrelated endpoint")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	env := boardCacheEnv(t, server.URL)
	env = append(env, "AGENT=1")
	group := jqlcmd.New(jqlcmd.Deps{})
	commands := append([]*cobra.Command{group}, group.Commands()...)
	count := 0
	for _, command := range commands {
		for line := range strings.SplitSeq(command.Example, "\n") {
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil || len(args) == 0 {
				t.Fatal("published JQL example is not an executable invocation")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("published JQL example failed: exit=%d stdout=%s stderr=%q", exit, stdout, stderr)
			}
			format, op := "compact", "jql.validate"
			if strings.Contains(line, "--output=json") {
				format = "json"
			}
			if strings.Contains(line, "jql reference") {
				op = "jql.reference"
			}
			if strings.Contains(line, "jql build") {
				op = "jql.build"
			}
			if strings.Contains(line, "issue list") {
				op = "issue.list"
			}
			adfData(t, stdout, format, op, true)
			count++
		}
	}
	if count != 11 || parses.Load() != 4 || references.Load() != 2 {
		t.Fatal("published JQL examples changed without executed coverage")
	}
}

func TestJQLBinaryJQProjectsDiagnosticsAndNativeDiscovery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/rest/api/3/jql/parse" {
			_, _ = io.WriteString(w, jqlParsedFixture)
		} else if r.Method == "GET" && r.URL.Path == "/rest/api/3/jql/autocompletedata" {
			_, _ = io.WriteString(w, jqlReferenceFixture)
		} else {
			t.Error("jq invocation addressed an unrelated route")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	for _, format := range []string{"json", "compact"} {
		prefix := "."
		if format == "json" {
			prefix = ".data."
		}
		for _, test := range []struct {
			args        []string
			query, want string
		}{
			{[]string{"jql", "validate", "authored", "invalid", "authored"}, prefix + `queries[] | select(.valid == false) | .query`, "invalid\n"},
			{[]string{"jql", "reference"}, prefix + `reference.visibleFunctionNames[].value`, "currentUser()\n"},
		} {
			args := append(test.args, "--output="+format, "--jq", test.query)
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1", "JIRA_READ_ONLY=true"), args...)
			if exit != 0 || stderr != "" || stdout != test.want {
				t.Fatalf("jq projected the wrong document or treated query diagnostics as failure: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
		}
	}
}

func TestJQLDashQueryRecoveryExecutes(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	query := "-starts-with-dash"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var input struct {
			Queries []string `json:"queries"`
		}
		if r.Method != "POST" || r.URL.Path != "/rest/api/3/jql/parse" || json.NewDecoder(r.Body).Decode(&input) != nil || !reflect.DeepEqual(input.Queries, []string{query}) {
			t.Error("positional terminator changed the authored query")
		}
		_, _ = io.WriteString(w, `{"queries":[{"query":"-starts-with-dash","errors":["syntax error"]}]}`)
	}))
	defer server.Close()
	env := append(editReceiptEnv(server.URL), "AGENT=1", "JIRA_READ_ONLY=true")
	stdout, _, exit := runBinaryEnv(t, env, "jql", "validate", query, "--output=json")
	var failed apiFrame
	if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &failed) != nil || failed.OK || len(failed.Errors) != 1 || failed.Errors[0].Code != cli.CodeFlagUnknown || hits.Load() != 0 {
		t.Fatal("unquoted dash query was silently reinterpreted or reached HTTP")
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "jql", "validate", "--output=json", "--", query)
	var result envelope.JQLValidateOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "jql.validate", true), &result) != nil || !reflect.DeepEqual(result.SubmittedQueries, []string{query}) || len(result.Queries) != 1 || result.Queries[0].Valid || hits.Load() != 1 {
		t.Fatal("documented positional terminator failed to recover the original parser task")
	}
}

func TestJQLBinaryDeadlineAndInterruptRemainFailures(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"validate", "reference"} {
		for _, failure := range []string{"timeout", "canceled"} {
			for _, format := range []string{"json", "compact"} {
				t.Run(verb+"/"+failure+"/"+format, func(t *testing.T) {
					if failure == "canceled" && runtime.GOOS == "windows" {
						t.Skip("os.Interrupt is not supported for subprocesses on Windows")
					}
					t.Parallel()
					started := make(chan struct{})
					var once sync.Once
					var hits atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							t.Error(err)
						}
						hits.Add(1)
						once.Do(func() { close(started) })
						<-r.Context().Done()
					}))
					defer server.Close()
					args := []string{"jql", verb, "--output=" + format}
					if verb == "validate" {
						args = append(args, "one")
					}
					wantExit := cli.ExitTimeout
					wantHits := int32(3) // Shared safe reads exhaust the three-attempt budget.
					var onStart func(*os.Process)
					if failure == "timeout" {
						args = append(args, "--timeout=750ms")
					} else {
						wantExit = cli.ExitCanceled
						wantHits = 1
						onStart = func(process *os.Process) {
							select {
							case <-started:
							case <-time.After(5 * time.Second):
								t.Fatal("JQL request did not start before interruption")
							}
							if err := process.Signal(os.Interrupt); err != nil {
								t.Fatal(err)
							}
						}
					}
					stdout, stderr, exit := runBinaryEnvStarted(t, append(editReceiptEnv(server.URL), "AGENT=1", "JIRA_READ_ONLY=true"), onStart, args...)
					var result apiFrame
					if exit != wantExit || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != failure || hits.Load() != wantHits {
						t.Fatalf("JQL interruption became success or lost its cause: exit=%d hits=%d stdout=%s stderr=%q", exit, hits.Load(), stdout, stderr)
					}
				})
			}
		}
	}
}

func TestJQLReferenceBinaryPreservesLiveMetadata(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "compact", "human"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/jql/autocompletedata" || len(r.URL.Query()) != 0 {
					t.Error("reference became local/static or used the wrong endpoint")
				}
				_, _ = io.WriteString(w, jqlReferenceFixture)
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "jql", "reference", "--output="+format, "--no-input")
			if exit != 0 || hits.Load() != 1 || stderr != "" {
				t.Fatalf("live reference failed: exit=%d calls=%d stdout=%s stderr=%q", exit, hits.Load(), stdout, stderr)
			}
			if format == "human" {
				for _, want := range []string{"custom", "Custom field", "cf[12345]", "currentUser()", "and", "or", "missing entries do not prove absence"} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("human metadata omitted %q", want)
					}
				}
				return
			}
			data := adfData(t, stdout, format, "jql.reference", true)
			var out envelope.JQLReferenceOutput
			if json.Unmarshal(data, &out) != nil || len(out.Fields) != 1 || out.Fields[0].CustomFieldID.Value != "cf[12345]" || len(out.Functions) != 1 || len(out.ReservedWords.Value) != 2 || out.Reference.VisibleFieldNames.Value[0].Searchable.Value != "false" || !out.Reference.VisibleFieldNames.Value[0].Auto.Null || string(out.Reference.VisibleFieldNames.Value[0].Extra["future"]) != `{"large":9007199254740993,"text":"<&>"}` || string(out.Reference.Extra["future"]) != "false" {
				t.Fatalf("native metadata was projected away or coerced: %s", data)
			}
		})
	}
}

func TestJQLBinaryFailuresRemainTypedAndNonzero(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"validate", "reference"} {
		for _, format := range []string{"json", "compact"} {
			for _, test := range []struct {
				status     int
				body, code string
				exit       int
			}{
				{400, `{"errorMessages":["bad request"]}`, "jira_bad_request", cli.ExitValidation}, {401, `{}`, "jira_unauthorized", cli.ExitAuth}, {403, `{}`, "jira_forbidden", cli.ExitAuth}, {429, `{}`, "jira_rate_limited", cli.ExitRateLimit}, {503, `{}`, "jira_server_error", cli.ExitServer}, {200, `null`, cli.CodeJQLResponseInvalid, cli.ExitServer}, {200, `{} {}`, cli.CodeJQLResponseInvalid, cli.ExitServer},
			} {
				t.Run(verb+"/"+format+"/"+test.code+test.body, func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(test.status)
						_, _ = io.WriteString(w, test.body)
					}))
					defer server.Close()
					args := []string{"jql", verb}
					if verb == "validate" {
						args = append(args, "one")
					}
					args = append(args, "--output="+format)
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					var out apiFrame
					if exit != test.exit || json.Unmarshal([]byte(stdout), &out) != nil || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != test.code || bytes.Contains(out.Data, []byte(`"valid":true`)) {
						t.Fatalf("read failure fabricated success: exit=%d stdout=%s stderr=%q", exit, stdout, stderr)
					}
				})
			}
		}
	}
}

func TestJQLBinaryLocalRecoveryAndDiscoveryNeedNoCredentials(t *testing.T) {
	t.Parallel()
	env := []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "AGENT=1"}
	for _, test := range []struct {
		args        []string
		code, field string
	}{
		{[]string{"jql", "validate", "one", "--mode=STRICT"}, cli.CodeFlagValueInvalid, "mode"}, {[]string{"jql", "validate", ""}, cli.CodeArgValueInvalid, "QUERY"}, {[]string{"jql", "validate"}, cli.CodeArgCountInvalid, ""}, {[]string{"jql", "reference", "extra"}, cli.CodeArgCountInvalid, ""},
	} {
		stdout, stderr, exit := runBinaryEnv(t, env, append(test.args, "--output=json")...)
		var out apiFrame
		if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &out) != nil || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != test.code || (test.field != "" && out.Errors[0].Field != test.field) {
			t.Fatalf("local refusal reached credentials or lost recovery: exit=%d stdout=%s stderr=%q", exit, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"jql", "validate", "--help"}, {"jql", "reference", "--help"}, {"agent", "schema", "--path=jira jql validate", "--output=json"}} {
		stdout, stderr, exit := runBinaryEnv(t, env, args...)
		if exit != 0 || stdout == "" || stderr != "" {
			t.Fatalf("local discovery needed credentials: %q exit=%d stderr=%q", args, exit, stderr)
		}
		if args[0] == "agent" {
			tree, _ := readSchema(t, stdout, "json")
			if tree.Path != "jira jql validate" || tree.OutputSchema == nil || !strings.Contains(stdout, `strict`) || !strings.Contains(stdout, `warn`) || !strings.Contains(stdout, `none`) {
				t.Fatal("mode or output contract missing from runtime discovery")
			}
		}
	}
}
