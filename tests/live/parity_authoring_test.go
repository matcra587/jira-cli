//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const parityAuthoringDocument = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Owned café","marks":[{"type":"strong"}]}]}]}`

// This oracle knows only independently authored JSON, including exact numeric
// tokens. It does not call the CLI's ADF converter, JQL builder or native models.
func parityAuthoringJSON(actual, expected []byte) bool {
	decode := func(raw []byte) (any, bool) {
		if !jsontext.Value(raw).IsValid() {
			return nil, false
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		var extra any
		return value, errors.Is(decoder.Decode(&extra), io.EOF)
	}
	a, aOK := decode(actual)
	b, bOK := decode(expected)
	return aOK && bOK && reflect.DeepEqual(a, b)
}

func parityOriginalFrameData(stdout []byte) json.RawMessage {
	if !jsontext.Value(stdout).IsValid() {
		return nil
	}
	var frame struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return nil
	}
	return frame.Data
}

func parityAuthoringData(t *testing.T, stdout []byte, lane, operation string, exit int) map[string]json.RawMessage {
	t.Helper()
	if !jsontext.Value(stdout).IsValid() {
		t.Fatal("authoring envelope contains duplicate names or malformed JSON")
	}
	if err := parityValidateFrame(stdout, lane, operation, exit); err != nil {
		t.Fatalf("invalid selected-binary %s result: %v; %s", operation, err, stdout)
	}
	var frame struct{ Data map[string]json.RawMessage }
	if json.Unmarshal(stdout, &frame) != nil {
		t.Fatal("missing authoring result")
	}
	return frame.Data
}

func parityAuthoringCleanData(t *testing.T, stdout []byte, lane, operation string) map[string]json.RawMessage {
	t.Helper()
	data := parityAuthoringData(t, stdout, lane, operation, 0)
	var frame struct{ Warnings json.RawMessage }
	if json.Unmarshal(stdout, &frame) != nil || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) {
		t.Fatal("clean authoring result contains contradictory warnings")
	}
	return data
}

func parityAuthoringDocumentResult(t *testing.T, stdout []byte, lane, mode string) {
	t.Helper()
	if !jsontext.Value(stdout).IsValid() {
		t.Fatal("authoring output contains duplicate names or malformed JSON")
	}
	document := stdout
	var data map[string]json.RawMessage
	if mode == "json" {
		data = parityAuthoringCleanData(t, stdout, lane, "adf.convert")
		if lane == "before" {
			document, _ = json.Marshal(data)
		}
	} else if mode == "compact" && lane == "after" {
		if json.Unmarshal(stdout, &data) != nil {
			t.Fatal("invalid compact authoring wrapper")
		}
	}
	if lane == "after" {
		document = data["document"]
		if string(data["lossy"]) != "false" || !parityAuthoringJSON(data["diagnostics"], []byte(`[]`)) {
			t.Fatal("clean current conversion lost explicit diagnostics")
		}
	}
	if !parityAuthoringJSON(document, []byte(parityAuthoringDocument)) {
		t.Fatalf("conversion differs from the independently authored native document: %s", stdout)
	}
}

