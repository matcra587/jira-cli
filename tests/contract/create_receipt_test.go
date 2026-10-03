package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type createReceiptFrame struct {
	OK   bool `json:"ok"`
	Data *struct {
		Applied   bool               `json:"applied"`
		Attempted bool               `json:"attempted"`
		Payload   json.RawMessage    `json:"payload"`
		Issue     *envelope.IssueRef `json:"issue"`
		Receipt   json.RawMessage    `json:"receipt"`
	} `json:"data"`
	Errors []envelope.Error `json:"errors"`
}

func TestIssueCreateInterruptRetainsUnacknowledgedAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var once sync.Once
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				var body map[string]json.RawMessage
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" || json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Error("creation changed its native dispatch")
				}
				once.Do(func() { close(started) })
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=authored", "--no-input"}
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("interrupted creation bypasses jq")`)
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("creation did not reach independent interruption stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, args...)
			if exit != cli.ExitCanceled || calls.Load() != 1 {
				t.Fatalf("interrupted creation changed exit or replayed: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "Creation was attempted") || strings.Contains(stdout, "Jira acknowledged") || strings.Contains(stdout, "Created ") {
					t.Fatal("human interruption dropped or invented creation facts")
				}
				return
			}
			var frame createReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || !frame.Data.Attempted || frame.Data.Applied || frame.Data.Issue != nil || len(frame.Data.Receipt) != 0 || len(frame.Data.Payload) == 0 || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled {
				t.Fatal("interrupted creation discarded its attempted payload or fabricated acknowledgement")
			}
		})
	}
}

func TestIssueCreationBrokenPipeUsesOutputExitWithoutReplay(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	t.Parallel()
	for _, verb := range []string{"create", "clone"} {
		for _, body := range []string{`{"id":"9001","key":"NEW-1"}`, `{}`} {
			for _, mode := range []string{"json", "compact", "human", "agent"} {
				t.Run(verb+"/"+strconv.Itoa(len(body))+"/"+mode, func(t *testing.T) {
					t.Parallel()
					var posts atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						if req.Method == http.MethodGet && verb == "clone" {
							_, _ = w.Write([]byte(`{"key":"KEY-1","fields":{"summary":"authored","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`))
							return
						}
						if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
							t.Error("creation changed its native request")
						}
						posts.Add(1)
						w.WriteHeader(http.StatusCreated)
						_, _ = w.Write([]byte(body))
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
					args := []string{"issue", verb, "--no-input"}
					if verb == "create" {
						args = append(args, "--project=KEY", "--type=Task", "--summary=authored")
					} else {
						args = append(args, "KEY-1", "--parallelism=1")
					}
					env := editReceiptEnv(server.URL)
					if mode == "agent" {
						env = append(env, "AGENT=1")
					} else {
						args = append(args, "--output="+mode)
					}
					dir := t.TempDir()
					cmd := exec.CommandContext(t.Context(), jiraBinary(t), args...)
					cmd.Dir = dir
					cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb"}, env...)
					var stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = writer, &stderr
					err = cmd.Run()
					failure, ok := errors.AsType[*exec.ExitError](err)
					if !ok || failure.ExitCode() != cli.ExitOutput || posts.Load() != 1 {
						t.Fatalf("creation pipe bypassed exit8 or replayed: err=%v posts=%d stderr=%q", err, posts.Load(), stderr.String())
					}
				})
			}
		}
	}
}

func TestIssueCreatePreservesNativeFollowupOutcomes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		exit       int
		code       string
	}{
		{name: "future response", body: `{"id":"9001","key":"NEW-1","future":{"zero":0,"nullable":null,"markup":"<a>&"}}`},
		{name: "transition failed", body: `{"id":"9001","key":"NEW-1","transition":{"status":400,"errorCollection":{"errors":{"transition":"fixture rejected"}}}}`, exit: cli.ExitServer, code: "issue_create_followup_failed"},
		{name: "watchers failed", body: `{"id":"9001","key":"NEW-1","watchers":{"status":403,"errorCollection":{"errorMessages":["fixture watcher rejected"]}}}`, exit: cli.ExitServer, code: "issue_create_followup_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "create", "--project=KEY", "--type=Task", "--summary=authored", "--output=json", "--no-input")
			var frame createReceiptFrame
			if exit != tc.exit || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK != (tc.exit == 0) || frame.Data == nil || !frame.Data.Applied || frame.Data.Issue == nil || frame.Data.Issue.Key != "NEW-1" {
				t.Fatal("creation lost its usable identity or reported an observed failed followup as success")
			}
			var want, got any
			if json.Unmarshal([]byte(tc.body), &want) != nil || json.Unmarshal(frame.Data.Receipt, &got) != nil || !reflect.DeepEqual(want, got) {
				t.Fatal("creation dropped native followup or future response data")
			}
			if tc.code != "" && (len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code) {
				t.Fatal("creation classified a failed followup as a failed issue creation")
			}
		})
	}
}

func TestIssueCreateInvalidReceiptRetainsAttemptAndAcknowledgement(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null":              `null`,
		"empty object":      `{}`,
		"missing id":        `{"key":"NEW-1"}`,
		"missing key":       `{"id":"9001"}`,
		"blank key":         `{"id":"9001","key":" \n"}`,
		"malformed":         `{"id":`,
		"trailing document": `{"id":"9001","key":"NEW-1"} {}`,
	} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue" {
						t.Error("creation changed its native request or read back after an invalid receipt")
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=authored", "--verify", "--no-input"}
				env := editReceiptEnv(server.URL)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				if mode == "json" || mode == "compact" {
					args = append(args, "--jq", `error("failed creation bypasses jq")`)
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != cli.ExitServer || calls.Load() != 1 || strings.ContainsAny(stderr, "\x1b\a") {
					t.Fatalf("invalid creation receipt changed exit, replay or verification: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "Jira acknowledged creation") || strings.Contains(stdout, "Created NEW-1") || !strings.Contains(stderr, "Inspect") {
						t.Fatal("human creation output discarded its acknowledgement or invented a usable identity")
					}
					return
				}
				var frame createReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || !frame.Data.Attempted || !frame.Data.Applied || frame.Data.Issue != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_create_response_invalid" {
					t.Fatal("creation discarded its attempted request or native201 acknowledgement")
				}
				var payload struct {
					Fields struct {
						Summary string `json:"summary"`
					} `json:"fields"`
				}
				if json.Unmarshal(frame.Data.Payload, &payload) != nil || payload.Fields.Summary != "authored" {
					t.Fatal("creation changed its retained authored payload")
				}
			})
		}
	}
}

func TestIssueCreateUnexpectedSuccessRemainsUncertain(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 202, 204} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"id":"9001","key":"NEW-1"}`))
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "create", "--project=KEY", "--type=Task", "--summary=authored", "--output=json", "--no-input")
			var frame createReceiptFrame
			if exit != cli.ExitServer || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data == nil || !frame.Data.Attempted || frame.Data.Applied || frame.Data.Issue != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_create_response_invalid" {
				t.Fatal("creation discarded an uncertain attempt or claimed an undocumented acknowledgement")
			}
		})
	}
}
