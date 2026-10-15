//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const parityParseFixture = `{"queries":[{"query":"normalized owned","errors":null,"warnings":["warning only"],"structure":{"future":{"number":900719925474099312345}}},{"query":"invalid owned","errors":["syntax error"],"warnings":[]},{"query":"normalized owned","errors":[]}],"future":{"number":900719925474099312345}}`

func parityParseProjection(data, native []byte, submitted []string, mode, lane string) bool {
	if (lane != "before" && lane != "after") || (mode != "strict" && mode != "warn" && mode != "none") || len(submitted) == 0 || !jsontext.Value(data).IsValid() || !jsontext.Value(native).IsValid() {
		return false
	}
	var source struct{ Queries []map[string]json.RawMessage }
	if json.Unmarshal(native, &source) != nil || len(source.Queries) != len(submitted) {
		return false
	}
	queries := make([]map[string]json.RawMessage, len(source.Queries))
	for i, entry := range source.Queries {
		var query string
		if entry == nil || string(entry["query"]) == "null" || json.Unmarshal(entry["query"], &query) != nil {
			return false
		}
		row := map[string]json.RawMessage{"query": entry["query"]}
		valid := true
		for _, name := range []string{"errors", "warnings"} {
			raw, present := entry[name]
			var messages []string
			if present && string(raw) != "null" {
				var values []json.RawMessage
				if json.Unmarshal(raw, &values) != nil || values == nil {
					return false
				}
				for _, value := range values {
					var text string
					if string(value) == "null" || json.Unmarshal(value, &text) != nil {
						return false
					}
					messages = append(messages, text)
				}
			}
			if name == "errors" {
				valid = len(messages) == 0
			}
			if (lane == "after" && present) || (lane == "before" && len(messages) != 0) {
				row[name] = raw
			}
		}
		row["valid"], _ = json.Marshal(valid)
		queries[i] = row
	}
	want := map[string]any{"queries": queries}
	if lane == "after" {
		want["mode"], want["submitted_queries"], want["parsed"] = mode, submitted, json.RawMessage(native)
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func TestParityParseProjectionRejectsFalseDiagnostics(t *testing.T) {
	const legacy = `{"queries":[{"query":"normalized owned","valid":true,"warnings":["warning only"]},{"query":"invalid owned","valid":false,"errors":["syntax error"]},{"query":"normalized owned","valid":true}]}`
	const current = `{"queries":[{"query":"normalized owned","valid":true,"errors":null,"warnings":["warning only"]},{"query":"invalid owned","valid":false,"errors":["syntax error"],"warnings":[]},{"query":"normalized owned","valid":true,"errors":[]}],"mode":"strict","submitted_queries":["-authored","invalid","-authored"],"parsed":` + parityParseFixture + `}`
	inputs := []string{"-authored", "invalid", "-authored"}
	for _, tc := range []struct{ lane, data string }{{"before", legacy}, {"after", current}} {
		t.Run(tc.lane, func(t *testing.T) {
			if !parityParseProjection([]byte(tc.data), []byte(parityParseFixture), inputs, "strict", tc.lane) {
				t.Fatal("independent parser projection refused")
			}
			for _, changed := range []string{`{}`, strings.Replace(tc.data, `"valid":false`, `"valid":true`, 1), strings.Replace(tc.data, `"syntax error"`, `"different"`, 1), strings.Replace(tc.data, `"warning only"`, `"different"`, 1), strings.Replace(tc.data, `"query":"invalid owned"`, `"query":null`, 1), strings.Replace(tc.data, `"valid":false`, `"valid":true,"valid":false`, 1)} {
				if parityParseProjection([]byte(changed), []byte(parityParseFixture), inputs, "strict", tc.lane) {
					t.Fatal("parser oracle admitted false validity or changed diagnostics")
				}
			}
			if tc.lane == "after" {
				for _, changed := range []string{strings.Replace(current, `"errors":null`, `"errors":[]`, 1), strings.Replace(current, `900719925474099312345`, `900719925474099300000`, 1), strings.Replace(current, `"mode":"strict"`, `"mode":"none"`, 1), strings.Replace(current, `"-authored","invalid","-authored"`, `"invalid","-authored","-authored"`, 1)} {
					if parityParseProjection([]byte(changed), []byte(parityParseFixture), inputs, "strict", "after") {
						t.Fatal("current parser lost native presence, precision or submitted order")
					}
				}
			}
		})
	}
}

func parityParserRequest(r *http.Request, mode string, expected []byte) bool {
	user, password, auth := r.BasicAuth()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	return r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/jql/parse" && queryErr == nil && len(query) == 1 && slices.Equal(query["validation"], []string{mode}) && auth && user == "fixture@example.invalid" && password == "synthetic-fixture-token" && err == nil && len(body) <= 4096 && parityAuthoringJSON(body, expected)
}

func TestParityParserRequestRejectsAmbiguousBoundaries(t *testing.T) {
	const expected = `{"queries":["-authored"]}`
	for _, tc := range []struct {
		name, query, body string
		want              bool
	}{
		{"complete", "validation=strict", expected, true},
		{"duplicate mode", "validation=strict&validation=none", expected, false},
		{"hidden trailing document", "validation=strict", expected + strings.Repeat(" ", 4096-len(expected)) + `{}`, false},
		{"duplicate JSON member", "validation=strict", `{"queries":[],"queries":["-authored"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://fixture.invalid/rest/api/3/jql/parse?"+tc.query, strings.NewReader(tc.body))
			r.SetBasicAuth("fixture@example.invalid", "synthetic-fixture-token")
			if parityParserRequest(r, "strict", []byte(expected)) != tc.want {
				t.Fatal("request oracle admits an ambiguous query or incomplete bounded body")
			}
		})
	}
}

func TestParityActualBinaryJQLValidate(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	queries := []string{"-authored <&>", "project = (", "-authored <&>"}
	request, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			for _, selection := range []string{"default", "strict", "warn", "none", "read-only"} {
				mode := selection
				if mode == "default" || mode == "read-only" {
					mode = "strict"
				}
				for _, format := range []string{"json", "compact", "human"} {
					if selection == "read-only" && format != "json" {
						continue
					}
					t.Run(selection+"/"+format, func(t *testing.T) {
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							if !parityParserRequest(r, mode, request) {
								http.Error(w, "parser request changed literal argv or transport", http.StatusBadRequest)
								return
							}
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write([]byte(parityParseFixture))
						}))
						defer server.Close()
						args := []string{"jql", "validate"}
						if selection != "default" && selection != "read-only" {
							args = append(args, "--mode="+selection)
						}
						args = append(args, "--")
						args = append(args, queries...)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						policy := "JIRA_READ_ONLY=false"
						if selection == "read-only" {
							policy = "JIRA_READ_ONLY=true"
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, t.TempDir(), parityFixtureInput{Output: format}, []string{policy}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if selection == "read-only" && lane.name == "before" {
							if result.Exit != 3 || calls.Load() != 0 || !parityParseReadOnlyRefusal(result.Stdout) {
								t.Fatal("legacy read-only parser no longer has its recorded method-based refusal")
							}
							name := "C059.fixture." + selection + "." + format
							evidence.Checks[name] = append(evidence.Checks[name], lane.name)
							return
						}
						if result.Exit != 0 || calls.Load() != 1 {
							t.Fatalf("parser failed: exit %d; calls %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						if format == "human" {
							wants := []string{"OK (warnings)  normalized owned — warning only", "INVALID  invalid owned — syntax error", "OK  normalized owned"}
							if lane.name == "after" {
								wants = []string{"Validation mode: " + mode, "no errors: normalized owned", "warning: warning only", "errors: invalid owned", "error: syntax error", "no errors: normalized owned"}
							}
							remaining := string(result.Stdout)
							for _, want := range wants {
								_, after, found := strings.Cut(remaining, want)
								if !found {
									t.Fatalf("human diagnostics lost ordered text %q: %s", want, result.Stdout)
								}
								remaining = after
							}
						} else {
							data := result.Stdout
							if format == "json" {
								members := parityAuthoringCleanData(t, data, lane.name, "jql.validate")
								data, _ = json.Marshal(members)
							}
							if !parityParseProjection(data, []byte(parityParseFixture), queries, mode, lane.name) {
								t.Fatalf("parser output changed native diagnostics: %s", result.Stdout)
							}
						}
						name := "C059.fixture." + selection + "." + format
						evidence.Checks[name] = append(evidence.Checks[name], lane.name)
					})
				}
			}
			t.Run("invalid-mode", func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusForbidden)
				}))
				defer server.Close()
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, t.TempDir(), parityFixtureInput{}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, "jql", "validate", "--mode=STRICT", "--", queries[0])
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				var frame struct {
					Data   json.RawMessage
					Errors []struct{ Code, Field string }
				}
				code := "validation_failed"
				if lane.name == "after" {
					code = "flag_value_invalid"
				}
				if result.Exit != 3 || calls.Load() != 0 || !jsontext.Value(result.Stdout).IsValid() || parityValidateFrame(result.Stdout, lane.name, "jql.validate", 3) != nil || json.Unmarshal(result.Stdout, &frame) != nil || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != code || (lane.name == "after" && frame.Errors[0].Field != "mode") {
					t.Fatalf("invalid mode entered remote work or lost typed refusal: %s", result.Stdout)
				}
				evidence.Checks["C059.fixture.invalid-mode"] = append(evidence.Checks["C059.fixture.invalid-mode"], lane.name)
			})
		})
	}
	saveParityLocal(t, "jql-validate-fixture", evidence)
}

func parityParseReadOnlyRefusal(stdout []byte) bool {
	var frame struct {
		Data   json.RawMessage
		Errors []struct{ Code string }
	}
	return jsontext.Value(stdout).IsValid() && parityValidateFrame(stdout, "before", "jql.validate", 3) == nil && json.Unmarshal(stdout, &frame) == nil && string(frame.Data) == "null" && len(frame.Errors) == 1 && frame.Errors[0].Code == "read_only"
}

func TestLiveParityJQLValidate(t *testing.T) {
	p := newParityRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !liveKey.MatchString(key) || !strings.HasPrefix(key, p.owner.manifest.Project+"-") {
		t.Fatal("parser acceptance requires the admitted project's read fixture")
	}
	queries := []string{"key = " + key, "project = (", "key = " + key, "jira_cli_missing_field_parity_5af391 = 1"}
	request, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(p.owner.dir, "paired-parser-input.json")
	if err := os.WriteFile(input, request, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, lane := range p.evidence.Lanes {
		for _, selection := range []string{"strict", "warn", "none", "read-only"} {
			t.Run(lane.Name+"/"+selection, func(t *testing.T) {
				mode, policy := selection, "JIRA_READ_ONLY=false"
				if selection == "read-only" {
					mode, policy = "strict", "JIRA_READ_ONLY=true"
				}
				native := rawRead(t, p.owner, "api", "post", "/rest/api/3/jql/parse", "--query=validation="+mode, "--input", input)
				args := append([]string{"jql", "validate", "--mode=" + mode, "--"}, queries...)
				frame, stdout, exit := p.invoke(t, lane, []string{policy}, args...)
				if selection == "read-only" && lane.Name == "before" {
					if exit != 3 || frame.OK || !parityParseReadOnlyRefusal(stdout) {
						t.Fatal("legacy live parser no longer has its recorded read-only refusal")
					}
					p.check(t, lane, "C059.parser.read-only.native")
					return
				}
				if exit != 0 || !frame.OK {
					t.Fatal("parser diagnostics became command failure; inspect retained receipts")
				}
				data := parityAuthoringCleanData(t, stdout, lane.Name, "jql.validate")
				encoded, err := json.Marshal(data)
				if err != nil || !parityParseProjection(encoded, native.Response.JSON, queries, mode, lane.Name) {
					t.Fatal("parser output differs from independent native observation")
				}
				var result struct{ Queries []struct{ Valid *bool } }
				if json.Unmarshal(encoded, &result) != nil || len(result.Queries) != 4 {
					t.Fatal("parser omitted the four admitted queries")
				}
				for i, valid := range []bool{true, false, true, mode == "none"} {
					if result.Queries[i].Valid == nil || *result.Queries[i].Valid != valid {
						t.Fatal("parser syntax/semantic mode behavior differs from the admitted scenario")
					}
				}
				p.check(t, lane, "C059.parser."+selection+".native")
			})
		}
	}
}
