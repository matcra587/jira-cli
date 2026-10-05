//go:build linux && nativekeyring

package contract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// TestNativeKeyringPasswordDeadlineProcess supplies an actual command deadline
// to the production composition entry. --timeout bounds HTTP requests only.
func TestNativeKeyringPasswordDeadlineProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_PASSWORD_DEADLINE") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(20)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	os.Exit(root.Run(ctx, os.Args[separator+1:]))
}

// testNativeAccessiblePasswordContext observes an active native masked read.
// It runs only in the private bus, with synthetic credentials and its own PTY.
func testNativeAccessiblePasswordContext(t *testing.T) {
	const email = "password-context@example.invalid"
	const token = "synthetic-password-context-token"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		account, candidate, ok := r.BasicAuth()
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" || !ok || account != email || candidate != token {
			t.Error("password login did not verify the authored tuple")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"password:fixture","active":true}`)
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "password-context.toml")
	if err := os.WriteFile(configPath, []byte("base_url = '"+server.URL+"'\nemail = '"+email+"'\nkeyring = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TERM=dumb", "JIRA_KEYRING=true", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env = append(env, name+"="+os.Getenv(name))
	}
	original := credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "previous@example.invalid", Token: "previous-synthetic-password"}
	seed, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                           string
		partial, deadline, replacement bool
	}{
		{name: "cancel empty"},
		{name: "cancel partial", partial: true},
		{name: "deadline empty", deadline: true},
		{name: "deadline partial", deadline: true, partial: true},
		{name: "replacement cancel empty", replacement: true},
		{name: "replacement cancel partial", replacement: true, partial: true},
		{name: "replacement deadline empty", replacement: true, deadline: true},
		{name: "replacement deadline partial", replacement: true, deadline: true, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ""
			if tc.replacement {
				nativeKeyringTool(t, string(seed), 0, "store", "--label=CLI cancellation fixture", "service", "jira-cli", "username", "default")
				defer nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "default")
				before = nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default")
			}
			step := terminalAuthStep{wait: "API token", passwordReady: true, signal: os.Interrupt}
			args := []string{"--config", configPath, "--output=json"}
			caseEnv := append([]string{}, env...)
			wantExit, wantCode := cli.ExitCanceled, "canceled"
			if tc.partial {
				step.input = token
			}
			if tc.deadline {
				step.signal = nil
				self, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("JIRA_TEST_KEYRING_BINARY", self)
				caseEnv = append(caseEnv, "JIRA_TEST_PASSWORD_DEADLINE=1")
				args = append([]string{"-test.run=^TestNativeKeyringPasswordDeadlineProcess$", "--"}, args...)
				wantExit, wantCode = cli.ExitTimeout, "timeout"
			}
			args = append(args, "auth", "login", "--site", server.URL, "--email", email)
			stdout, screen, exit := nativeKeyringTerminal(t, caseEnv, []terminalAuthStep{
				step,
			}, args...)
			if exit != wantExit {
				t.Fatalf("active accessible password context: exit=%d, want %d", exit, wantExit)
			}
			if strings.Contains(stdout+screen, token) || strings.Contains(stdout+screen, original.Token) || calls.Load() != 0 {
				t.Fatal("active password cancellation exposed a token or contacted Jira")
			}
			var result frame
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.OK || len(result.Errors) != 1 {
				t.Fatal("active password cancellation did not return one machine error")
			}
			var entry struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(result.Errors[0], &entry) != nil || entry.Code != wantCode {
				t.Fatal("active password cancellation returned the wrong error code")
			}
			lookupExit := 1
			if tc.replacement {
				lookupExit = 0
			}
			if value := nativeKeyringTool(t, "", lookupExit, "lookup", "service", "jira-cli", "username", "default"); value != before {
				t.Fatal("active password cancellation changed native credentials")
			}
		})
	}
	for _, replacement := range []bool{false, true} {
		name := "successful first login"
		if replacement {
			name = "successful replacement"
		}
		t.Run(name, func(t *testing.T) {
			if replacement {
				nativeKeyringTool(t, string(seed), 0, "store", "--label=CLI cancellation fixture", "service", "jira-cli", "username", "default")
			}
			defer nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "default")
			before := calls.Load()
			stdout, screen, exit := nativeKeyringTerminal(t, env, []terminalAuthStep{{wait: "API token", passwordReady: true, input: token + "\r"}}, "--config", configPath, "--output=json", "auth", "login", "--site", server.URL, "--email", email)
			if exit != cli.ExitOK || calls.Load() != before+1 || strings.Contains(stdout+screen, token) || strings.Contains(stdout+screen, original.Token) {
				t.Fatal("successful masked login lost its request, output or secrecy contract")
			}
			var result frame
			if json.Unmarshal([]byte(stdout), &result) != nil || !result.OK || len(result.Errors) != 0 {
				t.Fatal("successful masked login did not return a machine success")
			}
			value := nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default")
			var stored credentials.Record
			if json.Unmarshal([]byte(value), &stored) != nil || stored != (credentials.Record{Version: credentials.Version, Site: server.URL, Email: email, Token: token}) {
				t.Fatal("successful masked login stored a different native record")
			}
		})
	}
}
