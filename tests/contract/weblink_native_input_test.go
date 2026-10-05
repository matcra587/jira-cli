package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const nativeWeblinkInput = `{"object":{"url":"https://example.com/runbook","title":"Runbook","summary":null,"icon":{"url16x16":"https://example.com/icon","future":{"id":900719925474099312345,"text":"\u003c\/"}},"status":{"resolved":false,"icon":{}},"future":{"text":"\u2028","value":null}},"application":{"name":"Example","type":"com.example","future":false},"globalId":"native-fixture","relationship":"documents","future":{"text":"\u2029","zero":0}}`

func TestWeblinkNativeInputRejectsEmptySpellings(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"json-input", "from-json", "body-adf"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, "issue", "weblink", "add", "KEY-1", "--"+flag+"=", "--url=https://example.com", "--dry-run", "--output=json")
			var frame struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
			}
			if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagValueInvalid || frame.Errors[0].Field != "json-input" {
				t.Fatalf("empty native input spelling was silently ignored: exit=%d stdout=%q", exit, stdout)
			}
		})
	}
}

func TestWeblinkNativeInputRetainsPartialAndInterruptedReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, outcome := range []string{"partial", "interrupt", "timeout"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				if outcome == "interrupt" && runtime.GOOS == "windows" {
					t.Skip("os.Interrupt is not supported for subprocesses on Windows")
				}
				t.Parallel()
				started := make(chan struct{})
				var mu sync.Mutex
				var keys []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/remotelink")
					body, err := io.ReadAll(req.Body)
					var got, want map[string]json.RawMessage
					if err != nil || json.Unmarshal(body, &got) != nil || json.Unmarshal([]byte(nativeWeblinkInput), &want) != nil || string(got["object"]) != string(want["object"]) || string(got["future"]) != string(want["future"]) {
						t.Error("partial/interrupted write changed native request values")
					}
					mu.Lock()
					keys = append(keys, key)
					mu.Unlock()
					if key == "KEY-2" {
						if outcome == "partial" {
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, `{"errorMessages":["fixture denied"]}`)
						} else {
							close(started)
							<-req.Context().Done()
						}
						return
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":10042}`)
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "weblink", "add", "KEY-1", "KEY-2", "KEY-3", "--json-input=-", "--parallelism=1", "--no-input", "--output=" + mode}
				var stimulus func(*os.Process)
				wantExit, wantCalls := 0, 3
				switch outcome {
				case "interrupt":
					wantExit, wantCalls = cli.ExitCanceled, 2
					stimulus = func(process *os.Process) {
						select {
						case <-started:
						case <-time.After(5 * time.Second):
							t.Fatal("native write did not reach cancellation stimulus")
						}
						if err := process.Signal(os.Interrupt); err != nil {
							t.Fatal(err)
						}
					}
				case "timeout":
					// --timeout bounds each Jira request. A timed-out target does
					// not prevent the next independent target from completing.
					args = append(args, "--timeout=500ms")
				}
				stdout, stderr, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), nativeWeblinkInput, stimulus, args...)
				mu.Lock()
				called := append([]string(nil), keys...)
				mu.Unlock()
				if exit != wantExit || len(called) != wantCalls || called[0] != "KEY-1" || called[1] != "KEY-2" {
					t.Fatalf("native partial/interruption changed exit or dispatch: exit=%d calls=%v stderr=%q", exit, called, stderr)
				}
				var data struct {
					Payload json.RawMessage                                      `json:"payload"`
					Results []envelope.KeyedResult[envelope.IssueWeblinkAddData] `json:"results"`
				}
				encoded := []byte(stdout)
				if mode == "json" || outcome == "interrupt" {
					var frame struct {
						Data json.RawMessage `json:"data"`
					}
					if json.Unmarshal(encoded, &frame) != nil {
						t.Fatal("native outcome lost its full frame")
					}
					encoded = frame.Data
				}
				if json.Unmarshal(encoded, &data) != nil || len(data.Results) != 3 || data.Results[0].Data == nil || data.Results[0].Data.Weblink == nil || data.Results[0].Data.Weblink.ID != "10042" || !data.Results[0].OK || data.Results[1].OK || data.Results[2].OK != (outcome != "interrupt") {
					t.Fatal("native partial/interruption lost joined or queued receipts")
				}
				wantCode := "jira_forbidden"
				if outcome == "timeout" {
					wantCode = "timeout"
				}
				if outcome == "interrupt" {
					wantCode = "canceled"
				}
				if data.Results[1].Err == nil || data.Results[1].Err.Code != wantCode {
					t.Fatal("native partial/interruption lost its target failure classification")
				}
				var got, want map[string]json.RawMessage
				if json.Unmarshal(data.Payload, &got) != nil || json.Unmarshal([]byte(nativeWeblinkInput), &want) != nil || string(got["object"]) != string(want["object"]) || string(got["future"]) != string(want["future"]) {
					t.Fatal("native partial/interruption lost the complete authored payload")
				}
			})
		}
	}
}

