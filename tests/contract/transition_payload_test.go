package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

type nativeTransitionFrame struct {
	OK   bool `json:"ok"`
	Data struct {
		Transition string          `json:"transition"`
		Payload    json.RawMessage `json:"payload"`
		Comment    json.RawMessage `json:"comment"`
		Results    []struct {
			OK    bool                 `json:"ok"`
			Issue struct{ Key string } `json:"issue"`
			Error *struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			} `json:"error"`
			Data struct {
				Attempted bool            `json:"attempted"`
				Applied   bool            `json:"applied"`
				Payload   json.RawMessage `json:"payload"`
			} `json:"data"`
		} `json:"results"`
	} `json:"data"`
}

func TestTransitionEscapedADFTypeRetainsCommentRoute(t *testing.T) {
	t.Parallel()
	const body = `{"type":"d\u006fc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"<>&\u2028"}]}]}`
	stdout, stderr, exit := runBinary(t, "issue", "transition", "KEY-1", "Done", "--json-input="+transitionPayloadFile(t, body), "--dry-run", "--output=json")
	var f nativeTransitionFrame
	var request struct {
		Fields map[string]json.RawMessage `json:"fields"`
		Update struct {
			Comment []struct {
				Add struct{ Body json.RawMessage } `json:"add"`
			} `json:"comment"`
		} `json:"update"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || json.Unmarshal(f.Data.Payload, &request) != nil ||
		string(f.Data.Comment) != body || len(request.Fields) != 0 || len(request.Update.Comment) != 1 || string(request.Update.Comment[0].Add.Body) != body {
		t.Fatalf("escaped ADF type lost the comment route or raw bytes: exit=%d stderr=%q", exit, stderr)
	}
}

func TestTransitionDuplicatePayloadMembersRefuseBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"transition", `{"transition":{"id":"31"},"transition":{"id":"41"}}`},
		{"transition id", `{"transition":{"id":"31","id":"41"}}`},
		{"target case collision", `{"transition":{"id":"41","ID":"31"}}`},
		{"fields", `{"fields":{"summary":"first"},"fields":{"summary":"second"}}`},
		{"field id", `{"fields":{"summary":"first","summary":"second"}}`},
		{"escaped field id", `{"fields":{"summary":"first","summ\u0061ry":"second"}}`},
		{"update", `{"update":{"labels":[]},"update":{"summary":[]}}`},
		{"operation", `{"update":{"labels":[{"add":"first","add":"second"}]}}`},
		{"extension", `{"fields":{},"future":{"value":1,"value":2}}`},
		{"fields case collision", `{"fields":{"summary":"first"},"Fields":{"summary":"second"}}`},
		{"update case collision", `{"update":{"labels":[]},"Update":{"summary":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			_, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "--jql=project=KEY", "--transition=41", "--json-input="+transitionPayloadFile(t, tc.body), "--force", "--no-input", "--output=json")
			if exit != cli.ExitValidation || calls.Load() != 0 {
				t.Fatalf("duplicate transition input reached HTTP: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
		})
	}
}

func TestTransitionMalformedNativeSectionsRefuseBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"properties":null}`, `{"properties":{}}`, `{"properties":"fixture"}`, `{"historyMetadata":null}`, `{"historyMetadata":[]}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			_, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "--jql=project=KEY", "--transition=31", "--json-input="+transitionPayloadFile(t, body), "--force", "--no-input", "--output=json")
			if exit != cli.ExitValidation || calls.Load() != 0 {
				t.Fatalf("malformed native request section reached HTTP: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
		})
	}
}

func transitionPayloadFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTransitionNativePayloadLocalPreviewRetainsWholeRequest(t *testing.T) {
	t.Parallel()
	const body = `{"transition":{"id":9007199254740993},"fields":{},"update":{},"historyMetadata":{"extraData":{"text":"<>&\u2028"}},"properties":[{"key":"fixture","value":9007199254740993}],"extension":null}`
	for _, mode := range []string{"json", "compact", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "transition", "KEY-1", "KEY-2", "--json-input=" + transitionPayloadFile(t, body), "--dry-run", "--no-input"}
			if mode != "agent" {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), args...)
			if mode != "json" && exit == 0 {
				stdout = `{"ok":true,"data":` + stdout + `}`
			}
			var frame nativeTransitionFrame
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.Transition != "9007199254740993" || len(frame.Data.Results) != 2 || calls.Load() != 0 {
				t.Fatalf("whole transition preview lost target, keys or local boundary: exit=%d stderr=%q", exit, stderr)
			}
			var payload map[string]json.RawMessage
			if json.Unmarshal(frame.Data.Payload, &payload) != nil || string(payload["fields"]) != "{}" || string(payload["update"]) != "{}" || string(payload["extension"]) != "null" || !strings.Contains(string(payload["properties"]), "9007199254740993") || !strings.Contains(string(payload["historyMetadata"]), `<>&\u2028`) || string(payload["transition"]) != `{"id":"9007199254740993"}` {
				t.Fatal("transition preview dropped native containers, extensions or raw spelling")
			}
			for index, row := range frame.Data.Results {
				if !row.OK || row.Issue.Key != []string{"KEY-1", "KEY-2"}[index] || row.Data.Attempted || row.Data.Applied {
					t.Fatal("local transition preview reordered keys or claimed a write")
				}
			}
		})
	}
}

func TestTransitionNativePayloadWireAndFailedReceiptAgree(t *testing.T) {
	t.Parallel()
	const body = `{"transition":{"name":"Done"},"fields":{"summary":"<>&\u2028","customfield_10001":9007199254740993},"update":{"labels":[{"add":"fixture"}]},"historyMetadata":{"type":"fixture"},"properties":[{"key":"fixture","value":null}],"extension":{"opaque":false}}`
	var mu sync.Mutex
	requests := make(map[string][]byte)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
		if req.Method == http.MethodGet {
			if req.URL.Query().Get("expand") != "transitions.fields" {
				t.Error("native payload validation did not request field metadata")
			}
			id := "31"
			if key == "KEY-2" {
				id = "41"
			}
			_, _ = io.WriteString(w, `{"transitions":[{"id":"`+id+`","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":true},"customfield_10001":{"required":false},"labels":{"required":false}}}]}`)
			return
		}
		wire, err := io.ReadAll(req.Body)
		if err != nil || req.Method != http.MethodPost {
			t.Error("native transition request did not reach the write endpoint")
		}
		mu.Lock()
		requests[key] = wire
		calls = append(calls, key)
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"errorMessages":["fixture uncertain"]}`)
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "KEY-2", "--json-input="+transitionPayloadFile(t, body), "--parallelism=1", "--no-input", "--output=json")
	var frame nativeTransitionFrame
	if exit != cli.ExitServer || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Data.Results) != 2 {
		t.Fatalf("native transition failure lost prepared outcomes: exit=%d stderr=%q", exit, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, []string{"KEY-1", "KEY-2"}) {
		t.Fatalf("native transition changed ordering or replayed: %v", calls)
	}
	for index, row := range frame.Data.Results {
		key := []string{"KEY-1", "KEY-2"}[index]
		var wire map[string]json.RawMessage
		if row.OK || row.Issue.Key != key || !row.Data.Attempted || row.Data.Applied || string(row.Data.Payload) != string(requests[key]) || json.Unmarshal(requests[key], &wire) != nil {
			t.Fatal("failed native transition did not retain exact per-key wire payload")
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		for name, expected := range input {
			if name == "fields" || name == "update" {
				var want, got map[string]json.RawMessage
				if json.Unmarshal(expected, &want) != nil || json.Unmarshal(wire[name], &got) != nil || len(want) != len(got) {
					t.Fatalf("native transition changed %s container", name)
				}
				for field, raw := range want {
					if string(got[field]) != string(raw) {
						t.Fatalf("native transition changed %s.%s", name, field)
					}
				}
			} else if name != "transition" && string(wire[name]) != string(expected) {
				t.Fatalf("native transition changed %s", name)
			}
		}
		id := []string{"31", "41"}[index]
		if string(wire["transition"]) != `{"id":"`+id+`"}` {
			t.Fatal("native transition reused another key's resolved target")
		}
	}
}

func TestADFValidationInWorkingDirectoryWithURLCharacters(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"braces{}", "percent%", "fragment#", "question?"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if runtime.GOOS == "windows" && strings.Contains(name, "?") {
				t.Skip("Windows filenames cannot contain a question mark")
			}
			workdir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(workdir, 0o700); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"fixture"}]}]}`
			_, stderr, exit := runBinaryIOInDirStarted(t, workdir, nil, nil, nil, nil, "issue", "comment", "add", "KEY-1", "--json-input="+transitionPayloadFile(t, body), "--dry-run", "--output=json")
			if exit != 0 {
				t.Fatalf("embedded schema depended on working-directory URL spelling: exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestTransitionNativePayloadScreenlessRefusalHasNoWrite(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"transition":{"id":"31"},"fields":{"summary":"fixture"}}`,
		`{"transition":{"id":"31"},"update":{"labels":[{"add":"fixture"}]}}`,
		`{"transition":{"id":"31"},"properties":[{"key":"fixture","value":false}]}`,
		`{"transition":{"id":"31"},"update":{"comment":[{"add":{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"fixture"}]}]}}}]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				reads.Add(1)
				_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false}]}`)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "--json-input="+transitionPayloadFile(t, body), "--no-input", "--output=json")
			var frame nativeTransitionFrame
			if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data.Attempted || frame.Data.Results[0].Data.Applied || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("screenless native transition discarded payload or changed status: exit=%d reads=%d writes=%d stderr=%q", exit, reads.Load(), writes.Load(), stderr)
			}
			err := frame.Data.Results[0].Error
			if err == nil || err.Code != "transition_screen_required" || !strings.Contains(err.Hint, "screen") || strings.Contains(err.Hint, "drop") {
				t.Fatalf("native screen refusal offered dropping the payload instead of preserving its intended changes: %+v", err)
			}
		})
	}
}

