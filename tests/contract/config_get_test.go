package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigGetReadsOnlyStoredValuesAndDistinguishesAbsence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "stored.toml")
	const original = "# keep authored bytes\nread_only=false\ntimeout='0s'\nemail=''\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, value string
		present    bool
	}{{"read_only", "false", true}, {"timeout", "0s", true}, {"email", "", true}, {"keyring", "", false}, {"base_url", "", false}} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(tc.key+"/"+mode, func(t *testing.T) {
				stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_READ_ONLY=true", "JIRA_TIMEOUT=19s", "JIRA_EMAIL=env@example.invalid", "JIRA_BASE_URL=https://env.example.invalid", "JIRA_KEYRING=false"}, "config", "get", tc.key, "--config", path, "--timeout=21s", "--output="+mode)
				result := decodeConfigGet(t, mode, stdout)
				if exit != 0 || result.Key != tc.key || result.Value != tc.value || result.Present != tc.present || result.Secret || !result.FileExists || result.Path != path {
					t.Fatalf("stored setting was replaced by effective values: exit=%d out=%q stderr=%q", exit, stdout, stderr)
				}
			})
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != original {
		t.Fatalf("config get changed the file: %q %v", after, err)
	}
}

func TestConfigGetRefusesMissingExplicitFileAndUnknownKeyBeforeIO(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.toml")
	for _, tc := range []struct {
		key, code string
	}{{"read_only", "local_io_failed"}, {"read_ony", "arg_value_invalid"}} {
		stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "get", tc.key, "--config", path, "--output=json")
		var result apiFrame
		if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != tc.code {
			t.Fatalf("config get refusal lost precedence: key=%s exit=%d out=%q", tc.key, exit, stdout)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config get created its target: %v", err)
	}
}

func TestConfigGetRedactsSecretsAndKeepsHumanControlsOut(t *testing.T) {
	t.Parallel()
	name := "stored-\x1b[31m.toml"
	if xos.IsWindows() {
		// Windows cannot represent control characters in a filename. Retain
		// literal Unicode paths and the stored-value control checks below.
		name = "stored-古い.toml"
	}
	path := filepath.Join(t.TempDir(), name)
	const token = "synthetic-get-token"
	const siteSecret = "synthetic-get-password"
	const file = "api_token='" + token + "'\nbase_url='https://user:" + siteSecret + "@example.invalid'\nemail=\"saved\\u001b[31m@example.invalid\"\n"
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"api_token", "base_url", "email"} {
		for _, mode := range []string{"json", "compact", "human"} {
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_THEME=dark"}, "config", "get", key, "--config", path, "--output="+mode)
			if exit != 0 || strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, siteSecret) {
				t.Fatalf("stored secret escaped redaction: key=%s mode=%s exit=%d out=%q stderr=%q", key, mode, exit, stdout, stderr)
			}
			if mode == "human" && strings.ContainsAny(stdout, "\x1b\r") {
				t.Fatalf("human setting/path retained controls: %q", stdout)
			}
			if mode != "human" {
				result := decodeConfigGet(t, mode, stdout)
				if !result.Present || result.Secret != (key != "email") || result.Path != path {
					t.Fatalf("stored secrecy/presence/path lost: %q", stdout)
				}
				if key == "email" && result.Value != "saved\x1b[31m@example.invalid" {
					t.Fatalf("machine setting changed literal bytes: %q", result.Value)
				}
			}
		}
	}
}

func decodeConfigGet(t *testing.T, mode, stdout string) envelope.ConfigGetOutput {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &fields); err != nil {
		t.Fatalf("invalid setting output: %v", err)
	}
	if mode != "compact" {
		if err := json.Unmarshal(fields["data"], &fields); err != nil {
			t.Fatalf("invalid setting data: %v", err)
		}
	}
	for _, key := range []string{"path", "file_exists", "key", "value", "present", "secret"} {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			t.Fatalf("setting output omitted required %s: %q", key, stdout)
		}
	}
	if mode == "compact" {
		var result envelope.ConfigGetOutput
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("invalid compact setting result: %v", err)
		}
		return result
	}
	var result struct {
		OK   bool                     `json:"ok"`
		Data envelope.ConfigGetOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || !result.OK {
		t.Fatalf("invalid JSON setting envelope: %v, %q", err, stdout)
	}
	return result.Data
}