func TestNativeInputAliasOnlyDisagreementPrecedesIO(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"create", "edit", "clone", "weblink"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			args := []string{"issue", route}
			if route == "weblink" {
				args = append(args, "add")
			}
			if route != "create" {
				args = append(args, "KEY-1")
			}
			args = append(args, "--from-json=first-missing.json", "--body-adf=second-missing.json", "--output=json", "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, args...)
			var frame struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
			}
			if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagConflict || frame.Errors[0].Field != "json-input" || !strings.Contains(stderr, "--json-input") {
				t.Fatalf("alias-only ambiguity was not refused before I/O: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
		})
	}
}

func TestWeblinkNativeInputPreservesCompleteRequestAndReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "agent"} {
		for _, route := range []string{"stdin", "file", "from-json", "body-adf"} {
			for _, outcome := range []string{"preview", "write", "failure"} {
				t.Run(mode+"/"+route+"/"+outcome, func(t *testing.T) {
					t.Parallel()
					var mu sync.Mutex
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						body, err := io.ReadAll(req.Body)
						if err != nil || req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/remotelink") {
							t.Error("native remote-link request changed transport")
						}
						mu.Lock()
						requests = append(requests, string(body))
						mu.Unlock()
						if outcome == "failure" {
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write([]byte(`{"errorMessages":["fixture denied"]}`))
							return
						}
						w.WriteHeader(http.StatusCreated)
						_, _ = w.Write([]byte(`{"id":10042}`))
					}))
					t.Cleanup(server.Close)
					env := editReceiptEnv(server.URL)
					args := []string{"issue", "weblink", "add", "KEY-1", "KEY-2", "--parallelism=2", "--no-input"}
					if mode == "agent" {
						env = append(env, "AGENT=1")
					} else {
						args = append(args, "--output="+mode)
					}
					stdin := nativeWeblinkInput
					if route == "file" {
						path := filepath.Join(t.TempDir(), "payload.json")
						if err := os.WriteFile(path, []byte(stdin), 0o600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--json-input", path)
						stdin = ""
					} else {
						flag := "json-input"
						if route != "stdin" {
							flag = route
						}
						args = append(args, "--"+flag+"=-")
					}
					if outcome == "preview" {
						args = append(args, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnvInputStarted(t, env, stdin, nil, args...)
					wantExit, wantCalls := 0, 2
					switch outcome {
					case "preview":
						wantCalls = 0
					case "failure":
						wantExit = cli.ExitAuth
					}
					mu.Lock()
					bodies := append([]string(nil), requests...)
					mu.Unlock()
					if exit != wantExit || len(bodies) != wantCalls {
						t.Fatalf("native request exit=%d, calls=%d; want %d/%d; stdout=%q stderr=%q", exit, len(bodies), wantExit, wantCalls, stdout, stderr)
					}
					var data struct {
						Payload json.RawMessage                         `json:"payload"`
						Results []envelope.KeyedResult[json.RawMessage] `json:"results"`
					}
					if (mode == "compact" || mode == "agent") && outcome != "failure" {
						if err := json.Unmarshal([]byte(stdout), &data); err != nil {
							t.Fatal(err)
						}
					} else {
						var frame struct {
							Data json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(frame.Data, &data); err != nil {
							t.Fatal(err)
						}
					}
					if len(data.Results) != 2 || data.Results[0].Issue.Key != "KEY-1" || data.Results[1].Issue.Key != "KEY-2" {
						t.Fatal("native remote-link outcome identities were lost")
					}
					for _, raw := range append(bodies, string(data.Payload)) {
						var fields, want map[string]json.RawMessage
						if json.Unmarshal([]byte(raw), &fields) != nil || json.Unmarshal([]byte(nativeWeblinkInput), &want) != nil {
							t.Fatal("native payload missing or malformed")
						}
						for name, value := range want {
							if string(fields[name]) != string(value) {
								t.Fatalf("native member %s changed: %s", name, fields[name])
							}
						}
					}
				})
			}
		}
	}
}

