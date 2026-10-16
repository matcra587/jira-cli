//go:build live

package live

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

const (
	parityAPIEndpoint = "/rest/agile/1.0/fixture"
	parityAPIMatch    = `"tag,with,commas", W/"weak"`
	parityAPIDate     = "Thu, 01 Jan 1970 00:00:00 GMT"
	parityAPIInput    = " \n{\"opaque\":900719925474099312345,\"text\":\"<&>\\u2028\"}\n"
	parityAPIReply    = " {\"future\":900719925474099312345,\"text\":\"<&>\\u2028\"}\n"
)

func parityAPIRequest(r *http.Request, method string, body []byte) bool {
	user, password, auth := r.BasicAuth()
	value, err := io.ReadAll(io.LimitReader(r.Body, int64(len(body)+1)))
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	return r.Method == method && r.URL.EscapedPath() == parityAPIEndpoint &&
		queryErr == nil && reflect.DeepEqual(query, url.Values{"empty": {""}, "field": {"summary,status", "a=b &+"}}) &&
		auth && user == "fixture@example.invalid" && password == "synthetic-fixture-token" &&
		err == nil && bytes.Equal(value, body) &&
		reflect.DeepEqual(r.Header.Values("Content-Type"), []string{"application/vnd.fixture+json"}) &&
		reflect.DeepEqual(r.Header.Values("If-Match"), []string{parityAPIMatch}) &&
		reflect.DeepEqual(r.Header.Values("If-Unmodified-Since"), []string{parityAPIDate})
}

func parityAPIExpected(method string, body []byte, preview, refused bool, status, bound int, captured, reply []byte, truncated bool) []byte {
	request := map[string]any{
		"method": method, "endpoint": parityAPIEndpoint,
		"query": []any{
			map[string]any{"name": "empty", "values": []string{""}},
			map[string]any{"name": "field", "values": []string{"summary,status", "a=b &+"}},
		},
		"content_type": "application/vnd.fixture+json", "max_response_bytes": bound,
		"if_match": parityAPIMatch, "if_unmodified_since": parityAPIDate,
	}
	if body != nil {
		request["body_base64"] = base64.StdEncoding.EncodeToString(body)
	}
	out := map[string]any{"request": request, "attempted": !preview}
	if method != "GET" && method != "HEAD" && method != "OPTIONS" {
		out["dry_run"] = preview
	}
	if !preview && !refused {
		response := map[string]any{
			"status_code": status,
			"headers": []any{
				map[string]any{"name": "Content-Length", "values": []string{strconv.Itoa(len(reply))}},
				map[string]any{"name": "Content-Type", "values": []string{"application/json"}},
				map[string]any{"name": "Etag", "values": []string{`"native-tag"`}},
				map[string]any{"name": "X-Ratelimit-Fixture", "values": []string{"first", "second"}},
			},
			"body_base64": base64.StdEncoding.EncodeToString(captured),
			"truncated":   truncated, "attempts": 1, "waited_ms": 0,
		}
		if utf8.Valid(captured) {
			response["body_text"] = string(captured)
		}
		if !truncated && utf8.Valid(captured) && json.Valid(captured) {
			response["json"] = json.RawMessage(captured)
		}
		out["response"] = response
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return raw
}

// Human receipts use an independent complete projection, including delivery markers.
// Literal control-bearing response expectations avoid reusing the CLI sanitizer.
func parityAPIHumanExpected(method, scenario string, status int, reply []byte) string {
	if scenario == "missing-force" {
		return ""
	}
	request := method + " " + parityAPIEndpoint + "\n" +
		"Query: empty=\nQuery: field=summary,status\nQuery: field=a=b &+\n" +
		"If-Match: " + parityAPIMatch + "\nIf-Unmodified-Since: " + parityAPIDate + "\n"
	if scenario == "preview" {
		return "Would send " + request + "Content-Type: application/vnd.fixture+json\n" + parityAPIInput + "\nNo request sent.\n"
	}
	if scenario == "read-only" {
		return request + "No HTTP response received; inspect diagnostics for delivery and mutation outcomes.\n"
	}
	response := "HTTP " + strconv.Itoa(status) + "\nContent-Length: " + strconv.Itoa(len(reply)) + "\n" +
		"Content-Type: application/json\nEtag: \"native-tag\"\nX-Ratelimit-Fixture: first\nX-Ratelimit-Fixture: second\n"
	body := string(reply) + "\n"
	switch {
	case method == "HEAD":
		body = "\n"
	case scenario == "opaque-response":
		body = "Body (base64): /wAb\n"
	case scenario == "control-response":
		body = "saferedend\nnextline\n"
	case scenario == "capture-bound":
		body = "Response capture incomplete; inspect the partial receipt before retrying.\nnull\n"
	}
	return request + response + body
}

func parityAPIHumanReceipt(text, method, scenario string, status int, reply []byte) bool {
	return !strings.ContainsAny(text, "\x00\x1b") && text == parityAPIHumanExpected(method, scenario, status, reply)
}

func TestParityAPIHumanReceiptGuards(t *testing.T) {
	const request = "POST /rest/agile/1.0/fixture\nQuery: empty=\nQuery: field=summary,status\nQuery: field=a=b &+\n" +
		"If-Match: \"tag,with,commas\", W/\"weak\"\nIf-Unmodified-Since: Thu, 01 Jan 1970 00:00:00 GMT\n"
	const response = "HTTP 200\nContent-Length: 53\nContent-Type: application/json\nEtag: \"native-tag\"\n" +
		"X-Ratelimit-Fixture: first\nX-Ratelimit-Fixture: second\n" +
		" {\"future\":900719925474099312345,\"text\":\"<&>\\u2028\"}\n\n"
	live := request + response
	preview := "Would send " + request + "Content-Type: application/vnd.fixture+json\n" +
		" \n{\"opaque\":900719925474099312345,\"text\":\"<&>\\u2028\"}\n\nNo request sent.\n"
	refused := request + "No HTTP response received; inspect diagnostics for delivery and mutation outcomes.\n"
	for _, tc := range []struct {
		name, scenario, text string
		valid                bool
	}{
		{"live", "native-lower", live, true},
		{"preview", "preview", preview, true},
		{"read-only", "read-only", refused, true},
		{"live-preview-wording", "native-lower", "Would send " + live, false},
		{"preview-live-wording", "preview", strings.TrimPrefix(preview, "Would send "), false},
		{"live-no-request", "native-lower", live + "No request sent.\n", false},
		{"live-no-response", "native-lower", live + "No HTTP response received; invented\n", false},
		{"preview-invented-response", "preview", preview + response, false},
		{"refused-invented-response", "read-only", refused + response, false},
		{"live-invented-truncation", "native-lower", live + "Response capture incomplete; invented\n", false},
		{"live-control", "native-lower", live + "\x1b[31m", false},
		{"wrong-status", "native-lower", strings.Replace(live, "HTTP 200", "HTTP 201", 1), false},
		{"duplicate-query", "native-lower", live + "Query: empty=\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityAPIHumanReceipt(tc.text, "POST", tc.scenario, 200, []byte(parityAPIReply)) != tc.valid {
				t.Fatal("human API guard admitted contradictory delivery or changed literal receipt")
			}
		})
	}
}