func TestTransitionMalformedPayloadRefusesBeforeSelectorHTTP(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `[]`, `{"fields":null}`, `{"transition":{"id":31.5}}`, `{"transition":{"id":3e1}}`, `{"transition":{"id":"31","name":"Done"}}`, `{"fields":{"labels":[]},"update":{"labels":[{"add":"fixture"}]}}`, `{"comment":null}`, `{"update":{"labels":{}}}`, `{"comment":{"type":"doc","version":1,"content":[]},"update":{"comment":[]}}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			_, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "--jql=project=KEY", "--transition=31", "--json-input="+transitionPayloadFile(t, body), "--force", "--no-input", "--output=json")
			if exit != cli.ExitValidation || calls.Load() != 0 {
				t.Fatalf("invalid native payload reached selector HTTP: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
		})
	}
}

func TestTransitionWorkflowMetadataRefusesUnsafeWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, target, transitions, payload string
	}{
		{"ambiguous destination", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false},{"id":"41","name":"Resolve","to":{"name":"Done"},"hasScreen":false}]`, `{}`},
		{"unavailable target", "31", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false,"isAvailable":false}]`, `{}`},
		{"required field missing", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":true,"hasDefaultValue":false}}}]`, `{"fields":{}}`},
		{"required field null", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":true}}}]`, `{"fields":{"summary":null}}`},
		{"required field empty", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":true}}}]`, `{"fields":{"summary":""}}`},
		{"required operation empty", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"labels":{"required":true}}}]`, `{"update":{"labels":[]}}`},
		{"required default empty array", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"labels":{"required":true,"defaultValue":[ ]}}}]`, `{"fields":{}}`},
		{"field outside screen", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":false}}}]`, `{"fields":{"customfield_10001":"fixture"}}`},
		{"operation outside screen", "Done", `[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"summary":{"required":false}}}]`, `{"update":{"labels":[{"add":"fixture"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, preview := range []bool{false, true} {
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method == http.MethodGet {
						_, _ = io.WriteString(w, `{"transitions":`+tc.transitions+`}`)
						return
					}
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				args := []string{"issue", "transition", "KEY-1", "--transition=" + tc.target, "--json-input=" + transitionPayloadFile(t, tc.payload), "--no-input", "--output=json"}
				if preview {
					args = append(args, "--dry-run", "--validate-remote")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				server.Close()
				var frame nativeTransitionFrame
				if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data.Attempted || frame.Data.Results[0].Data.Applied || writes.Load() != 0 {
					t.Fatalf("unsafe workflow accepted (preview=%t): exit=%d writes=%d stderr=%q", preview, exit, writes.Load(), stderr)
				}
			}
		})
	}
}

func TestTransitionWorkflowMetadataAcceptsSuppliedValuesAndDefaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, metadata, payload string }{
		{"field supplied", `{"required":true}`, `{"fields":{"customfield_10001":"fixture"}}`},
		{"zero supplied", `{"required":true}`, `{"fields":{"customfield_10001":0}}`},
		{"false supplied", `{"required":true}`, `{"fields":{"customfield_10001":false}}`},
		{"operation supplied", `{"required":true}`, `{"update":{"customfield_10001":[{"set":"fixture"}]}}`},
		{"default advertised", `{"required":true,"hasDefaultValue":true}`, `{"fields":{}}`},
		{"default returned", `{"required":true,"defaultValue":"fixture"}`, `{"fields":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":true,"fields":{"customfield_10001":`+tc.metadata+`}}]}`)
					return
				}
				writes.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "--transition=Done", "--json-input="+transitionPayloadFile(t, tc.payload), "--no-input", "--output=json")
			var frame nativeTransitionFrame
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || !frame.Data.Results[0].Data.Attempted || !frame.Data.Results[0].Data.Applied || writes.Load() != 1 {
				t.Fatalf("valid workflow input refused: exit=%d writes=%d stderr=%q", exit, writes.Load(), stderr)
			}
		})
	}
}

