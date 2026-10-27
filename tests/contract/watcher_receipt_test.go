package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestWatcherFailureReceiptBrokenPipeUsesOutputExitContract(t *testing.T) {
	t.Parallel()
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	for _, verb := range []string{"list", "add", "remove"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusNotFound)
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
				cmd := exec.CommandContext(t.Context(), jiraBinary(t), watcherReceiptArgs(verb, mode, "KEY-1")...)
				cmd.Dir = dir
				cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb"}, editReceiptEnv(server.URL)...)
				var stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = writer, &stderr
				err = cmd.Run()
				var failure *exec.ExitError
				if !errors.As(err, &failure) || failure.ExitCode() != cli.ExitOutput || calls.Load() != 1 {
					t.Fatalf("failed watcher receipt bypassed exit8 or replayed work: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
				}
			})
		}
	}
}

func TestWatcherLocalRefusalPublishesNoReceiptOrRequests(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, verb := range []string{"add", "remove"} {
		stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--output=json", "--no-input")
		var frame struct {
			OK     bool             `json:"ok"`
			Data   json.RawMessage  `json:"data"`
			Errors []envelope.Error `json:"errors"`
		}
		if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != cli.ExitValidation || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagRequired || string(frame.Data) != "null" || calls.Load() != 0 {
			t.Fatalf("missing watcher account invented a receipt or reached HTTP: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
		}
	}
}

type watcherReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	} `json:"meta"`
	Data *struct {
		AccountID string `json:"account_id"`
		Results   []struct {
			Issue envelope.IssueRef  `json:"issue"`
			OK    bool               `json:"ok"`
			Data  json.RawMessage    `json:"data"`
			Error *envelope.KeyError `json:"error"`
		} `json:"results"`
	} `json:"data"`
	Errors []envelope.Error `json:"errors"`
}

func TestWatcherPublishedHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	var reads, adds, removes, myself, searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/user/search" {
			searches.Add(1)
			if req.URL.Query().Get("query") != "ada@example.com" {
				t.Error("published watcher query changed its explicit search")
			}
			if req.URL.Query().Get("startAt") == "0" {
				_, _ = w.Write([]byte(`[{"accountId":"5b10a2844c20165700ede21g","active":true}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/myself" {
			myself.Add(1)
			_, _ = w.Write([]byte(`{"accountId":"5b10a2844c20165700ede21g"}`))
			return
		}
		if !strings.HasPrefix(req.URL.Path, "/rest/api/3/issue/KEY-") || !strings.HasSuffix(req.URL.Path, "/watchers") {
			t.Error("published watcher example reached an unrelated target")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.Method {
		case http.MethodGet:
			reads.Add(1)
			_, _ = w.Write([]byte(`{"watchCount":2,"isWatching":true,"watchers":[{"accountId":"5b10a2844c20165700ede21g","displayName":"Fixture Person","active":true,"accountType":"atlassian"},{"accountId":"app-fixture","displayName":"Fixture App","active":true,"accountType":"app"}]}`))
		case http.MethodPost:
			adds.Add(1)
			var account string
			if json.NewDecoder(req.Body).Decode(&account) != nil || account != "5b10a2844c20165700ede21g" {
				t.Error("published watcher add lost its resolved account")
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			removes.Add(1)
			if req.URL.Query().Get("accountId") != "5b10a2844c20165700ede21g" {
				t.Error("published watcher removal lost its resolved account")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("published watcher example changed its method")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	count := 0
	for _, verb := range []string{"list", "add", "remove"} {
		stdout, stderr, exit := runBinary(t, "issue", "watchers", verb, "--help")
		if exit != 0 {
			t.Fatalf("watcher help failed: exit=%d stderr=%q", exit, stderr)
		}
		for line := range strings.SplitSeq(stdout+stderr, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil {
				t.Fatal(err)
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if exit != 0 || stdout == "" || strings.TrimSpace(stdout) == "null" {
				t.Fatalf("published watcher example failed: %q exit=%d stderr=%q", line, exit, stderr)
			}
			count++
		}
	}
	if count != 17 || reads.Load() != 20 || adds.Load() != 9 || removes.Load() != 8 || myself.Load() != 2 || searches.Load() != 4 {
		t.Fatalf("watcher help coverage changed: examples=%d reads=%d adds=%d removes=%d myself=%d searches=%d", count, reads.Load(), adds.Load(), removes.Load(), myself.Load(), searches.Load())
	}
}

func watcherReceiptArgs(verb, mode string, keys ...string) []string {
	args := append([]string{"issue", "watchers", verb}, keys...)
	args = append(args, "--parallelism=1", "--no-input")
	if verb != "list" {
		args = append(args, "--user=account-fixture", "--no-readback")
	}
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func TestWatcherTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add", "remove"} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if _, err := io.Copy(io.Discard, req.Body); err != nil {
						t.Error(err)
					}
					mu.Lock()
					calls = append(calls, req.Method+" "+req.URL.Path)
					mu.Unlock()
					switch req.URL.Path {
					case "/rest/api/3/issue/KEY-1/watchers":
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"errorMessages":["fixture absent \u001b[2Jdisplay\u0007"]}`))
					case "/rest/api/3/issue/KEY-2/watchers":
						w.WriteHeader(http.StatusForbidden)
					default:
						t.Error("watcher command changed its targets")
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				args := watcherReceiptArgs(verb, mode, "KEY-1", "KEY-2")
				extra := editReceiptEnv(server.URL)
				if mode == "agent" {
					extra = append(extra, "AGENT=1")
				}
				if mode == "json" || mode == "compact" {
					args = append(args, "--jq", `error("failed watcher runs must bypass filters")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, extra, args...)
				method := map[string]string{"list": http.MethodGet, "add": http.MethodPost, "remove": http.MethodDelete}[verb]
				mu.Lock()
				got := append([]string(nil), calls...)
				mu.Unlock()
				if exit != cli.ExitAuth || len(got) != 2 || got[0] != method+" /rest/api/3/issue/KEY-1/watchers" || got[1] != method+" /rest/api/3/issue/KEY-2/watchers" {
					t.Fatalf("watcher failure changed severity, targets or replay policy: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if strings.ContainsAny(stderr, "\x1b\a") {
					t.Fatal("watcher diagnostics forwarded terminal controls")
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "fixture absent display") || !strings.Contains(stdout, "KEY-2") || strings.ContainsAny(stdout, "\x1b\a") {
						t.Fatalf("watcher failure discarded sanitized human outcomes: %q", stdout)
					}
					return
				}
				var frame watcherReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.Command != "issue.watchers."+verb || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || len(frame.Data.Results) != 2 {
					t.Fatal("watcher total failure discarded its classified full receipt")
				}
				if verb != "list" && frame.Data.AccountID != "account-fixture" {
					t.Fatal("failed watcher write discarded its authored account")
				}
				for i, code := range []string{"jira_not_found", "jira_forbidden"} {
					row := frame.Data.Results[i]
					if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || len(row.Data) != 0 || row.Error == nil || row.Error.Code != code {
						t.Fatal("watcher total failure changed ordered verdicts or fabricated success")
					}
				}
			})
		}
	}
}

func TestWatcherInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, verb := range []string{"list", "add", "remove"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				secondStarted := make(chan struct{})
				var secondStartedOnce sync.Once
				var mu sync.Mutex
				var calls []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if _, err := io.Copy(io.Discard, req.Body); err != nil {
						t.Error(err)
					}
					mu.Lock()
					calls = append(calls, req.URL.Path)
					mu.Unlock()
					switch req.URL.Path {
					case "/rest/api/3/issue/KEY-1/watchers":
						if verb == "list" {
							_, _ = w.Write([]byte(`{"watchCount":1,"isWatching":true,"watchers":[{"accountId":"account-fixture","displayName":"Fixture Person","active":true,"accountType":"atlassian"}]}`))
						} else {
							w.WriteHeader(http.StatusNoContent)
						}
					case "/rest/api/3/issue/KEY-2/watchers":
						secondStartedOnce.Do(func() { close(secondStarted) })
						<-req.Context().Done()
					default:
						t.Error("canceled watcher batch requested a queued target or replayed a write")
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
					select {
					case <-secondStarted:
					case <-time.After(5 * time.Second):
						t.Fatal("watcher batch did not reach the independent cancellation stimulus")
					}
					if err := process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}, watcherReceiptArgs(verb, mode, "KEY-1", "KEY-2", "KEY-3")...)
				mu.Lock()
				got := append([]string(nil), calls...)
				mu.Unlock()
				if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-1/watchers" || got[1] != "/rest/api/3/issue/KEY-2/watchers" {
					t.Fatalf("interrupted watcher batch lost cancellation or sent queued/replayed work: exit=%d calls=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "KEY-2") || !strings.Contains(stdout, "KEY-3") || (verb == "list" && !strings.Contains(stdout, "Fixture Person")) {
						t.Fatalf("watcher interruption discarded human results: %q", stdout)
					}
					return
				}
				var frame watcherReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || len(frame.Data.Results) != 3 {
					t.Fatal("interrupted watcher batch discarded its joined receipt")
				}
				for i, key := range []string{"KEY-1", "KEY-2", "KEY-3"} {
					row := frame.Data.Results[i]
					if row.Issue.Key != key {
						t.Fatal("watcher interruption reordered outcomes")
					}
					if i == 0 {
						if !row.OK || len(row.Data) == 0 || row.Error != nil {
							t.Fatal("watcher interruption discarded an acknowledged completion")
						}
						if verb == "list" {
							var data envelope.IssueWatchersListData
							if json.Unmarshal(row.Data, &data) != nil || data.WatchCount != jira.NullableOf(int64(1)) || data.IsWatching != jira.NullableOf(true) || len(data.Watchers.Value) != 1 || data.Watchers.Value[0].AccountID != "account-fixture" {
								t.Fatal("watcher interruption discarded completed identity/count/state")
							}
						}
					} else if row.OK || len(row.Data) != 0 || row.Error == nil || row.Error.Code != cli.CodeCanceled {
						t.Fatal("canceled or queued watcher work invented completion")
					}
				}
			})
		}
	}
}

func TestWatcherFailedReadbackReportsFailureWithoutReplayingWrite(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var methods []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					mu.Lock()
					methods = append(methods, req.Method)
					mu.Unlock()
					if req.Method == http.MethodGet {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"errorMessages":["readback denied \u001b[2Jdisplay\u0007"]}`))
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user=account-fixture", "--output="+mode, "--no-input")
				mu.Lock()
				got := append([]string(nil), methods...)
				mu.Unlock()
				method := map[string]string{"add": http.MethodPost, "remove": http.MethodDelete}[verb]
				if exit != cli.ExitOK || len(got) != 2 || got[0] != method || got[1] != http.MethodGet {
					t.Fatalf("failed watcher readback erased acknowledgement or replayed the write: exit=%d methods=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "readback denied display") || strings.ContainsAny(stdout+stderr, "\x1b\a") {
						t.Fatal("failed watcher readback lost its sanitized diagnostic")
					}
					return
				}
				var data struct {
					Results []struct {
						OK   bool `json:"ok"`
						Data struct {
							Readback      *envelope.IssueWatchersListData `json:"readback"`
							ReadbackError *envelope.KeyError              `json:"readback_error"`
						} `json:"data"`
					} `json:"results"`
				}
				if mode == "json" {
					var frame struct {
						Data json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
						t.Fatal(err)
					}
					stdout = string(frame.Data)
				}
				if json.Unmarshal([]byte(stdout), &data) != nil || len(data.Results) != 1 || !data.Results[0].OK || data.Results[0].Data.Readback != nil || data.Results[0].Data.ReadbackError == nil || data.Results[0].Data.ReadbackError.Code != "jira_forbidden" {
					t.Fatal("acknowledged watcher write hid the failed readback or fabricated state")
				}
			})
		}
	}
}