func TestWeblinkNativeInputRefusesAmbiguityBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		flags       []string
	}{
		{name: "null document", input: `null`},
		{name: "array document", input: `[]`},
		{name: "trailing document", input: nativeWeblinkInput + `{}`},
		{name: "duplicate root", input: `{"object":{},"object":{}}`},
		{name: "duplicate nested extension", input: `{"object":{"url":"https://example.com","title":"Example","future":{"id":1,"id":2}}}`},
		{name: "root casing", input: `{"Object":{"url":"https://example.com","title":"Example"}}`},
		{name: "object casing", input: `{"object":{"URL":"https://example.com","title":"Example"}}`},
		{name: "application casing", input: `{"object":{"url":"https://example.com","title":"Example"},"application":{"Name":"Example"}}`},
		{name: "icon casing", input: `{"object":{"url":"https://example.com","title":"Example","icon":{"Url16x16":"https://example.com/icon"}}}`},
		{name: "status casing", input: `{"object":{"url":"https://example.com","title":"Example","status":{"Resolved":false}}}`},
		{name: "status icon casing", input: `{"object":{"url":"https://example.com","title":"Example","status":{"icon":{"Title":"Example"}}}}`},
		{name: "null object", input: `{"object":null}`},
		{name: "array object", input: `{"object":[]}`},
		{name: "missing url", input: `{"object":{"title":"Example"}}`},
		{name: "missing title", input: `{"object":{"url":"https://example.com"}}`},
		{name: "null title", input: `{"object":{"url":"https://example.com","title":null}}`},
		{name: "nonstring title", input: `{"object":{"url":"https://example.com","title":0}}`},
		{name: "non-web URL", input: `{"object":{"url":"javascript://example.com","title":"Example"}}`},
		{name: "nonobject status", input: `{"object":{"url":"https://example.com","title":"Example","status":false}}`},
		{name: "nonboolean resolved", input: `{"object":{"url":"https://example.com","title":"Example","status":{"resolved":"false"}}}`},
		{name: "nonstring application", input: `{"object":{"url":"https://example.com","title":"Example"},"application":{"name":0}}`},
		{name: "url collision", input: nativeWeblinkInput, flags: []string{"--url=https://example.com/runbook"}},
		{name: "title collision", input: nativeWeblinkInput, flags: []string{"--title=Runbook"}},
		{name: "null summary collision", input: nativeWeblinkInput, flags: []string{"--summary="}},
		{name: "global id collision", input: nativeWeblinkInput, flags: []string{"--global-id="}},
		{name: "relationship collision", input: nativeWeblinkInput, flags: []string{"--relationship=documents"}},
		{name: "alias disagreement", input: nativeWeblinkInput, flags: []string{"--from-json=other.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":10042}`))
			}))
			t.Cleanup(server.Close)
			args := append([]string{"issue", "weblink", "add", "KEY-1", "--json-input=-", "--output=json", "--no-input"}, tc.flags...)
			stdout, stderr, exit := runBinaryEnvInputStarted(t, editReceiptEnv(server.URL), tc.input, nil, args...)
			var frame struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
			}
			mu.Lock()
			gotCalls := calls
			mu.Unlock()
			if exit != cli.ExitValidation || gotCalls != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || (frame.Errors[0].Code != cli.CodeFlagValueInvalid && frame.Errors[0].Code != cli.CodeFlagConflict) {
				t.Fatalf("invalid native input crossed the local refusal: exit=%d calls=%d stdout=%q stderr=%q", exit, gotCalls, stdout, stderr)
			}
		})
	}
}

func TestWeblinkNativeInputMergesOnlyAbsentMembers(t *testing.T) {
	t.Parallel()
	input := `{"object":{"url":"https:\/\/example.com\/runbook","future":{"id":900719925474099312345,"text":"\u003c\/"}},"future":null}`
	for _, title := range []string{"Example", ""} {
		t.Run(title, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, input, nil,
				"issue", "weblink", "add", "KEY-1", "--json-input=-", "--title="+title, "--summary=Summary", "--relationship=documents", "--global-id=fixture", "--dry-run", "--output=json")
			var frame struct {
				Data struct {
					Payload struct {
						Object       map[string]json.RawMessage `json:"object"`
						Future       json.RawMessage            `json:"future"`
						GlobalID     string                     `json:"globalId"`
						Relationship string                     `json:"relationship"`
					} `json:"payload"`
				} `json:"data"`
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil {
				t.Fatalf("nonconflicting native composition failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			payload := frame.Data.Payload
			var gotTitle string
			if json.Unmarshal(payload.Object["title"], &gotTitle) != nil || gotTitle != title || string(payload.Object["url"]) != `"https:\/\/example.com\/runbook"` || string(payload.Object["future"]) != `{"id":900719925474099312345,"text":"\u003c\/"}` || string(payload.Future) != "null" || string(payload.Object["summary"]) != `"Summary"` || payload.GlobalID != "fixture" || payload.Relationship != "documents" {
				t.Fatal("flag composition changed authored native members or explicit empty title")
			}
		})
	}
}