func TestTransitionNativeInputFormsAndConflicts(t *testing.T) {
	t.Parallel()
	const document = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"fixture"}]}]}`
	for _, tc := range []struct {
		name, body, route string
		args              []string
		exit              int
	}{
		{"stdin target", `{"transition":{"id":"31"},"fields":{},"update":{}}`, "stdin", nil, 0},
		{"payload alias", `{"transition":{"id":"31"},"fields":{}}`, "from-json", nil, 0},
		{"ADF alias with payload", `{"transition":{"id":"31"},"fields":{}}`, "body-adf", nil, 0},
		{"matching target", `{"transition":{"id":"31"}}`, "json-input", []string{"--transition=31"}, 0},
		{"case-insensitive target", `{"transition":{"name":"Done"}}`, "json-input", []string{"--transition=done"}, 0},
		{"flat fields", `{"resolution":{"name":"Fixed"},"customfield_10001":false}`, "json-input", []string{"--transition=Done"}, 0},
		{"flat fields with target", `{"transition":{"name":"Done"},"resolution":{"name":"Fixed"},"customfield_10001":false}`, "json-input", nil, 0},
		{"ADF document", document, "json-input", []string{"--transition=31"}, 0},
		{"convenience comment", `{"transition":{"id":"31"},"comment":` + document + `}`, "json-input", nil, 0},
		{"different targets", `{"transition":{"id":"31"}}`, "json-input", []string{"--transition=41"}, cli.ExitValidation},
		{"field flag conflict", `{"transition":{"id":"31"},"fields":{"resolution":{"name":"Fixed"}}}`, "json-input", []string{"--resolution=Duplicate"}, cli.ExitValidation},
		{"operation flag conflict", `{"transition":{"id":"31"},"update":{"assignee":[{"set":{"accountId":"fixture"}}]}}`, "json-input", []string{"--assignee=other-fixture"}, cli.ExitValidation},
		{"comment route conflict", `{"transition":{"id":"31"}}`, "json-input", []string{"--comment=fixture"}, cli.ExitValidation},
		{"atomic without comment", `{"transition":{"id":"31"}}`, "json-input", []string{"--atomic-only"}, cli.ExitValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := []string{"issue", "transition", "KEY-1", "--dry-run", "--no-input", "--output=json"}
			args = append(args, tc.args...)
			var stdout, stderr string
			var exit int
			if tc.route == "stdin" {
				args = append(args, "--json-input=-")
				stdout, stderr, exit = runBinaryEnvInputStarted(t, nil, tc.body, nil, args...)
			} else {
				args = append(args, "--"+tc.route+"="+transitionPayloadFile(t, tc.body))
				stdout, stderr, exit = runBinary(t, args...)
			}
			if exit != tc.exit {
				t.Fatalf("transition input form changed: exit=%d stderr=%q", exit, stderr)
			}
			if exit != 0 {
				return
			}
			var frame nativeTransitionFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Data.Results) != 1 || frame.Data.Results[0].Issue.Key != "KEY-1" || frame.Data.Results[0].Data.Attempted || frame.Data.Results[0].Data.Applied || len(frame.Data.Payload) == 0 {
				t.Fatal("transition input route discarded its prepared local request")
			}
			if tc.name == "flat fields" || tc.name == "flat fields with target" {
				var payload struct{ Fields map[string]json.RawMessage }
				if json.Unmarshal(frame.Data.Payload, &payload) != nil || string(payload.Fields["customfield_10001"]) != "false" || string(payload.Fields["resolution"]) != `{"name":"Fixed"}` {
					t.Fatal("flat transition field input was not normalized losslessly")
				}
			}
			if tc.name == "ADF document" || tc.name == "convenience comment" {
				var payload struct {
					Update struct {
						Comment []struct {
							Add struct{ Body json.RawMessage }
						}
					}
				}
				if json.Unmarshal(frame.Data.Payload, &payload) != nil || len(payload.Update.Comment) != 1 || string(payload.Update.Comment[0].Add.Body) != document {
					t.Fatal("ADF transition input lost its native comment body")
				}
			}
		})
	}
}

func TestTransitionNativeTargetExtensionsSurvivePreviewAndWire(t *testing.T) {
	t.Parallel()
	for _, target := range []string{`"id":"31"`, `"name":"Done"`} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			body := `{"transition":{` + target + `,"looped":false,"future":{"text":"<>&\u2028","count":9007199254740993}},"fields":{},"update":{}}`
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false}]}`)
					return
				}
				writes.Add(1)
				var request struct{ Transition map[string]json.RawMessage }
				if json.NewDecoder(r.Body).Decode(&request) != nil || string(request.Transition["id"]) != `"31"` || string(request.Transition["looped"]) != "false" || string(request.Transition["future"]) != `{"text":"<>&\u2028","count":9007199254740993}` {
					t.Error("native transition target extensions changed on the wire")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			for _, preview := range []bool{true, false} {
				args := []string{"issue", "transition", "KEY-1", "--json-input=" + transitionPayloadFile(t, body), "--no-input", "--output=json"}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var f nativeTransitionFrame
				var request struct{ Transition map[string]json.RawMessage }
				if exit != 0 || json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || json.Unmarshal(f.Data.Payload, &request) != nil || string(request.Transition["looped"]) != "false" || string(request.Transition["future"]) != `{"text":"<>&\u2028","count":9007199254740993}` {
					t.Fatalf("native target extensions lost (preview=%t): exit=%d stderr=%q", preview, exit, stderr)
				}
				if preview && writes.Load() != 0 {
					t.Fatal("native local preview wrote")
				}
			}
			if writes.Load() != 1 {
				t.Fatal("native target extension write skipped or replayed")
			}
		})
	}
}

func TestTransitionEmptyPropertiesRetainScreenlessRequest(t *testing.T) {
	t.Parallel()
	for _, properties := range []string{"[]", "[ ]"} {
		t.Run(properties, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","hasScreen":false}]}`)
					return
				}
				writes.Add(1)
				var payload map[string]json.RawMessage
				var values []json.RawMessage
				if json.NewDecoder(r.Body).Decode(&payload) != nil || json.Unmarshal(payload["properties"], &values) != nil || values == nil || len(values) != 0 || string(payload["fields"]) != "{}" || string(payload["update"]) != "{}" {
					t.Error("empty native transition containers changed on the wire")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			body := `{"transition":{"id":"31"},"fields":{},"update":{},"properties":` + properties + `}`
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "--json-input="+transitionPayloadFile(t, body), "--no-input", "--output=json")
			var f nativeTransitionFrame
			if exit != 0 || json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || writes.Load() != 1 || len(f.Data.Results) != 1 || !f.Data.Results[0].Data.Applied {
				t.Fatalf("empty properties refused a screenless transition: exit=%d writes=%d stderr=%q", exit, writes.Load(), stderr)
			}
		})
	}
}