func TestParityActualBinaryLocalAuthoring(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			home := t.TempDir()
			markdown := filepath.Join(home, "owned.md")
			document := filepath.Join(home, "owned.json")
			for path, data := range map[string]string{markdown: "**Owned café**", document: parityAuthoringDocument} {
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			invoke := func(input parityFixtureInput, exit int, args ...string) []byte {
				t.Helper()
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, input, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
				receipt := parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr, InputBytes: input.Stdin}
				evidence.Invocations = append(evidence.Invocations, receipt)
				if result.Exit != exit || calls.Load() != 0 {
					t.Fatalf("local invocation differs: exit %d, want %d; HTTP %d; stdout %s; stderr %s", result.Exit, exit, calls.Load(), result.Stdout, result.Stderr)
				}
				return result.Stdout
			}
			check := func(name string) { evidence.Checks[name] = append(evidence.Checks[name], lane.name) }
			for _, tc := range []struct {
				name string
				args []string
				want string
			}{
				{"default", nil, "updated >= -365d ORDER BY updated DESC"},
				{"structured", []string{"--project=KEY", "--assignee=me", "--status=Open", "--order-by=key", "--desc=false"}, "project = KEY AND assignee = currentUser() AND status = Open ORDER BY key ASC"},
			} {
				if tc.name == "structured" && lane.name == "after" {
					tc.want = `project in ("KEY") AND assignee = currentUser() AND status in ("Open") ORDER BY key ASC`
				}
				stdout := invoke(parityFixtureInput{}, 0, append([]string{"jql", "build"}, tc.args...)...)
				data := parityAuthoringCleanData(t, stdout, lane.name, "jql.build")
				var query, browse, precedence string
				var board struct{ Applied *bool }
				if json.Unmarshal(data["jql"], &query) != nil || query != tc.want || json.Unmarshal(data["url"], &browse) != nil || json.Unmarshal(data["precedence"], &precedence) != nil || precedence != "none" || json.Unmarshal(data["board_scope"], &board) != nil || board.Applied == nil || *board.Applied {
					t.Fatalf("local JQL differs from the authored lane projection: %s", stdout)
				}
				parsed, err := url.Parse(browse)
				if err != nil || parsed.Scheme+"://"+parsed.Host != server.URL || parsed.Path != "/issues/" || parsed.Query().Get("jql") != tc.want || len(parsed.Query()) != 1 || parsed.Fragment != "" || parsed.User != nil {
					t.Fatalf("JQL browser URL differs: %q", browse)
				}
				check("C057." + tc.name)
			}
			for _, source := range []string{"file", "stdin"} {
				for _, mode := range []string{"json", "compact"} {
					input := parityFixtureInput{Output: mode}
					path := markdown
					if source == "stdin" {
						path, input.Stdin = "-", []byte("**Owned café**")
					}
					stdout := invoke(input, 0, "adf", "convert", "--input", path)
					parityAuthoringDocumentResult(t, stdout, lane.name, mode)
					check("C003." + source + "." + mode)
					path = document
					if source == "stdin" {
						path, input.Stdin = "-", []byte(parityAuthoringDocument)
					}
					stdout = invoke(input, 0, "adf", "render", "--input", path)
					var data map[string]json.RawMessage
					if mode == "json" {
						data = parityAuthoringCleanData(t, stdout, lane.name, "adf.render")
					} else if !jsontext.Value(stdout).IsValid() || json.Unmarshal(stdout, &data) != nil {
						t.Fatal("invalid compact projection")
					}
					var projection string
					losses := "null"
					if lane.name == "after" {
						losses = "[]"
					}
					validLosses := parityAuthoringJSON(data["lossy_constructs"], []byte(losses))
					if mode == "compact" && lane.name == "before" {
						_, present := data["lossy_constructs"]
						validLosses = !present
					}
					if json.Unmarshal(data["markdown"], &projection) != nil || projection != "**Owned café**\n" || string(data["lossy"]) != "false" || !validLosses {
						t.Fatalf("native Markdown projection differs: %s", stdout)
					}
					check("C004." + source + "." + mode)
				}
			}
			for _, tc := range []struct{ verb, expression, expected string }{
				{"convert", ".data", parityAuthoringDocument},
				{"render", ".data.markdown", "**Owned café**\n\n"},
			} {
				path, id := markdown, "C003"
				if tc.verb == "render" {
					path, id = document, "C004"
				} else if lane.name == "after" {
					tc.expression = ".data.document"
				}
				stdout := invoke(parityFixtureInput{}, 0, "adf", tc.verb, "--input", path, "--jq", tc.expression)
				if (tc.verb == "convert" && !parityAuthoringJSON(stdout, []byte(tc.expected))) || (tc.verb == "render" && string(stdout) != tc.expected) {
					t.Fatalf("explicit extraction changed its literal result: %q", stdout)
				}
				check(id + ".jq")
			}
			for _, policy := range []string{"strict-default", "best-effort", "strict-false"} {
				args := []string{"adf", "convert", "--input=-"}
				exit, code := 3, "markdown_lossy_conversion"
				if lane.name == "after" {
					code = "adf_content_loss"
				}
				switch policy {
				case "best-effort":
					args = append(args, "--adf-best-effort")
					exit = 0
				case "strict-false":
					args = append(args, "--adf-strict=false")
					if lane.name == "after" {
						exit = 0
					}
				}
				stdout := invoke(parityFixtureInput{Stdin: []byte("**Owned café**\n\n<script>dropped</script>\n")}, exit, args...)
				data := parityAuthoringData(t, stdout, lane.name, "adf.convert", exit)
				var frame struct {
					Errors   []struct{ Code string }
					Warnings []struct {
						Type    string
						Lossy   *bool
						Message string
					}
				}
				if json.Unmarshal(stdout, &frame) != nil || (exit != 0 && (len(frame.Errors) != 1 || frame.Errors[0].Code != code)) {
					t.Fatal("lossy conversion has the wrong refusal classification")
				}
				if exit == 0 || lane.name == "after" {
					doc, _ := json.Marshal(data)
					warnings := frame.Warnings
					if lane.name == "after" {
						doc = data["document"]
						if string(data["lossy"]) != "true" || json.Unmarshal(data["diagnostics"], &warnings) != nil {
							t.Fatal("current loss handling discarded the candidate diagnostics")
						}
					}
					if !parityAuthoringJSON(doc, []byte(parityAuthoringDocument)) || len(warnings) != 1 || warnings[0].Type != "markdown_lossy_conversion" || warnings[0].Lossy == nil || !*warnings[0].Lossy || !strings.Contains(warnings[0].Message, "HTML") {
						t.Fatalf("loss acceptance or retained refusal hid dropped HTML: %s", stdout)
					}
				}
				check("C003.loss." + policy)
			}
			for _, invalid := range []string{`{}`, `null`, `{"body":` + parityAuthoringDocument + `}`} {
				exit := 0
				if lane.name == "after" {
					exit = 3
				}
				stdout := invoke(parityFixtureInput{Stdin: []byte(invalid)}, exit, "adf", "render", "--input=-")
				data := parityAuthoringData(t, stdout, lane.name, "adf.render", exit)
				if lane.name == "before" {
					if !parityAuthoringJSON(data["markdown"], []byte(`"\n"`)) || string(data["lossy"]) != "false" {
						t.Fatal("legacy invalid-root behavior differs from the recorded permissive projection")
					}
				} else {
					var frame struct {
						Data   json.RawMessage
						Errors []struct{ Code, Field string }
					}
					if json.Unmarshal(stdout, &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != "input" || string(frame.Data) != "null" {
						t.Fatal("current invalid root lacks its typed refusal")
					}
				}
				check("C004.invalid-root." + invalid[:min(len(invalid), 7)])
			}
			stdout := invoke(parityFixtureInput{}, 0, "version")
			data := parityAuthoringCleanData(t, stdout, lane.name, "version")
			want := os.Getenv("JIRA_PARITY_" + strings.ToUpper(lane.name) + "_VERSION_DATA")
			encoded, err := json.Marshal(data)
			if want == "" || err != nil || !parityAuthoringJSON(encoded, []byte(want)) {
				t.Fatal("version provenance differs from the independently admitted selected artifact")
			}
			check("C070.json-provenance")
			var version struct{ Version string }
			if json.Unmarshal([]byte(want), &version) != nil || version.Version == "" {
				t.Fatal("admitted version lacks its version string")
			}
			for _, mode := range []string{"compact", "human"} {
				stdout := invoke(parityFixtureInput{Output: mode}, 0, "version")
				if mode == "compact" {
					if !parityAuthoringJSON(stdout, []byte(want)) {
						t.Fatal("compact version changed admitted provenance")
					}
				} else {
					human := version.Version
					if lane.name == "before" {
						human = strings.TrimPrefix(human, "v")
					}
					if string(stdout) != human+"\n" {
						t.Fatalf("human version differs from its declared spelling: %q", stdout)
					}
				}
				check("C070." + mode)
			}
			stdout = invoke(parityFixtureInput{}, 0, "version", "--jq=.data.version")
			if string(stdout) != version.Version+"\n" {
				t.Fatal("version extraction changed admitted identity")
			}
			check("C070.jq")
			for path, want := range map[string]string{markdown: "**Owned café**", document: parityAuthoringDocument} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatal("local authoring changed an input file")
				}
			}
		})
	}
	saveParityLocal(t, "authoring", evidence)
}

