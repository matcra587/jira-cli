//go:build linux && nativekeyring

package contract

import (
	"bytes"
	"context"
	"encoding/json"
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

// TestNativeKeyringInputDeadlineProcess supplies an actual command deadline;
// the public --timeout flag only bounds HTTP requests.
func TestNativeKeyringInputDeadlineProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_INPUT_DEADLINE") != "1" {
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

// testNativeAccessibleInputContext runs only inside the private bus. It checks
// active native text and confirmation reads before any HTTP or storage mutation.
func testNativeAccessibleInputContext(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "input-context.toml")
	configBytes := []byte("base_url = '" + server.URL + "'\nemail = 'input-context@example.invalid'\nkeyring = true\n")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TERM=dumb", "JIRA_KEYRING=true", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env = append(env, name+"="+os.Getenv(name))
	}
	original := credentials.Record{Version: credentials.Version, Site: "https://example.invalid", Email: "previous@example.invalid", Token: "synthetic-input-context-token"}
	seed, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"site", "email", "logout"} {
		for _, replacement := range []bool{false, true} {
			if field == "logout" && !replacement {
				continue // Logout requires an existing native record.
			}
			for _, deadline := range []bool{false, true} {
				for _, partial := range []bool{false, true} {
					name := field + "/first/cancel/empty"
					if replacement {
						name = strings.ReplaceAll(name, "/first/", "/replacement/")
					}
					if deadline {
						name = strings.ReplaceAll(name, "/cancel/", "/deadline/")
					}
					if partial {
						name = strings.TrimSuffix(name, "/empty") + "/partial"
					}
					t.Run(name, func(t *testing.T) {
						before, lookupExit := "", 1
						if replacement {
							nativeKeyringTool(t, string(seed), 0, "store", "--label=CLI input fixture", "service", "jira-cli", "username", "default")
							defer nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "default")
							before = nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default")
							lookupExit = 0
						}
						step := terminalAuthStep{wait: "Jira site", signal: os.Interrupt}
						command := []string{"auth", "login"}
						switch field {
						case "email":
							step.wait = "Email"
							command = append(command, "--site", server.URL)
						case "logout":
							step.wait = "[y/N]"
							step.beforeInput = []string{original.Email, original.Site, "not revoked"}
							command = []string{"auth", "logout"}
						}
						if partial {
							step.input = "unfinished-input"
							if field == "logout" {
								step.input = "yes" // An unfinished affirmative must not authorize removal.
							}
						}
						args := []string{"--config", configPath, "--output=json"}
						caseEnv := append([]string{}, env...)
						wantExit, wantCode := cli.ExitCanceled, "canceled"
						if deadline {
							step.signal = nil
							self, err := os.Executable()
							if err != nil {
								t.Fatal(err)
							}
							t.Setenv("JIRA_TEST_KEYRING_BINARY", self)
							caseEnv = append(caseEnv, "JIRA_TEST_INPUT_DEADLINE=1")
							args = append([]string{"-test.run=^TestNativeKeyringInputDeadlineProcess$", "--"}, args...)
							wantExit, wantCode = cli.ExitTimeout, "timeout"
						}
						stdout, screen, exit := nativeKeyringTerminal(t, caseEnv, []terminalAuthStep{step}, append(args, command...)...)
						if exit != wantExit || calls.Load() != 0 {
							t.Fatalf("accessible input context: exit=%d, want=%d, HTTP calls=%d", exit, wantExit, calls.Load())
						}
						if strings.Contains(stdout+screen, original.Token) {
							t.Fatal("accessible input exposed the stored synthetic token")
						}
						var result frame
						if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.OK || len(result.Errors) != 1 {
							t.Fatal("accessible input context did not return one machine error")
						}
						var entry struct {
							Code string `json:"code"`
						}
						if json.Unmarshal(result.Errors[0], &entry) != nil || entry.Code != wantCode {
							t.Fatal("accessible input context returned the wrong error code")
						}
						if got := nativeKeyringTool(t, "", lookupExit, "lookup", "service", "jira-cli", "username", "default"); got != before {
							t.Fatal("interrupted accessible input changed native credentials")
						}
						data, readErr := os.ReadFile(configPath)
						info, statErr := os.Stat(configPath)
						if readErr != nil || statErr != nil || !bytes.Equal(data, configBytes) || info.Mode().Perm() != 0o600 {
							t.Fatal("interrupted accessible input changed the synthetic configuration")
						}
					})
				}
			}
		}
	}
}
