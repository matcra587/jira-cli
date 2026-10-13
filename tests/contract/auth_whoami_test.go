package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAuthWhoamiRetainsNativeIdentity(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const body = `{"accountId":"actual:person","accountType":"atlassian","displayName":"Native <person>&","emailAddress":null,"timeZone":"America/Toronto","active":false,"future":{"large":9007199254740993,"null":null}}`
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(server.Close)
			args := []string{"auth", "whoami", "--no-input"}
			env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			format := mode
			if mode == "agent" {
				format = "compact"
			}
			var out struct {
				BaseURL string          `json:"base_url"`
				User    json.RawMessage `json:"user"`
			}
			if exit != 0 || stderr != "" || calls.Load() != 1 {
				t.Fatalf("identity result: exit=%d stderr=%q calls=%d stdout=%q", exit, stderr, calls.Load(), stdout)
			}
			if err := json.Unmarshal(adfData(t, stdout, format, "auth.whoami", true), &out); err != nil {
				t.Fatal(err)
			}
			if out.BaseURL != server.URL {
				t.Fatalf("wrong identity site: %+v", out)
			}
			decode := func(b []byte) any {
				d := json.NewDecoder(bytes.NewReader(b))
				d.UseNumber()
				var v any
				if err := d.Decode(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			if !reflect.DeepEqual(decode(out.User), decode([]byte(body))) {
				t.Fatalf("native identity changed: %s", out.User)
			}
			if strings.Contains(stdout+stderr, "example-fixture-token") || strings.Contains(string(out.User), "fixture@example") {
				t.Fatalf("configured credential replaced native identity or leaked")
			}
		})
	}
}

func TestAuthWhoamiRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	for _, body := range []struct{ name, value string }{
		{name: "null", value: `null`}, {name: "missing", value: `{}`}, {name: "null_id", value: `{"accountId":null}`}, {name: "blank_id", value: `{"accountId":" \t"}`}, {name: "wrong_type", value: `{"accountId":42}`}, {name: "trailing", value: `{"accountId":"fixture"} {}`}, {name: "truncated", value: `{"accountId":`},
	} {
		t.Run(body.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, body.value) }))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "whoami", "--output=json")
			var out struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatal(err)
			}
			if exit != cli.ExitServer || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "current_user_response_invalid" || calls.Load() != 1 || strings.Contains(stdout+stderr, "example-fixture-token") {
				t.Fatalf("invalid identity: %+v exit=%d calls=%d stderr=%q", out, exit, calls.Load(), stderr)
			}
		})
	}
}

func TestAuthWhoamiRejectsArgumentsBeforeIO(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"extra"}, {"--save=invalid"}, {"--show-token"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"accountId":"fixture"}`)
			}))
			t.Cleanup(server.Close)
			argv := append([]string{"auth", "whoami", "--output=json"}, args...)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), argv...)
			if exit != cli.ExitValidation || calls.Load() != 0 || strings.Contains(stdout+stderr, "example-fixture-token") {
				t.Fatalf("invalid arguments made a request: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestAuthWhoamiHumanUsesNativeIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"accountId":"actual:person","displayName":"Native\u001b[2J <person>","emailAddress":"native@example.invalid","accountType":"atlassian","timeZone":"UTC","active":false}`)
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "whoami", "--output=human", "--color=never")
	for _, want := range []string{"actual:person", "native@example.invalid", "atlassian", "UTC", "false"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q: %q", want, stdout)
		}
	}
	if exit != 0 || stderr != "" || strings.Contains(stdout, "\x1b") || strings.Contains(stdout, "example-fixture-token") {
		t.Fatalf("unsafe identity: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestAuthWhoamiDiscoveryAndExamples(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "schema", "--path=auth whoami", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("identity schema: %d %q", exit, stderr)
	}
	tree, _ := readSchema(t, stdout, "json")
	if tree.Path != "jira auth whoami" || tree.ReadOnly || tree.OutputSchema == nil {
		t.Fatalf("mixed identity command has incorrect discovery: %+v", tree)
	}
	properties := tree.OutputSchema["properties"].(map[string]any)
	user := properties["user"].(map[string]any)
	for _, field := range []string{"accountId", "accountType", "displayName", "emailAddress", "timeZone", "active"} {
		if user["properties"].(map[string]any)[field] == nil {
			t.Errorf("native schema omits %s", field)
		}
	}
	examples, ok := tree.Extensions["examples"].(string)
	if !ok {
		t.Fatal("identity examples missing")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
			t.Errorf("example changed request")
		}
		_, _ = io.WriteString(w, `{"accountId":"actual:person"}`)
	}))
	t.Cleanup(server.Close)
	for _, line := range strings.Split(examples, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		argv, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit = runBinaryEnv(t, editReceiptEnv(server.URL), argv...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "actual:person") {
			t.Fatalf("published example %q: exit=%d stdout=%q stderr=%q", line, exit, stdout, stderr)
		}
	}
}