// Raw API methods are current-only. These controlled-server scenarios prove
// native transport and explicit local refusal, never live Jira endpoint support or CAS.
func TestParityActualBinaryAPIMethods(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual current binary; legacy has no raw API command")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	ids := map[string]string{"DELETE": "C082", "GET": "C083", "HEAD": "C084", "OPTIONS": "C085", "PATCH": "C086", "POST": "C087", "PUT": "C088"}
	for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		mutation := method != "GET" && method != "HEAD" && method != "OPTIONS"
		for _, mode := range []string{"json", "compact", "human"} {
			for _, scenario := range []string{"native-lower", "native-upper", "opaque-response", "control-response", "http-refusal", "preview", "missing-force", "read-only", "server-write-failure", "capture-bound", "default-bound", "unbounded", "stdin-body", "empty-body", "no-body", "opaque-input", "force-alias"} {
				if !mutation && (scenario == "preview" || scenario == "missing-force" || scenario == "read-only" || scenario == "server-write-failure" || scenario == "force-alias") || method == "HEAD" && (scenario == "opaque-response" || scenario == "control-response" || scenario == "capture-bound") {
					continue
				}
				t.Run(method+"/"+mode+"/"+scenario, func(t *testing.T) {
					home := t.TempDir()
					var calls atomic.Int32
					status, expectedExit, bound := 200, 0, 16777216
					body := []byte(parityAPIInput)
					reply := []byte(parityAPIReply)
					code := ""
					switch scenario {
					case "unbounded":
						bound = 0
					case "opaque-input":
						body = []byte{0, 0xff, 0x1b}
					case "empty-body":
						body = []byte{}
					case "no-body":
						body = nil
					case "opaque-response":
						reply = []byte{0xff, 0, 0x1b}
					case "control-response":
						reply = []byte("safe\x1b[31mred\x1b[0m\x00\tend\nnext\rline")
					case "http-refusal":
						status, expectedExit, code = 400, 3, "jira_bad_request"
					case "missing-force":
						expectedExit, code = 3, "flag_required"
					case "read-only":
						expectedExit, code = 3, "read_only_blocked"
					case "server-write-failure":
						status, expectedExit, code = 500, 5, "jira_server_error"
					case "capture-bound":
						bound, expectedExit, code = 4, 8, "api_response_incomplete"
						reply = []byte("null plus more")
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if !parityAPIRequest(r, method, body) {
							t.Error("raw request changed its exact method, path, query, credentials, body or preconditions")
						}
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", strconv.Itoa(len(reply)))
						w.Header().Set("ETag", `"native-tag"`)
						w.Header().Add("X-RateLimit-Fixture", "first")
						w.Header().Add("X-RateLimit-Fixture", "second")
						w.Header().Set("Set-Cookie", "excluded-fixture-header")
						w.Header().Set("X-Auth-Token", "excluded-fixture-header")
						w.WriteHeader(status)
						if method != "HEAD" {
							_, _ = w.Write(reply)
						}
					}))
					defer server.Close()
					config := parityCachePublicConfig(t, home, "after", server.URL)
					input := filepath.Join(home, "input.json")
					if err := os.WriteFile(input, body, 0o600); err != nil {
						t.Fatal(err)
					}
					before := parityLocalTree(t, home)
					spelling := strings.ToLower(method)
					if scenario == "native-upper" {
						spelling = method
					}
					selectedInput := input
					if scenario == "missing-force" {
						selectedInput = filepath.Join(home, "missing-input")
					}
					args := []string{"api", spelling, parityAPIEndpoint, "--content-type=application/vnd.fixture+json", "--query=field=summary,status", "--query=field=a=b &+", "--query=empty=", "--if-match=" + parityAPIMatch, "--if-unmodified-since=" + parityAPIDate}
					if scenario != "default-bound" {
						args = append(args, "--max-response-bytes="+strconv.Itoa(bound))
					}
					var stdin []byte
					if scenario == "stdin-body" {
						selectedInput = "-"
						stdin = body
					}
					if scenario != "no-body" {
						args = append(args, "--input", selectedInput)
					}
					extra := []string{"JIRA_READ_ONLY=false"}
					preview := scenario == "preview"
					if preview {
						args = append(args, "--dry-run")
						extra = append(extra, "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false")
					} else if mutation && scenario != "missing-force" {
						force := "--force"
						if scenario == "force-alias" {
							force = "--yes"
						}
						args = append(args, force)
					}
					if scenario == "read-only" {
						extra = append(extra, "JIRA_READ_ONLY=true")
					}
					if err := parityFingerprint(binary, digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config, Output: mode, Stdin: stdin}, extra, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr, InputBytes: body})
					wantCalls := int32(1)
					if preview || scenario == "missing-force" || scenario == "read-only" {
						wantCalls = 0
					}
					if result.Exit != expectedExit || expectedExit == 0 && len(result.Stderr) != 0 || calls.Load() != wantCalls || !reflect.DeepEqual(before, parityLocalTree(t, home)) || (bytes.Contains(result.Stdout, []byte("excluded-fixture-header")) || bytes.Contains(result.Stderr, []byte("excluded-fixture-header"))) {
						t.Fatalf("raw API entry/exit/effects differ: exit %d want %d HTTP %d want %d stdout %s stderr %s", result.Exit, expectedExit, calls.Load(), wantCalls, result.Stdout, result.Stderr)
					}
					if mode == "human" {
						if !parityAPIHumanReceipt(string(result.Stdout), method, scenario, status, reply) {
							t.Fatalf("human raw API receipt differs: %q", result.Stdout)
						}
						if scenario == "missing-force" && !bytes.Contains(result.Stderr, []byte("raw mutations require --force")) {
							t.Fatal("unentered human raw API refusal lost its force diagnostic")
						}
						if err := parityFingerprint(binary, digest); err != nil {
							t.Fatal(err)
						}
						check := ids[method] + ".transport." + scenario + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], "after")
						return
					}
					data := result.Stdout
					operation := "api." + strings.ToLower(method)
					if result.Exit != 0 || mode == "json" {
						parityAuthoringData(t, result.Stdout, "after", operation, result.Exit)
						var frame struct {
							Data     json.RawMessage
							Errors   []struct{ Code string }
							Warnings json.RawMessage
						}
						if json.Unmarshal(result.Stdout, &frame) != nil || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) || result.Exit != 0 && (len(frame.Errors) != 1 || frame.Errors[0].Code != code) {
							t.Fatalf("raw API result lost exact classification: %s", result.Stdout)
						}
						data = frame.Data
					}
					if scenario == "missing-force" {
						if string(data) != "null" {
							t.Fatal("unentered raw API refusal invented a request receipt")
						}
					} else {
						captured := reply
						if method == "HEAD" {
							captured = nil
						} else if bound > 0 && bound < len(reply) {
							captured = reply[:bound]
						}
						want := parityAPIExpected(method, body, preview, scenario == "read-only", status, bound, captured, reply, scenario == "capture-bound")
						if !parityAuthoringJSON(data, want) {
							t.Fatalf("raw API native receipt differs: %s; want %s", data, want)
						}
					}
					if err := parityFingerprint(binary, digest); err != nil {
						t.Fatal(err)
					}
					check := ids[method] + ".transport." + scenario + "." + mode
					evidence.Checks[check] = append(evidence.Checks[check], "after")
				})
			}
		}
	}
	saveParityLocal(t, "api-fixture", evidence)
}

