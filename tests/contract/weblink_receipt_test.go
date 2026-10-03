package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type weblinkReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data *struct {
		URL      string                                  `json:"url"`
		Title    string                                  `json:"title"`
		GlobalID string                                  `json:"global_id"`
		Results  []envelope.KeyedResult[json.RawMessage] `json:"results"`
	} `json:"data"`
	Errors []envelope.Error `json:"errors"`
}

const weblinkReceiptIssue = `[{"id":10042,"object":{"url":"https://example.com/runbook","title":"Runbook"}}]`

func TestWeblinkOmittedTitleUsesURL(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "preview"}[preview], func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				var body struct {
					Object map[string]json.RawMessage `json:"object"`
				}
				if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&body) != nil || string(body.Object["title"]) != `"https://example.com/runbook"` || string(body.Object["url"]) != `"https://example.com/runbook"` {
					t.Error("omitted title did not use the URL in the native request")
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":10042}`))
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "weblink", "add", "KEY-1", "--url=https://example.com/runbook", "--output=json", "--no-input"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame weblinkReceiptFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			want := 1
			if preview {
				want = 0
			}
			if exit != 0 || got != want || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil || frame.Data.Title != "https://example.com/runbook" || frame.Data.URL != "https://example.com/runbook" || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK {
				t.Fatalf("omitted title lost supported request/preview behavior: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func weblinkReceiptArgs(verb, mode string, keys ...string) []string {
	args := append([]string{"issue", "weblink", verb}, keys...)
	args = append(args, "--parallelism=1", "--no-input")
	if verb == "add" {
		args = append(args, "--url=https://example.com/runbook", "--title=Runbook", "--global-id=fixture-link")
	}
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func TestWeblinkTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add"} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/remotelink")
					if verb == "add" {
						var body struct {
							Object   struct{ URL, Title string } `json:"object"`
							GlobalID string                      `json:"globalId"`
						}
						if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/remotelink") || json.NewDecoder(req.Body).Decode(&body) != nil || body.Object.URL != "https://example.com/runbook" || body.Object.Title != "Runbook" || body.GlobalID != "fixture-link" {
							t.Error("remote-link add changed its authored method or payload")
						}
					} else if req.Method != http.MethodGet || len(req.URL.Query()) != 0 {
						t.Error("link listing changed its read boundary")
					}
					mu.Lock()
					calls = append(calls, key)
					mu.Unlock()
					if key == "KEY-1" {
						w.WriteHeader(http.StatusNotFound)
					} else {
						w.WriteHeader(http.StatusForbidden)
					}
					_, _ = w.Write([]byte(`{"errorMessages":["fixture denied\u001b[31m\u0007"]}`))
				}))
				t.Cleanup(server.Close)
				env := editReceiptEnv(server.URL)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				}
				args := weblinkReceiptArgs(verb, mode, "KEY-1", "KEY-2")
				if mode == "json" || mode == "compact" {
					args = append(args, "--jq", `error("failed links bypass jq")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				mu.Lock()
				got := append([]string(nil), calls...)
				mu.Unlock()
				if exit != cli.ExitAuth || len(got) != 2 || got[0] != "KEY-1" || got[1] != "KEY-2" || strings.ContainsAny(stderr, "\x1b\a") {
					t.Fatalf("link failure changed classification, requests or control sanitization: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.ContainsAny(stdout, "\x1b\a") {
						t.Fatal("link failure discarded sanitized human outcomes")
					}
					return
				}
				var frame weblinkReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || len(frame.Data.Results) != 2 {
					t.Fatal("link total failure discarded ordered outcomes")
				}
				if verb == "add" && (frame.Data.URL != "https://example.com/runbook" || frame.Data.Title != "Runbook" || frame.Data.GlobalID != "fixture-link") {
					t.Fatal("remote-link failure discarded its authored request")
				}
				for i, code := range []string{"jira_not_found", "jira_forbidden"} {
					row := frame.Data.Results[i]
					if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code || row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] {
						t.Fatal("link failure reordered verdicts or fabricated a relationship")
					}
				}
			})
		}
	}
}

func TestWeblinkInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, verb := range []string{"list", "add"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				started := make(chan struct{})
				var once sync.Once
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/remotelink")
					if verb == "add" {
						var body json.RawMessage
						if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/remotelink") || json.NewDecoder(req.Body).Decode(&body) != nil {
							t.Error("interrupted remote-link add changed its request")
						}
					} else if req.Method != http.MethodGet {
						t.Error("link read became a write")
					}
					mu.Lock()
					calls = append(calls, key)
					mu.Unlock()
					switch key {
					case "KEY-1":
						if verb == "add" {
							w.WriteHeader(http.StatusCreated)
							_, _ = w.Write([]byte(`{"id":10042}`))
						} else {
							_, _ = w.Write([]byte(weblinkReceiptIssue))
						}
					case "KEY-2":
						once.Do(func() { close(started) })
						<-req.Context().Done()
					default:
						t.Error("interrupted link batch requested a queued target")
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
					select {
					case <-started:
					case <-time.After(5 * time.Second):
						t.Fatal("link batch did not reach the independent cancellation stimulus")
					}
					if err := process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}, weblinkReceiptArgs(verb, mode, "KEY-1", "KEY-2", "KEY-3")...)
				mu.Lock()
				got := append([]string(nil), calls...)
				mu.Unlock()
				if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "KEY-1" || got[1] != "KEY-2" {
					t.Fatalf("link interrupt changed classification or replayed work: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					completed := "Runbook"
					if verb == "add" {
						completed = "web link added"
					}
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || !strings.Contains(stdout, completed) {
						t.Fatal("interrupted link batch discarded human outcomes")
					}
					return
				}
				var frame weblinkReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Results) != 3 {
					t.Fatal("interrupted link batch discarded joined outcomes")
				}
				if verb == "add" && (frame.Data.URL != "https://example.com/runbook" || frame.Data.Title != "Runbook" || frame.Data.GlobalID != "fixture-link") {
					t.Fatal("interrupted remote-link add discarded its request")
				}
				for i, row := range frame.Data.Results {
					if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
						t.Fatal("interrupted link batch reordered targets")
					}
					if i == 0 {
						if !row.OK || row.Data == nil || row.Err != nil {
							t.Fatal("interrupted link batch discarded completed work")
						}
						if verb == "list" {
							var data envelope.IssueWeblinkListData
							if json.Unmarshal(*row.Data, &data) != nil || len(data.Weblinks) != 1 || data.Weblinks[0].ID != "10042" || data.Weblinks[0].URL != "https://example.com/runbook" || data.Weblinks[0].Title != "Runbook" {
								t.Fatal("interrupted link read lost its identity or direction")
							}
						} else {
							var data envelope.IssueWeblinkAddData
							if json.Unmarshal(*row.Data, &data) != nil || data.Weblink == nil || data.Weblink.ID != "10042" || data.Created == nil || !*data.Created {
								t.Fatal("interrupted remote-link add lost its acknowledged identity or creation verdict")
							}
						}
					} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
						t.Fatal("interrupted link batch invented completed work")
					}
				}
			})
		}
	}
}

func TestWeblinkWriteRejectsUnusableAcknowledgements(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusCreated, http.StatusOK} {
		for name, body := range map[string]string{"missing": "{}", "null": "null", "blank": "", "null-id": `{"id":null}`, "zero": `{"id":0}`, "negative": `{"id":-1}`, "string": `{"id":"10042"}`, "malformed": "{", "trailing": `{"id":10042}{"id":10043}`} {
			t.Run(http.StatusText(status)+"/"+name, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/remotelink" {
						t.Error("remote-link acknowledgement guard changed its write boundary")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), weblinkReceiptArgs("add", "json", "KEY-1")...)
				var frame weblinkReceiptFrame
				mu.Lock()
				got := calls
				mu.Unlock()
				if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "weblink_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "Inspect") || !strings.Contains(frame.Errors[0].Hint, "before retrying") {
					t.Fatalf("unusable acknowledgement reported success, replay or unsafe recovery: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
				}
				if frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != "weblink_response_invalid" {
					t.Fatal("uncertain acknowledgement fabricated a link or discarded its target")
				}
			})
		}
	}
}

func TestWeblinkWriteRejectsUnexpectedSuccessStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusAccepted, http.StatusNoContent, http.StatusResetContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/remotelink" {
					t.Error("unexpected-status guard changed its authored write")
				}
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte(`{"id":10042}`))
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), weblinkReceiptArgs("add", "json", "KEY-1")...)
			var frame weblinkReceiptFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "weblink_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") || frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil || frame.Data.Results[0].Err == nil || frame.Data.Results[0].Err.Code != "weblink_response_invalid" {
				t.Fatalf("unexpected acknowledgement invented completion, lost uncertainty or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestWeblinkProtocolFailureOutranksMissingIssue(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/remotelink")
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				method := http.MethodGet
				if verb == "add" {
					method = http.MethodPost
				}
				if req.Method != method || (key != "KEY-1" && key != "KEY-2") || !strings.HasSuffix(req.URL.Path, "/remotelink") {
					t.Error("mixed-failure guard changed its keyed request boundary")
				}
				if key == "KEY-1" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"errorMessages":["missing fixture"]}`))
					return
				}
				if verb == "add" {
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{}`))
				} else {
					_, _ = w.Write([]byte(`null`))
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), weblinkReceiptArgs(verb, "json", "KEY-1", "KEY-2")...)
			var frame weblinkReceiptFrame
			mu.Lock()
			got := slices.Clone(calls)
			mu.Unlock()
			if exit != cli.ExitServer || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "weblink_response_invalid" || frame.Data == nil || len(frame.Data.Results) != 2 {
				t.Fatalf("missing issue hid a protocol fault or uncertain write: exit=%d calls=%v stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
			for i, code := range []string{"jira_not_found", "weblink_response_invalid"} {
				row := frame.Data.Results[i]
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatal("mixed failure lost ordered target diagnostics or invented a receipt")
				}
			}
		})
	}
}

func TestWeblinkRefusesNonWebSchemesBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"ftp://example.com/path", "javascript://example.com/payload", "file://example.com/path", "data://example.com/path", "https://:443/path"} {
		for _, preview := range []bool{false, true} {
			t.Run(url+map[bool]string{false: "/write", true: "/preview"}[preview], func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":10042}`))
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "weblink", "add", "KEY-1", "--url=" + url, "--title=Runbook", "--output=json", "--no-input"}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame weblinkReceiptFrame
				mu.Lock()
				got := calls
				mu.Unlock()
				if exit != cli.ExitValidation || got != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != "url" {
					t.Fatalf("non-web URL escaped local refusal: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
				}
			})
		}
	}
}