func TestAuthWhoamiCurrentUserEOFHonorsTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"accountId":"actual:person"}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_TIMEOUT=140ms"), "auth", "whoami", "--output=json")
	var out struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitTimeout || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "timeout" {
		t.Fatalf("identity EOF timeout: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestAuthWhoamiCurrentUserEOFHonorsCancellation(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() {
		t.Skip("Windows lacks Unix interrupt delivery")
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"accountId":"actual:person"}`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnvStarted(t, append(editReceiptEnv(server.URL), "JIRA_TIMEOUT=0s"), func(process *os.Process) {
		go func() {
			select {
			case <-started:
				if err := process.Signal(os.Interrupt); err != nil {
					t.Errorf("interrupt: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("identity request did not start")
			}
		}()
	}, "auth", "whoami", "--output=json")
	var out struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitCanceled || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "canceled" {
		t.Fatalf("identity cancellation: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestMeSharesNativeIdentityRead(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "agent", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
					t.Errorf("unexpected identity request")
				}
				_, _ = io.WriteString(w, `{"accountId":"actual:person","emailAddress":null,"accountType":"atlassian","timeZone":"UTC","active":false,"futureCamelField":"native"}`)
			}))
			t.Cleanup(server.Close)
			argv := []string{"me", "--no-input"}
			env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				argv = append(argv, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, argv...)
			if exit != 0 || stderr != "" || calls.Load() != 1 || strings.Contains(stdout+stderr, "example-fixture-token") {
				t.Fatalf("me identity read: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "actual:person") || strings.Contains(stdout, "native-id@example") {
					t.Fatal("human identity replaced native fields")
				}
				return
			}
			format := mode
			if mode == "agent" {
				format = "compact"
			}
			var out envelope.AuthWhoamiOutput
			if err := json.Unmarshal(adfData(t, stdout, format, "me", true), &out); err != nil {
				t.Fatal(err)
			}
			if out.User.AccountID.Value != "actual:person" || !out.User.EmailAddress.Set || !out.User.EmailAddress.Null || out.User.AccountType.Value != "atlassian" || out.User.Active.Value || string(out.User.Extra["futureCamelField"]) != `"native"` {
				t.Fatalf("me native identity changed: %+v", out)
			}
		})
	}
}

func TestIdentityCommandsCompleteWithoutCredentials(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, "completion", "bash")
	if exit != 0 || stderr != "" || stdout == "" {
		t.Fatalf("identity completion generation: exit=%d stderr=%q", exit, stderr)
	}
	t.Run("native_bash", func(t *testing.T) {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("Bash is unavailable for native completion execution")
		}
		path := filepath.Join(t.TempDir(), "jira.bash")
		if err := os.WriteFile(path, []byte(stdout), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ name, words, cword, current, previous, want string }{
			{name: "whoami", words: "jira auth wh", cword: "2", current: "wh", previous: "auth", want: "whoami"}, {name: "me", words: "jira m", cword: "1", current: "m", previous: "jira", want: "me"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), bash, "--noprofile", "--norc", "-c", `source "$1"
read -r -a COMP_WORDS <<< "$2"
COMP_CWORD="$3"
_jira jira "$4" "$5"
printf '%s\n' "${COMPREPLY[@]}"`, "completion", path, tc.words, tc.cword, tc.current, tc.previous)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
				out, err := cmd.CombinedOutput()
				if err != nil || !slices.Contains(strings.Fields(string(out)), tc.want) {
					t.Fatalf("native identity completion: %q %v", out, err)
				}
			})
		}
	})
}