func TestParityAPIRequestGuards(t *testing.T) {
	for _, scenario := range []string{"complete", "wrong-method", "encoded-path", "duplicate-query", "reversed-query", "missing-empty-query", "bad-auth", "extra-body", "wrong-content-type", "duplicate-condition", "wrong-condition"} {
		t.Run(scenario, func(t *testing.T) {
			method, endpoint, body := "POST", parityAPIEndpoint, []byte(parityAPIInput)
			query := url.Values{"empty": {""}, "field": {"summary,status", "a=b &+"}}
			switch scenario {
			case "wrong-method":
				method = "PUT"
			case "encoded-path":
				endpoint = "/rest/agile/1.0/%66ixture"
			case "duplicate-query":
				query.Add("field", "extra")
			case "reversed-query":
				query["field"] = []string{"a=b &+", "summary,status"}
			case "missing-empty-query":
				delete(query, "empty")
			case "extra-body":
				body = append(body, []byte("hidden trailing bytes")...)
			}
			r := httptest.NewRequest(method, "http://fixture.invalid"+endpoint+"?"+query.Encode(), bytes.NewReader(body))
			r.SetBasicAuth("fixture@example.invalid", "synthetic-fixture-token")
			r.Header.Set("Content-Type", "application/vnd.fixture+json")
			r.Header.Set("If-Match", parityAPIMatch)
			r.Header.Set("If-Unmodified-Since", parityAPIDate)
			switch scenario {
			case "bad-auth":
				r.SetBasicAuth("other@example.invalid", "synthetic-fixture-token")
			case "wrong-content-type":
				r.Header.Set("Content-Type", "text/plain")
			case "duplicate-condition":
				r.Header.Add("If-Match", parityAPIMatch)
			case "wrong-condition":
				r.Header.Set("If-Unmodified-Since", "Thu, 01 Jan 1970 00:00:01 GMT")
			}
			if parityAPIRequest(r, "POST", []byte(parityAPIInput)) != (scenario == "complete") {
				t.Fatal("raw API request guard admitted changed literal wire facts")
			}
		})
	}
}

func TestParityAPILiteralReceiptFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/parity-api-oracles.json")
	var fixtures []struct {
		Name string
		Data json.RawMessage
	}
	if err != nil || json.Unmarshal(raw, &fixtures) != nil || len(fixtures) != 3 {
		t.Fatal("independent literal raw API receipt fixtures unavailable")
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			body := []byte(parityAPIInput)
			switch fixture.Name {
			case "empty-body":
				body = []byte{}
			case "no-body":
				body = nil
			case "body":
			default:
				t.Fatal("undeclared raw API fixture")
			}
			expected := parityAPIExpected("POST", body, false, false, 200, 16777216, []byte(parityAPIReply), []byte(parityAPIReply), false)
			if !parityAuthoringJSON(expected, fixture.Data) {
				t.Fatal("dynamic raw API oracle differs from independent literal native receipt")
			}
		})
	}
}
