package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenIssueUsesSelectedSiteWithoutCredentialsOrRequests(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("issue URL construction contacted Jira")
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "selected.toml")
	if err := os.WriteFile(path, []byte("base_url = '"+server.URL+"/jira'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact", "agent"} {
		t.Run(mode, func(t *testing.T) {
			env := []string{"JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_BASE_URL=", "JIRA_READ_ONLY=true", "BROWSER=/does/not/exist"}
			args := []string{"open", "KEY-007", "--config", path, "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("open failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			raw := json.RawMessage(stdout)
			if mode == "json" {
				var frame struct {
					OK   bool            `json:"ok"`
					Data json.RawMessage `json:"data"`
					Meta struct {
						Command string `json:"command"`
					} `json:"meta"`
				}
				if err := json.Unmarshal(raw, &frame); err != nil || !frame.OK || frame.Meta.Command != "open" {
					t.Fatalf("open receipt is not its own successful operation: %v %q", err, stdout)
				}
				raw = frame.Data
			}
			var data struct {
				Issue struct {
					Key string `json:"key"`
				} `json:"issue"`
				WebURL string `json:"web_url"`
				Opened bool   `json:"opened"`
			}
			if err := json.Unmarshal(raw, &data); err != nil || data.Issue.Key != "KEY-7" || data.WebURL != server.URL+"/jira/browse/KEY-7" || data.Opened {
				t.Fatalf("open lost target or launched a machine browser: %v %+v %q", err, data, stdout)
			}
			if strings.Contains(string(raw), "fields") || strings.Contains(string(raw), "details") {
				t.Fatalf("open invented fetched issue data: %q", raw)
			}
		})
	}
}

func TestOpenIssueHumanBrowserFailureStillPrintsURL(t *testing.T) {
	t.Parallel()
	env := []string{"JIRA_BASE_URL=https://example.invalid", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "BROWSER=/does/not/exist"}
	stdout, stderr, exit := runBinaryEnv(t, env, "open", "KEY-1", "--output=human", "--no-input")
	if exit != 0 || !strings.Contains(stdout, "https://example.invalid/browse/KEY-1") || !strings.Contains(stderr, "could not open a browser") {
		t.Fatalf("browser failure lost usable URL or warning: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestOpenIssueRefusalsKeepTypedErrorsAndNoRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{name: "missing", args: []string{"open"}, code: "arg_count_invalid"},
		{name: "many arguments", args: []string{"open", "KEY-1", "KEY-2"}, code: "arg_count_invalid"},
		{name: "many distinct keys", args: []string{"open", "KEY-1:2"}, code: "arg_value_invalid"},
		{name: "bad key", args: []string{"open", "KEY-0"}, code: "arg_value_invalid"},
		{name: "bad flag", args: []string{"open", "KEY-1", "--unknown"}, code: "flag_unknown"},
		{name: "no site", args: []string{"open", "KEY-1"}, code: "config_invalid"},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				env := []string{"JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "BROWSER=/does/not/exist"}
				if tc.name != "no site" {
					env = append(env, "JIRA_BASE_URL=https://example.invalid")
				}
				args := append(append([]string{}, tc.args...), "--output="+mode, "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != 3 {
					t.Fatalf("refusal misclassified: exit=%d out=%q err=%q", exit, stdout, stderr)
				}
				if mode == "human" {
					if stdout != "" || stderr == "" || strings.Contains(stderr, "could not open a browser") {
						t.Fatalf("human refusal lost failure or launched browser: out=%q err=%q", stdout, stderr)
					}
				} else {
					var frame struct {
						OK     bool `json:"ok"`
						Errors []struct {
							Code string `json:"code"`
						} `json:"errors"`
					}
					if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || strings.Contains(stderr, "could not open a browser") {
						t.Fatalf("refusal lost typed frame: out=%q err=%q", stdout, stderr)
					}
				}
			})
		}
	}
}
