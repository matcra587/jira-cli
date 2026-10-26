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

type linkReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data *struct {
		To           envelope.IssueRef                       `json:"to"`
		TypeResolved bool                                    `json:"type_resolved"`
		Type         *envelope.IssueLinkTypeView             `json:"type"`
		Relation     string                                  `json:"relation"`
		Sentences    []string                                `json:"sentences"`
		Results      []envelope.KeyedResult[json.RawMessage] `json:"results"`
	} `json:"data"`
	Errors []envelope.Error `json:"errors"`
}

const (
	linkReceiptTypes = `{"issueLinkTypes":[{"id":"1","name":"Blocks","inward":"is blocked by","outward":"blocks"}]}`
	linkReceiptIssue = `{"id":"1","key":"KEY-1","fields":{"issuelinks":[{"id":"10042","type":{"id":"1","name":"Blocks","inward":"is blocked by","outward":"blocks"},"outwardIssue":{"id":"9","key":"KEY-9","fields":{"summary":"linked","status":{"name":"To Do"},"issuetype":{"name":"Task"}}}}]}}`
)

func TestLinkPublishedDeleteHelpProjectionExecutes(t *testing.T) {
	t.Parallel()
	var calls int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-123" || req.URL.Query().Get("fields") != "issuelinks" {
			t.Error("published link lookup changed its read boundary")
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(linkReceiptIssue, "KEY-1", "KEY-123")))
	}))
	t.Cleanup(server.Close)
	help, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "link", "delete", "--help")
	if exit != 0 {
		t.Fatalf("link delete help failed: %d %q", exit, stderr)
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
		var row struct {
			ID       string `json:"id"`
			Relation string `json:"relation"`
			Key      string `json:"key"`
		}
		if exit != 0 || json.Unmarshal([]byte(stdout), &row) != nil || row.ID != "10042" || row.Relation != "blocks" || row.Key != "KEY-9" {
			t.Fatalf("literal published link lookup failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
		found++
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if found != 1 || got != 1 {
		t.Fatal("published link lookup was not executed exactly once")
	}
}

func TestLinkPublishedCreateHelpProjectionExecutes(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issueLinkType" {
			calls = append(calls, "types")
			_, _ = w.Write([]byte(linkReceiptTypes))
			return
		}
		var body struct {
			Type struct {
				Name string `json:"name"`
			} `json:"type"`
			InwardIssue  envelope.IssueRef `json:"inwardIssue"`
			OutwardIssue envelope.IssueRef `json:"outwardIssue"`
		}
		if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issueLink" || json.NewDecoder(req.Body).Decode(&body) != nil || body.Type.Name != "Blocks" || body.OutwardIssue.Key != "KEY-999" {
			t.Error("published link create changed its method, type or target")
		}
		calls = append(calls, body.InwardIssue.Key)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)
	help, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "link", "create", "--help")
	if exit != 0 {
		t.Fatalf("link create help failed: %d %q", exit, stderr)
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
		if exit != 0 || !slices.Equal(strings.Split(strings.TrimSpace(stdout), "\n"), []string{"KEY-123", "KEY-124"}) {
			t.Fatalf("literal published link create failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
		found++
	}
	mu.Lock()
	got := slices.Clone(calls)
	mu.Unlock()
	if found != 1 || len(got) != 3 || got[0] != "types" || !slices.Contains(got[1:], "KEY-123") || !slices.Contains(got[1:], "KEY-124") {
		t.Fatalf("published link create did not perform its exact type read and two writes: %v", got)
	}
}

func linkReceiptArgs(verb, mode string, keys ...string) []string {
	args := append([]string{"issue", "link", verb}, keys...)
	args = append(args, "--parallelism=1", "--no-input")
	if verb == "create" {
		args = append(args, "--to=KEY-9", "--type=Blocks", "--refresh")
	}
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func TestLinkTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "create"} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if verb == "create" && req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issueLinkType" {
						_, _ = w.Write([]byte(linkReceiptTypes))
						return
					}
					key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
					if verb == "create" {
						var body struct {
							InwardIssue  envelope.IssueRef `json:"inwardIssue"`
							OutwardIssue envelope.IssueRef `json:"outwardIssue"`
						}
						if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issueLink" || json.NewDecoder(req.Body).Decode(&body) != nil || body.OutwardIssue.Key != "KEY-9" {
							t.Error("link creation changed its authored method or target")
						}
						key = body.InwardIssue.Key
					} else if req.Method != http.MethodGet || req.URL.Query().Get("fields") != "issuelinks" {
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
				args := linkReceiptArgs(verb, mode, "KEY-1", "KEY-2")
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
				var frame linkReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || len(frame.Data.Results) != 2 {
					t.Fatal("link total failure discarded ordered outcomes")
				}
				if verb == "create" && (frame.Data.To.Key != "KEY-9" || !frame.Data.TypeResolved || frame.Data.Type == nil || frame.Data.Type.Name != "Blocks" || frame.Data.Relation != "blocks" || !slices.Equal(frame.Data.Sentences, []string{"KEY-1 blocks KEY-9", "KEY-2 blocks KEY-9"})) {
					t.Fatal("link failure discarded the resolved authored relationship")
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

func TestLinkInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, verb := range []string{"list", "create"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				started := make(chan struct{})
				var once sync.Once
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if verb == "create" && req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issueLinkType" {
						_, _ = w.Write([]byte(linkReceiptTypes))
						return
					}
					key := strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/")
					if verb == "create" {
						var body struct {
							InwardIssue envelope.IssueRef `json:"inwardIssue"`
						}
						if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issueLink" || json.NewDecoder(req.Body).Decode(&body) != nil {
							t.Error("interrupted link creation changed its request")
						}
						key = body.InwardIssue.Key
					} else if req.Method != http.MethodGet {
						t.Error("link read became a write")
					}
					mu.Lock()
					calls = append(calls, key)
					mu.Unlock()
					switch key {
					case "KEY-1":
						if verb == "create" {
							w.WriteHeader(http.StatusCreated)
						} else {
							_, _ = w.Write([]byte(linkReceiptIssue))
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
				}, linkReceiptArgs(verb, mode, "KEY-1", "KEY-2", "KEY-3")...)
				mu.Lock()
				got := append([]string(nil), calls...)
				mu.Unlock()
				if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "KEY-1" || got[1] != "KEY-2" {
					t.Fatalf("link interrupt changed classification or replayed work: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || !strings.Contains(stdout, "KEY-9") {
						t.Fatal("interrupted link batch discarded human outcomes")
					}
					return
				}
				var frame linkReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Results) != 3 {
					t.Fatal("interrupted link batch discarded joined outcomes")
				}
				if verb == "create" && (frame.Data.To.Key != "KEY-9" || !frame.Data.TypeResolved || frame.Data.Type == nil || frame.Data.Relation != "blocks") {
					t.Fatal("interrupted link creation discarded its relationship")
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
							var data envelope.IssueLinkListData
							if json.Unmarshal(*row.Data, &data) != nil || len(data.Links) != 1 || data.Links[0].ID != "10042" || data.Links[0].Issue.Key != "KEY-9" || data.Links[0].Relation != "blocks" {
								t.Fatal("interrupted link read lost its identity or direction")
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
