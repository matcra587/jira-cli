package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAuthStatusRejectsMalformedSuccessfulCurrentUserPayloads(t *testing.T) {
	t.Parallel()
	for _, body := range []struct{ name, value string }{
		{name: "null", value: `null`},
		{name: "empty", value: `{}`},
		{name: "missing identity", value: `{"displayName":"fixture person"}`},
		{name: "null identity", value: `{"accountId":null}`},
		{name: "empty identity", value: `{"accountId":""}`},
		{name: "blank identity", value: `{"accountId":" \t\n"}`},
		{name: "wrong identity type", value: `{"accountId":42}`},
		{name: "array", value: `[]`},
		{name: "trailing object", value: `{"accountId":"fixture"} {}`},
		{name: "trailing garbage", value: `{"accountId":"fixture"} garbage`},
		{name: "truncated", value: `{"accountId":`},
	} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(body.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/myself" {
						t.Errorf("unexpected current-user request: %s %s", req.Method, req.URL.Path)
					}
					_, _ = io.WriteString(w, body.value)
				}))
				t.Cleanup(server.Close)
				env := editReceiptEnv(server.URL)
				args := []string{"auth", "status", "--show-token"}
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != cli.ExitServer {
					t.Fatalf("malformed authentication exit=%d, want %d; stdout=%q stderr=%q", exit, cli.ExitServer, stdout, stderr)
				}
				if calls.Load() != 1 || strings.Contains(stdout+stderr, "example-fixture-token") || strings.Contains(stdout, "Logged in") {
					t.Fatalf("invalid identity was retried, revealed a token or reported authentication: calls=%d stdout=%q stderr=%q", calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					return
				}
				var out struct {
					OK   bool `json:"ok"`
					Meta struct {
						Command  string `json:"command"`
						ExitCode int    `json:"exit_code"`
					} `json:"meta"`
					Errors []envelope.Error `json:"errors"`
				}
				if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatal(err)
				}
				if out.OK || out.Meta.Command != "auth.status" || out.Meta.ExitCode != cli.ExitServer || len(out.Errors) != 1 || out.Errors[0].Code != "current_user_response_invalid" {
					t.Fatalf("malformed identity lost typed failure: %+v", out)
				}
			})
		}
	}
}

func TestCurrentUserInvalidIdentityNeverAuthorizesAccountWrites(t *testing.T) {
	t.Parallel()
	for _, operation := range []struct {
		name string
		args []string
	}{
		{name: "assignee and reporter", args: []string{"issue", "edit", "KEY-123", "--assignee=@me", "--reporter=@me", "--no-input"}},
		{name: "watcher", args: []string{"issue", "watchers", "add", "KEY-123", "--user=@me", "--no-input"}},
		{name: "remote preview", args: []string{"issue", "edit", "KEY-123", "--assignee=@me", "--dry-run", "--validate-remote"}},
	} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(operation.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself" {
						reads.Add(1)
						_, _ = io.WriteString(w, `{"accountId":" \t"}`)
						return
					}
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				env := editReceiptEnv(server.URL)
				args := append([]string(nil), operation.args...)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != cli.ExitServer || reads.Load() != 1 || writes.Load() != 0 {
					t.Fatalf("invalid current account escaped write preflight: exit=%d reads=%d writes=%d stdout=%q stderr=%q", exit, reads.Load(), writes.Load(), stdout, stderr)
				}
				if mode != "human" {
					var out struct {
						OK     bool             `json:"ok"`
						Errors []envelope.Error `json:"errors"`
					}
					if err := json.Unmarshal([]byte(stdout), &out); err != nil {
						t.Fatal(err)
					}
					if out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "current_user_response_invalid" {
						t.Fatalf("account refusal lost shared taxonomy: %+v", out)
					}
				}
			})
		}
	}
}

func TestAuthStatusPrivacyAndCurrentUserRecoveryRecipe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
			t.Errorf("unexpected identity recipe: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"accountId":"opaque:person","emailAddress":null,"future":{"large":9007199254740993}}`)
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "status", "--output=json")
	var status struct {
		OK   bool                      `json:"ok"`
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || stderr != "" || !status.OK || status.Data.AccountID != "opaque:person" || status.Data.DisplayName != "" || status.Data.Token != "" {
		t.Fatalf("privacy-safe account was refused or token exposed: %+v exit=%d stderr=%q", status, exit, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, editReceiptEnv(server.URL), "api", "get", "/rest/api/3/myself", "--output=json")
	var raw struct {
		OK   bool `json:"ok"`
		Data struct {
			Response struct {
				StatusCode int    `json:"status_code"`
				BodyText   string `json:"body_text"`
			} `json:"response"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || stderr != "" || !raw.OK || raw.Data.Response.StatusCode != http.StatusOK || !strings.Contains(raw.Data.Response.BodyText, "9007199254740993") {
		t.Fatalf("published recovery recipe did not retain the native identity: %+v exit=%d stderr=%q", raw, exit, stderr)
	}
}

func TestAuthStatusRejectsUnexpectedSuccessfulCurrentUserStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusCreated, http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = io.WriteString(w, `{"accountId":"fixture"}`)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "status", "--output=json")
			var out struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatal(err)
			}
			if exit != cli.ExitServer || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "current_user_response_invalid" {
				t.Fatalf("unexpected successful status blamed credentials or caller input: status=%d exit=%d stdout=%q stderr=%q", status, exit, stdout, stderr)
			}
		})
	}
}

func TestAuthStatusCurrentUserEOFHonorsConfiguredTimeout(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"accountId":"fixture"}`)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Error(err)
					return
				}
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"auth", "status", "--timeout=500ms"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != cli.ExitTimeout || calls.Load() != 1 || strings.Contains(stdout, "Logged in") {
				t.Fatalf("unfinished identity escaped the selected request deadline: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			if mode != "human" {
				var out struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatal(err)
				}
				if out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "timeout" {
					t.Fatalf("body deadline lost its original class: %+v", out)
				}
			}
		})
	}
}

func TestAuthStatusCurrentUserEOFHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"accountId":"fixture"}`)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Error(err)
					return
				}
				close(started)
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			args := []string{"auth", "status", "--timeout=0"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("current-account response never reached its body")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, args...)
			if exit != cli.ExitCanceled || calls.Load() != 1 || strings.Contains(stdout, "Logged in") {
				t.Fatalf("unfinished identity escaped interruption or was replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			if mode != "human" {
				var out struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatal(err)
				}
				if out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "canceled" {
					t.Fatalf("interrupted body lost its original class: %+v", out)
				}
			}
		})
	}
}
