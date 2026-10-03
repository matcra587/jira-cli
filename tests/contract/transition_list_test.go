package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/issue"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const transitionListNative = `{"expand":"transitions.fields","future":900719925474099312345,"transitions":[{"id":"31","name":"Finish","to":{"id":"42","name":"Done"},"hasScreen":true,"fields":{"resolution":{"required":true,"name":"Resolution","schema":{"type":"resolution"},"allowedValues":[{"id":"7","name":"Fixed"}],"future":null}},"future":"<&>\u2028"},{"id":"11","name":"Restart","to":{"id":"40","name":"To Do"},"hasScreen":false,"fields":{}}]}`

func TestTransitionListUnexpectedNativeSuccessUsesProtocolError(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusCreated, http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(http.StatusText(status)+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodGet {
						t.Error("workflow discovery attempted a write")
					}
					w.WriteHeader(status)
					if status != http.StatusNoContent {
						_, _ = w.Write([]byte(transitionListNative))
					}
				}))
				t.Cleanup(server.Close)
				stdout, _, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1"), transitionListArgs(mode, "KEY-1")...)
				if exit != cli.ExitServer || calls.Load() != 1 {
					t.Fatal("native protocol error blamed input or retried an unexpected success status")
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "unexpected HTTP status") {
						t.Fatal("human protocol error omitted its target or cause")
					}
					return
				}
				var frame transitionListFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "transition_read_invalid" || frame.Data == nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Data != nil {
					t.Fatal("protocol error lost ordered failure or published a native workflow")
				}
			})
		}
	}
}

func TestTransitionListRetainsNativeMetadataInAllModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var keys []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
				if req.Method != http.MethodGet || req.URL.Query().Get("expand") != "transitions.fields" || len(req.URL.Query()) != 1 {
					t.Error("transition discovery changed its native metadata read")
				}
				mu.Lock()
				keys = append(keys, key)
				mu.Unlock()
				_, _ = w.Write([]byte(transitionListNative))
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "transition", "list", "KEY-1", "KEY-2", "--parallelism=1", "--no-input"}
			if mode != "agent" {
				args = append(args, "--output="+mode)
			}
			stdout, _, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true", "AGENT=1"), args...)
			mu.Lock()
			got := slices.Clone(keys)
			mu.Unlock()
			if exit != 0 || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) {
				t.Fatal("transition discovery did not read ordered targets in read-only mode")
			}
			if mode == "human" {
				for _, text := range []string{"KEY-1", "KEY-2", "31", "Finish", "Done", "resolution", "required"} {
					if !strings.Contains(stdout, text) {
						t.Fatal("human transition discovery omitted identity, destination or required-field metadata")
					}
				}
				return
			}
			format := mode
			if mode == "agent" {
				format = "compact"
			}
			var out struct {
				Results []struct {
					Issue struct{ Key string }
					OK    bool
					Data  json.RawMessage
				}
			}
			if json.Unmarshal(adfData(t, stdout, format, "issue.transition.list", true), &out) != nil || len(out.Results) != 2 {
				t.Fatal("transition discovery lost its typed results")
			}
			for index, row := range out.Results {
				var actual, expected any
				decoder := json.NewDecoder(strings.NewReader(string(row.Data)))
				decoder.UseNumber()
				if decoder.Decode(&actual) != nil {
					t.Fatal("transition discovery lost its native payload")
				}
				decoder = json.NewDecoder(strings.NewReader(transitionListNative))
				decoder.UseNumber()
				if decoder.Decode(&expected) != nil {
					t.Fatal("invalid native stimulus")
				}
				a, _ := json.Marshal(actual)
				e, _ := json.Marshal(expected)
				if !row.OK || row.Issue.Key != []string{"KEY-1", "KEY-2"}[index] || string(a) != string(e) || !strings.Contains(string(row.Data), "900719925474099312345") {
					t.Fatal("transition discovery reordered targets or lost native metadata and extensions")
				}
			}
		})
	}
}