func TestParityAuthoringOracleRejectsChangedData(t *testing.T) {
	for _, changed := range []string{`null`, `{}`, strings.Replace(parityAuthoringDocument, "Owned café", "different", 1), parityAuthoringDocument + `{}`} {
		if parityAuthoringJSON([]byte(changed), []byte(parityAuthoringDocument)) {
			t.Fatal("authoring oracle accepts changed or incomplete native data")
		}
	}
	if parityAuthoringJSON([]byte(`{"future":9007199254740993}`), []byte(`{"future":9007199254740992}`)) {
		t.Fatal("authoring oracle rounds native numbers")
	}
}

func TestParityAuthoringOracleRejectsDuplicateNames(t *testing.T) {
	for _, tc := range []struct{ name, actual, expected string }{
		{"root", `{"type":"wrong","type":"doc"}`, `{"type":"doc"}`},
		{"nested", `{"content":[{"text":"wrong","text":"Owned café"}]}`, `{"content":[{"text":"Owned café"}]}`},
		{"escaped", `{"type":"wrong","t\u0079pe":"doc"}`, `{"type":"doc"}`},
		{"envelope", `{"data":{"type":"wrong","type":"doc"}}`, `{"data":{"type":"doc"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityAuthoringJSON([]byte(tc.actual), []byte(tc.expected)) {
				t.Fatal("duplicate native member silently earned authoring credit")
			}
		})
	}
}
