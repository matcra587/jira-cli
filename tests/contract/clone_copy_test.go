package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueCloneRejectsInvalidFullSourceThroughTheBinary(t *testing.T) {
	t.Parallel()
	valid := `{"key":"KEY-2","fields":{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"trailing object", valid + ` {}`, http.StatusOK},
		{"trailing junk", valid + ` trailing`, http.StatusOK},
		{"malformed declared field", `{"key":"KEY-2","fields":{"summary":false}}`, http.StatusOK},
		{"array fields", `{"key":"KEY-2","fields":[]}`, http.StatusOK},
		{"created instead of read", valid, http.StatusCreated},
		{"no content", "", http.StatusNoContent},
		{"partial status", valid, http.StatusPartialContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-1":
					w.WriteHeader(http.StatusNotFound)
				case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-2":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				case req.Method == http.MethodPost && req.URL.Path == "/rest/api/3/issue":
					writes.Add(1)
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"7002","key":"NEW-1"}`))
				default:
					t.Errorf("unexpected source guard request %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "clone", "KEY-1", "KEY-2", "--parallelism=1", "--output=json", "--no-input")
			var frame cloneReceiptFrame
			if exit != cli.ExitServer || writes.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeIssueCloneSourceInvalid || frame.Data == nil || len(frame.Data.Results) != 2 || frame.Data.Results[1].OK || frame.Data.Results[1].Data != nil || frame.Data.Results[1].Err == nil || frame.Data.Results[1].Err.Code != cli.CodeIssueCloneSourceInvalid {
				t.Fatalf("invalid full source reached creation or lost protocol severity: exit=%d writes=%d stdout=%s", exit, writes.Load(), stdout)
			}
		})
	}
}

func TestIssueCloneUpdateOverrideReplacesCopiedFieldThroughTheBinary(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "override.json")
			if err := os.WriteFile(path, []byte(`{"update":{"labels":[{"set":["override"]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-1" {
					_, _ = w.Write([]byte(`{"key":"KEY-1","fields":{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"},"labels":["source"]}}`))
					return
				}
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
					t.Errorf("unexpected update override request %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writes.Add(1)
				var body struct {
					Fields map[string]json.RawMessage `json:"fields"`
					Update map[string]json.RawMessage `json:"update"`
				}
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Error("invalid native clone request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if _, copied := body.Fields["labels"]; copied || string(body.Update["labels"]) != `[{"set":["override"]}]` || string(body.Fields["summary"]) != `"Source"` {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"errorMessages":["copied field collides with native override"]}`))
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"7002","key":"NEW-1"}`))
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"issue", "clone", "KEY-1", "--json-input", path, "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, _, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || writes.Load() != 1 || !strings.Contains(stdout, "NEW-1") {
				t.Fatalf("native update did not replace the copied field: exit=%d writes=%d stdout=%s", exit, writes.Load(), stdout)
			}
		})
	}
}

func TestIssueCloneCopiesNativeFieldsThroughTheBinary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, value string }{
		{"opaque object", `{"futureType":"native <&>\u2028","values":[null,false,0]}`},
		{"explicit null", `null`},
		{"literal line separator", "\"native <&>\u2028\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"json", "compact", "human", "agent"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					var reads, metadata, writes atomic.Int32
					var mu sync.Mutex
					var posted map[string]json.RawMessage
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						switch {
						case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issue/KEY-1":
							reads.Add(1)
							full := `{"summary":"Source","project":{"id":"7100","key":"KEY"},"issuetype":{"id":"7101","name":"Task"},"assignee":{"accountId":"source-person","displayName":"Read-only display"},"customfield_10011":` + tc.value + `,"futureNativeField":` + tc.value + `}`
							fields := []byte(full)
							if selected := req.URL.Query().Get("fields"); selected != "" && selected != "*all" {
								var all map[string]json.RawMessage
								if json.Unmarshal(fields, &all) != nil {
									t.Error("invalid authored source fixture")
									w.WriteHeader(http.StatusInternalServerError)
									return
								}
								projection := make(map[string]json.RawMessage)
								for _, name := range strings.Split(selected, ",") {
									if value, present := all[name]; present {
										projection[name] = value
									}
								}
								var err error
								fields, err = json.Marshal(projection)
								if err != nil {
									t.Error(err)
									w.WriteHeader(http.StatusInternalServerError)
									return
								}
							}
							_, _ = w.Write([]byte(`{"id":"7001","key":"KEY-1","fields":` + string(fields) + `}`))
						case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/field":
							metadata.Add(1)
							_, _ = w.Write([]byte(`[{"id":"customfield_10011","schema":{"custom":"example:text"}}]`))
						case req.Method == http.MethodPost && req.URL.Path == "/rest/api/3/issue":
							writes.Add(1)
							var body struct {
								Fields map[string]json.RawMessage `json:"fields"`
							}
							if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
								t.Error(err)
								w.WriteHeader(http.StatusBadRequest)
								return
							}
							mu.Lock()
							posted = body.Fields
							mu.Unlock()
							w.WriteHeader(http.StatusCreated)
							_, _ = w.Write([]byte(`{"id":"7002","key":"NEW-1"}`))
						default:
							t.Errorf("unexpected native request %s %s", req.Method, req.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(server.Close)
					env := editReceiptEnv(server.URL)
					args := []string{"issue", "clone", "KEY-1", "--parallelism=1", "--no-input"}
					if mode == "agent" {
						env = append(env, "AGENT=1")
					} else {
						args = append(args, "--output="+mode)
					}
					stdout, stderr, exit := runBinaryEnv(t, env, args...)
					if exit != 0 || reads.Load() != 1 || metadata.Load() != 1 || writes.Load() != 1 {
						t.Fatalf("clone failed or changed single native dispatch: exit=%d reads=%d writes=%d stderr=%q", exit, reads.Load(), writes.Load(), stderr)
					}
					mu.Lock()
					got := posted
					mu.Unlock()
					for name, want := range map[string]string{
						"project": `{"key":"KEY"}`, "issuetype": `{"name":"Task"}`,
						"assignee": `{"accountId":"source-person"}`, "customfield_10011": tc.value, "futureNativeField": tc.value,
					} {
						if value := string(got[name]); value != want {
							t.Errorf("posted %s = %s, want native copy %s", name, value, want)
						}
					}
					if mode == "human" {
						if !strings.Contains(stdout, "NEW-1") {
							t.Fatal("human copy omitted the created identity")
						}
						return
					}
					var out envelope.IssueCloneOutput
					if mode == "compact" || mode == "agent" {
						if err := json.Unmarshal([]byte(stdout), &out); err != nil {
							t.Fatal(err)
						}
					} else {
						var frame cloneReceiptFrame
						if err := json.Unmarshal([]byte(stdout), &frame); err != nil || !frame.OK || frame.Data == nil {
							t.Fatalf("native copy lost its successful frame: %v", err)
						}
						out = *frame.Data
					}
					if len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || !out.Results[0].Data.Applied || out.Results[0].Data.Clone == nil || out.Results[0].Data.Clone.Key != "NEW-1" {
						t.Fatal("native copy lost its acknowledged new identity")
					}
				})
			}
		})
	}
}