func TestTransitionListOutputFailuresRetainReadsWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run("filter/"+mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodGet {
					t.Error("discovery attempted a write")
				}
				_, _ = w.Write([]byte(transitionListNative))
			}))
			t.Cleanup(server.Close)
			args := append(transitionListArgs(mode, "KEY-1"), "--jq", `error("filter failure")`)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame transitionListFrame
			if exit != cli.ExitValidation || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jq_eval_failed" || frame.Data == nil || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || frame.Data.Results[0].Data == nil {
				t.Fatal("filter failure discarded native discovery or replayed its read")
			}
		})
	}
	if !xos.IsUnix() {
		return
	}
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run("pipe/"+mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodGet {
					t.Error("discovery attempted a write")
				}
				_, _ = w.Write([]byte(transitionListNative))
			}))
			t.Cleanup(server.Close)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Close() }()
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), jiraBinary(t), transitionListArgs(mode, "KEY-1")...)
			cmd.Dir = dir
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb", "AGENT=1"}, editReceiptEnv(server.URL)...)
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = writer, &stderr
			err = cmd.Run()
			failure, ok := errors.AsType[*exec.ExitError](err)
			if !ok || failure.ExitCode() != cli.ExitOutput || calls.Load() != 1 {
				t.Fatalf("discovery closed pipe bypassed exit8 or replayed: err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestTransitionListDiscoveryAndPublishedExamplesExecute(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.Method != http.MethodGet || req.URL.Query().Get("expand") != "transitions.fields" {
			t.Error("published workflow attempted a different request")
		}
		_, _ = w.Write([]byte(transitionListNative))
	}))
	t.Cleanup(server.Close)
	local := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
	stdout, stderr, exit := runBinaryEnv(t, local, "agent", "schema", "--path=issue transition list", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatal("local transition schema discovery failed")
	}
	tree, _ := readSchema(t, stdout, "json")
	if tree.Path != "jira issue transition list" || !tree.ReadOnly || tree.OutputSchema == nil {
		t.Fatal("schema lost native workflow read classification or shape")
	}
	props, ok := tree.OutputSchema["properties"].(map[string]any)
	if !ok || props["results"] == nil {
		t.Fatal("workflow schema omitted ordered native results")
	}
	flags := make([]string, 0, len(tree.Flags))
	for _, flag := range tree.Flags {
		flags = append(flags, flag.Name)
	}
	if !slices.Equal(flags, []string{"help", "parallelism"}) {
		t.Fatal("workflow discovery acquired mutation flags or lost its concurrency flag")
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "completion", "fish")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, `"jira issue transition list"`) || requests.Load() != 0 {
		t.Fatal("completion omitted workflow read or contacted Jira")
	}
	cmd, _, err := issue.New(issue.Deps{}).Find([]string{"transition", "list"})
	if err != nil {
		t.Fatal(err)
	}
	examples := 0
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, _, exit = runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
		if exit != 0 || !strings.Contains(stdout, "resolution") {
			t.Fatal("published transition-discovery example did not execute")
		}
		examples++
	}
	if examples != 2 || requests.Load() != 3 {
		t.Fatal("workflow examples changed without exact request coverage")
	}
}

type transitionListFrame struct {
	OK     bool                                `json:"ok"`
	Data   *envelope.IssueTransitionListOutput `json:"data"`
	Errors []envelope.Error                    `json:"errors"`
}