func TestConfigGetMalformedSecretNeverLeaksThroughFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "invalid.toml")
	const secret = "synthetic-malformed-get-secret"
	if err := os.WriteFile(path, []byte("api_token=['"+secret+"']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_THEME=dark"}, "config", "get", "api_token", "--config", path, "--output=json")
	var result apiFrame
	if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "config_invalid" {
		t.Fatalf("malformed secret was not refused: exit=%d out=%q", exit, stdout)
	}
	if strings.Contains(stdout+stderr, secret) {
		t.Fatal("malformed secret leaked through the configuration failure")
	}
}

func TestConfigGetMalformedSiteURLNeverLeaks(t *testing.T) {
	t.Parallel()
	const secret = "synthetic-malformed-site-password"
	for _, site := range []string{
		"https://user:" + secret + "@example.invalid/%zz",
		"https://user:" + secret + "@example.invalid:bad",
		"https://user:" + secret + "@example.invalid/\npath",
	} {
		path := filepath.Join(t.TempDir(), "invalid-site.toml")
		if err := os.WriteFile(path, []byte("base_url="+strconv.Quote(site)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"json", "compact", "human"} {
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "get", "base_url", "--config", path, "--output="+mode)
			if exit != 0 || strings.Contains(stdout+stderr, secret) {
				t.Fatalf("malformed site credentials escaped inspection: mode=%s exit=%d out=%q stderr=%q", mode, exit, stdout, stderr)
			}
			if mode != "human" {
				result := decodeConfigGet(t, mode, stdout)
				if !result.Present || !result.Secret || result.Value != "(set)" {
					t.Fatalf("malformed URL was not withheld with a fixed marker: %q", stdout)
				}
			}
		}
	}
}

func TestConfigGetAbsentDefaultNeverInventsEnvironmentOrBuiltins(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_WORKDAY_SECONDS=42", "JIRA_EMAIL=env@example.invalid"}, "config", "get", "workday_seconds", "--output=json")
	result := decodeConfigGet(t, "json", stdout)
	if exit != 0 || result.FileExists || result.Present || result.Value != "" || result.Path == "" {
		t.Fatalf("absent default was replaced with another layer: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestConfigGetDiscoveryCompletionAndJQExposeStoredContract(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("read_only=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, "config", "get", "read_only", "--config", path, "--jq=.data.value")
	if exit != 0 || strings.TrimSpace(stdout) != "false" {
		t.Fatalf("jq did not select the stored scalar: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinary(t, "agent", "schema", "--path=config get", "--output=json")
	for _, required := range []string{`"config.get"`, `"present"`, `"secret"`, `"file_exists"`, `"configkey"`, `"KEY"`} {
		if exit != 0 || !strings.Contains(stdout, required) {
			t.Fatalf("stored get discovery omitted %s: exit=%d out=%q stderr=%q", required, exit, stdout, stderr)
		}
	}
	stdout, stderr, exit = runBinary(t, "--@complete=configkey", "--", "config", "get")
	if exit != 0 {
		t.Fatalf("setting completion failed: exit=%d stderr=%q", exit, stderr)
	}
	want := []string{"base_url", "cloud_id", "email", "credential_id", "account_id", "account_id_site", "account_id_email", "account_id_cloud_id", "api_token", "api_token_env", "default_project", "default_issue_type", "default_board", "refresh_interval", "team_account_ids", "keyring", "secret_backend", "onepassword_account", "onepassword_vault", "onepassword_item", "read_only", "timeout", "theme", "theme_path", "glyphs", "workday_seconds", "queries_path", "editor", "aliases", "tui_preview", "tui_status", "tui_assignee", "tui_labels", "tui_priority", "tui_issue_type", "tui_compact", "tui_updated", "tui_auto_page", "tui_preview_dock", "tui_preview_size"}
	if got := strings.Fields(stdout); len(got) != len(want) {
		t.Fatalf("setting completion changed its supported vocabulary: %q", stdout)
	}
	for _, key := range want {
		if !strings.Contains("\n"+stdout, "\n"+key+"\n") {
			t.Fatalf("completion omitted %s: %q", key, stdout)
		}
	}
}