func TestWeblinkPublishedHelpProjectionsExecute(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-123/remotelink" || len(req.URL.Query()) != 0 {
			t.Error("published remote-link recipe changed its read boundary")
		}
		_, _ = w.Write([]byte(weblinkReceiptIssue))
	}))
	t.Cleanup(server.Close)
	for _, verb := range []string{"list", "delete"} {
		help, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "weblink", verb, "--help")
		if exit != 0 {
			t.Fatalf("remote-link help failed: %d %q", exit, stderr)
		}
		found := 0
		for _, line := range strings.Split(help, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(args, "--jq") {
				continue
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if verb == "list" {
				if exit != 0 || strings.TrimSpace(stdout) != "https://example.com/runbook" {
					t.Fatalf("literal published list projection failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
			} else {
				var row struct{ ID, URL string }
				if exit != 0 || json.Unmarshal([]byte(stdout), &row) != nil || row.ID != "10042" || row.URL != "https://example.com/runbook" {
					t.Fatalf("literal published delete lookup failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
			}
			found++
		}
		if found != 1 {
			t.Fatal("published help omitted its executable projection")
		}
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatal("literal help recipes did not execute exactly two reads")
	}
}

func TestWeblinkListRejectsUnusableObservations(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null": "null", "missing-id": `[{}]`, "null-id": `[{"id":null}]`, "zero": `[{"id":0}]`,
		"repeated-id": `[{"id":10042},{"id":10042}]`, "trailing": `[{"id":10042}][]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1/remotelink" || len(req.URL.Query()) != 0 {
					t.Error("remote-link observation changed its read boundary")
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), weblinkReceiptArgs("list", "json", "KEY-1")...)
			var frame weblinkReceiptFrame
			if exit != cli.ExitServer || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "weblink_response_invalid" || frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil {
				t.Fatalf("unusable observation invented empty state or identity: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
		})
	}
}