func transitionListArgs(mode string, keys ...string) []string {
	args := append([]string{"issue", "transition", "list"}, keys...)
	args = append(args, "--parallelism=1", "--no-input")
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func TestTransitionListFailureRetainsOrderedBinaryResults(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(mode+map[bool]string{false: "/total", true: "/partial"}[partial], func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
					if req.Method != http.MethodGet || req.URL.Query().Get("expand") != "transitions.fields" {
						t.Error("discovery changed read scope")
					}
					mu.Lock()
					calls = append(calls, key)
					mu.Unlock()
					if partial && key == "KEY-1" {
						_, _ = w.Write([]byte(transitionListNative))
						return
					}
					if key == "KEY-1" {
						w.WriteHeader(http.StatusNotFound)
					} else {
						w.WriteHeader(http.StatusForbidden)
					}
					_, _ = w.Write([]byte(`{"errorMessages":["fixture denied\u001b[31m\u0007"]}`))
				}))
				t.Cleanup(server.Close)
				args := transitionListArgs(mode, "KEY-1", "KEY-2")
				if !partial && (mode == "json" || mode == "compact") {
					args = append(args, "--jq", `error("failed reads bypass jq")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "AGENT=1", "JIRA_READ_ONLY=true"), args...)
				mu.Lock()
				got := slices.Clone(calls)
				mu.Unlock()
				wantExit := cli.ExitAuth
				if partial {
					wantExit = cli.ExitOK
				}
				if exit != wantExit || !slices.Equal(got, []string{"KEY-1", "KEY-2"}) || strings.ContainsAny(stderr, "\x1b\a") {
					t.Fatalf("discovery failure changed reads or classification: exit=%d calls=%v", exit, got)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "fixture denied") || strings.ContainsAny(stdout, "\x1b\a") || (partial && !strings.Contains(stdout, "resolution")) {
						t.Fatal("human failure lost keyed outcomes or safe native metadata")
					}
					return
				}
				var frame transitionListFrame
				if partial {
					format := mode
					if mode == "agent" {
						format = "compact"
					}
					frame.Data = &envelope.IssueTransitionListOutput{}
					if json.Unmarshal(adfData(t, stdout, format, "issue.transition.list", true), frame.Data) != nil {
						t.Fatal("partial discovery discarded its typed results")
					}
				} else if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || len(frame.Errors) != 1 {
					t.Fatal("total discovery failure discarded its frame")
				}
				if len(frame.Data.Results) != 2 {
					t.Fatal("discovery lost a requested target")
				}
				for i, row := range frame.Data.Results {
					if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] {
						t.Fatal("failed discovery reordered targets")
					}
					if partial && i == 0 {
						if !row.OK || row.Err != nil || row.Data == nil || len(row.Data.Transitions.Value) != 2 {
							t.Fatal("partial failure lost successful metadata")
						}
					} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != []string{"jira_not_found", "jira_forbidden"}[i] {
						t.Fatal("failed discovery invented a native list")
					}
				}
			})
		}
	}
}

func TestTransitionListCancellationAndRequestTimeoutRetainTargets(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		if !deadline && runtime.GOOS == "windows" {
			continue
		}
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(mode+map[bool]string{false: "/interrupt", true: "/deadline"}[deadline], func(t *testing.T) {
				t.Parallel()
				started := make(chan struct{})
				var once sync.Once
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/rest/api/3/issue/"), "/transitions")
					if req.Method != http.MethodGet {
						t.Error("discovery attempted a write")
					}
					mu.Lock()
					calls = append(calls, key)
					mu.Unlock()
					switch key {
					case "KEY-1":
						_, _ = w.Write([]byte(transitionListNative))
					case "KEY-2":
						once.Do(func() { close(started) })
						<-req.Context().Done()
					default:
						if deadline {
							_, _ = w.Write([]byte(transitionListNative))
							return
						}
						t.Error("queued discovery reached the wire after cancellation")
						w.WriteHeader(400)
					}
				}))
				t.Cleanup(server.Close)
				args := transitionListArgs(mode, "KEY-1", "KEY-2", "KEY-3")
				if deadline {
					args = append(args, "--timeout=1s")
				}
				stdout, stderr, exit := runBinaryEnvStarted(t, append(editReceiptEnv(server.URL), "AGENT=1"), func(process *os.Process) {
					select {
					case <-started:
					case <-time.After(5 * time.Second):
						t.Fatal("discovery did not reach cancellation stimulus")
					}
					if !deadline {
						if err := process.Signal(os.Interrupt); err != nil {
							t.Fatal(err)
						}
					}
				}, args...)
				wantExit, wantCode := cli.ExitCanceled, cli.CodeCanceled
				wantCalls := []string{"KEY-1", "KEY-2"}
				if deadline {
					// --timeout bounds each HTTP request. Safe read retries and
					// subsequent targets continue under the live command context.
					wantExit, wantCode = cli.ExitOK, "timeout"
					wantCalls = []string{"KEY-1", "KEY-2", "KEY-2", "KEY-2", "KEY-3"}
				}
				mu.Lock()
				got := slices.Clone(calls)
				mu.Unlock()
				if exit != wantExit || !slices.Equal(got, wantCalls) {
					t.Fatalf("canceled discovery changed read order or classification: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					for _, key := range []string{"KEY-1", "KEY-2", "KEY-3", "resolution"} {
						if !strings.Contains(stdout, key) {
							t.Fatal("canceled human discovery lost outcomes")
						}
					}
					return
				}
				var frame transitionListFrame
				if deadline {
					format := mode
					if mode == "agent" {
						format = "compact"
					}
					frame.Data = &envelope.IssueTransitionListOutput{}
					if json.Unmarshal(adfData(t, stdout, format, "issue.transition.list", true), frame.Data) != nil {
						t.Fatal("request timeout discarded partial native reads")
					}
				} else if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode {
					t.Fatal("interrupted discovery discarded its joined frame")
				}
				if len(frame.Data.Results) != 3 {
					t.Fatal("discovery lost a requested target")
				}
				for i, row := range frame.Data.Results {
					if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[i] {
						t.Fatal("canceled discovery reordered targets")
					}
					if i == 0 || (deadline && i == 2) {
						if !row.OK || row.Err != nil || row.Data == nil || len(row.Data.Transitions.Value) != 2 {
							t.Fatal("canceled discovery lost completed native data")
						}
					} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != wantCode {
						t.Fatal("canceled discovery fabricated a completed read")
					}
				}
			})
		}
	}
}

func TestTransitionListRefusalsAndParentRecoveryExecuteWithoutWrites(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1/transitions" {
			t.Error("recovery changed scope or attempted a write")
		}
		_, _ = w.Write([]byte(transitionListNative))
	}))
	t.Cleanup(server.Close)
	for _, flags := range [][]string{{"--parallelism=0"}, {"--web"}, {"--transition=31"}, {"--comment=unexpected"}} {
		stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(transitionListArgs("json", "KEY-1"), flags...)...)
		var frame transitionListFrame
		if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 {
			t.Fatal("invalid discovery flags made a request or lacked a typed refusal")
		}
	}
	stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "--output=json", "--no-input")
	var frame transitionListFrame
	if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Errors) != 1 || !slices.Contains(frame.Errors[0].Suggestions, "jira issue transition list KEY-1") {
		t.Fatal("missing transition target did not offer an executable read recovery")
	}
	stdout, _, exit = runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), strings.Fields(strings.TrimPrefix(frame.Errors[0].Suggestions[0], "jira "))...)
	if exit != 0 || requests.Load() != 1 || !strings.Contains(stdout, "resolution") {
		t.Fatal("suggested recovery did not complete native workflow discovery")
	}
}

func TestTransitionUnusableDiscoveryPreventsWrites(t *testing.T) {
	t.Parallel()
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			reads.Add(1)
		} else {
			writes.Add(1)
		}
		_, _ = w.Write([]byte(`{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"}}]}`))
	}))
	t.Cleanup(server.Close)
	stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "transition", "KEY-1", "Done", "--comment=must not post", "--output=json", "--no-input")
	var frame transitionReceiptFrame
	if exit != cli.ExitServer || reads.Load() != 1 || writes.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "transition_read_invalid" {
		t.Fatal("unusable workflow metadata authorized mutation or lost classification")
	}
}
