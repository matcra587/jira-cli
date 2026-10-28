package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func decodeConfigSet(t *testing.T, mode, stdout string) envelope.ConfigSetOutput {
	t.Helper()
	var data []byte
	if mode == "compact" {
		data = []byte(stdout)
	} else {
		var frame struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(stdout), &frame); err != nil || !frame.OK {
			t.Fatalf("invalid setting mutation envelope: %v %q", err, stdout)
		}
		data = frame.Data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"dry_run", "path", "key", "value", "previous_value", "previous_present", "previous_secret", "would_change", "changed", "created"} {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			t.Fatalf("setting mutation omitted required %s: %q", key, stdout)
		}
	}
	var result envelope.ConfigSetOutput
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConfigSetPreviewThenCreateDistinguishesAbsentFalseWithoutEffectiveLayers(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "missing-directory", "settings.toml")
			env := []string{"JIRA_KEYRING=false", "JIRA_READ_ONLY=true", "JIRA_EMAIL=env@example.invalid", "JIRA_TIMEOUT=19s"}
			args := []string{"config", "set", "read_only", "false", "--config", path, "--output=" + mode, "--no-input"}
			preview, stderr, exit := runBinaryEnv(t, env, append(args, "--dry-run")...)
			if exit != 0 || stderr != "" {
				t.Fatalf("preview failed: exit=%d out=%q stderr=%q", exit, preview, stderr)
			}
			if mode == "human" {
				if !strings.Contains(preview, "would store") || !strings.Contains(preview, "not stored") {
					t.Fatalf("human preview fabricated publication or previous data: %q", preview)
				}
			} else {
				out := decodeConfigSet(t, mode, preview)
				if !out.DryRun || !out.WouldChange || out.Changed || out.Created || out.PreviousPresent || out.PreviousSecret || out.PreviousValue != "" || out.Value != "false" || out.Key != "read_only" || out.Path != path {
					t.Fatalf("preview lost file-only declaration intent: %+v", out)
				}
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("preview created a configuration directory")
			}
			stored, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("first scalar publication failed: exit=%d out=%q stderr=%q", exit, stored, stderr)
			}
			if mode != "human" {
				out := decodeConfigSet(t, mode, stored)
				if out.DryRun || !out.WouldChange || !out.Changed || !out.Created || out.PreviousPresent || out.Value != "false" {
					t.Fatalf("first declaration receipt was not truthful: %+v", out)
				}
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != "read_only = false\n" {
				t.Fatalf("save added effective values or lost the TOML boolean: %q %v", contents, err)
			}
		})
	}
}

func TestConfigSetNoopPreservesAuthoredBytesAndReportsStoredPreviousValue(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		const original = "# preserve comments and lexical duration\ntimeout='120s'\nemail=''\nread_only=false\n"
		if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, preview := range []bool{true, false} {
			args := []string{"config", "set", "timeout", "2m", "--config", path, "--output=" + mode, "--timeout=3s", "--no-input"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_TIMEOUT=1s"}, args...)
			out := decodeConfigSet(t, mode, stdout)
			if exit != 0 || stderr != "" || out.DryRun != preview || out.WouldChange || out.Changed || out.Created || !out.PreviousPresent || out.PreviousSecret || out.PreviousValue != "2m0s" || out.Value != "2m0s" {
				t.Fatalf("no-op confused stored/effective values or publication: exit=%d out=%q stderr=%q", exit, stdout, stderr)
			}
		}
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != original {
			t.Fatal("no-op rewrote authored bytes")
		}
		after, err := os.Stat(path)
		if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
			t.Fatal("no-op altered permissions or modification time")
		}
	}
}

func TestConfigSetRefusesBadInputBeforeFileAccessAndNeverPrintsCredentials(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "invalid.toml")
	const original = "email=[\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	const siteSecret = "synthetic-refused-site"
	const token = "synthetic-refused-token"
	for _, setting := range []struct{ key, value, field string }{
		{"api_token", token, "KEY"},
		{"aliases", "{}", "KEY"},
		{"read_ony", "true", "KEY"},
		{"read_only", "yes", "VALUE"},
		{"workday_seconds", "1.5", "VALUE"},
		{"workday_seconds", "0", "VALUE"},
		{"timeout", "-1s", "VALUE"},
		{"theme", "bogus", "VALUE"},
		{"glyphs", "bogus", "VALUE"},
		{"base_url", "https://user:" + siteSecret + "@example.invalid/%zz", "VALUE"},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			for _, preview := range []bool{true, false} {
				args := []string{"config", "set", "--config", path, "--output=" + mode, "--no-input"}
				if preview {
					args = append(args, "--dry-run")
				}
				args = append(args, "--", setting.key, setting.value)
				stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
				if exit != 3 || strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, siteSecret) {
					t.Fatalf("invalid assignment leaked credentials or escaped refusal: key=%s mode=%s exit=%d out=%q stderr=%q", setting.key, mode, exit, stdout, stderr)
				}
				if mode != "human" {
					var result apiFrame
					if json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "arg_value_invalid" || result.Errors[0].Field != setting.field {
						t.Fatalf("input refusal lost field or preceded validation with I/O: %q", stdout)
					}
				}
			}
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatal("input refusal changed the selected file")
	}
}

func TestConfigSetRepairsSiteWithoutLeakingPreviousValueOrHumanControls(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		path := filepath.Join(t.TempDir(), "settings-\x1b[31m.toml")
		const siteSecret = "synthetic-repair-site"
		const original = "base_url='https://user:" + siteSecret + "@example.invalid/%zz'\nemail='keep@example.invalid'\n"
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, preview := range []bool{true, false} {
			args := []string{"config", "set", "base_url", "https://fixed.example.invalid", "--config", path, "--output=" + mode, "--no-input"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			if exit != 0 || strings.Contains(stdout+stderr, siteSecret) || mode == "human" && strings.ContainsAny(stdout, "\x1b\r") {
				t.Fatalf("site repair failed or leaked unsafe text: exit=%d out=%q stderr=%q", exit, stdout, stderr)
			}
			if mode != "human" {
				out := decodeConfigSet(t, mode, stdout)
				if out.DryRun != preview || out.Changed == preview || out.Created || !out.WouldChange || !out.PreviousPresent || !out.PreviousSecret || out.PreviousValue != "(set)" || out.Value != "https://fixed.example.invalid" || out.Path != path {
					t.Fatalf("repair receipt lost redacted original or intent: %+v", out)
				}
			}
			if preview {
				contents, err := os.ReadFile(path)
				if err != nil || string(contents) != original {
					t.Fatal("site repair preview changed its file")
				}
			}
		}
		stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "get", "email", "--config", path, "--output=json")
		if out := decodeConfigGet(t, "json", stdout); exit != 0 || !out.Present || out.Value != "keep@example.invalid" {
			t.Fatal("site repair dropped another declared setting")
		}
	}
}

func TestConfigSetSchemaJQAndCompletionExposeValidatedScalarContract(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "set", "workday_seconds", "36000", "--config", path, "--dry-run", "--jq=.data.value")
	if exit != 0 || strings.TrimSpace(stdout) != "36000" || stderr != "" {
		t.Fatalf("jq did not expose the validated preview value: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinary(t, "agent", "schema", "--path=config set", "--output=json")
	for _, required := range []string{`"config.set"`, `"previous_present"`, `"previous_secret"`, `"would_change"`, `"configsetkey"`, `"word_configvalue"`, `"VALUE"`} {
		if exit != 0 || !strings.Contains(stdout, required) {
			t.Fatalf("setting write discovery omitted %s: exit=%d out=%q stderr=%q", required, exit, stdout, stderr)
		}
	}
	stdout, stderr, exit = runBinary(t, "--@complete=configsetkey", "--")
	if exit != 0 || stderr != "" || len(strings.Fields(stdout)) != 35 || slices.Contains(strings.Fields(stdout), "api_token") || strings.Contains(stdout, "aliases") || !strings.Contains(stdout, "queries_path\n") || !strings.Contains(stdout, "onepassword_item\n") {
		t.Fatalf("write completion offered credentials, aliases or an incomplete inventory: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	for _, key := range []string{"credential_id", "account_id", "account_id_site", "account_id_email", "account_id_cloud_id", "tui_preview", "tui_status", "tui_assignee", "tui_labels", "tui_priority", "tui_issue_type", "tui_compact", "tui_updated"} {
		if !slices.Contains(strings.Fields(stdout), key) {
			t.Fatal("write completion omitted cached identity metadata", key)
		}
	}
	for _, tc := range []struct{ key, values string }{
		{"read_only", "true\nfalse\n"},
		{"tui_preview", "true\nfalse\n"},
		{"glyphs", "default\nnerd\n"},
		{"timeout", ""},
		{"api_token", ""},
		{"unknown", ""},
		{"secret_backend", "keyring\nenv\n1password\n"},
	} {
		stdout, stderr, exit = runBinary(t, "--@complete=configvalue", "--", tc.key)
		if exit != 0 || stdout != tc.values || stderr != "" {
			t.Fatalf("value completion ignored the selected key: key=%s exit=%d out=%q stderr=%q", tc.key, exit, stdout, stderr)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("schema, completion or jq preview created local state")
	}
}

func TestConfigSetGeneratedBashCompletionExecutesValuePredictor(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable for executing the generated completion script")
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("generating setting completion required credentials")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "jira.bash")
	if err := os.WriteFile(script, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(dir, "absent", "settings.toml")
	cmd := exec.CommandContext(t.Context(), bash, "--noprofile", "--norc", "-c", `binary="$2"
jira() { "$binary" "$@"; }
source "$1"
compopt() { :; }
COMP_WORDS=(jira --config "$3" config set -- read_only "")
COMP_CWORD=7
_jira jira "" read_only
printf '%s\n' "${COMPREPLY[@]}"`, "completion", script, jiraBinary(t), selected)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "JIRA_KEYRING=false", "NO_COLOR=1", "TERM=dumb"}
	result, err := cmd.CombinedOutput()
	if err != nil || string(result) != "true\nfalse\n" {
		t.Fatalf("generated setting completion passed wrong positional context: %q %v", result, err)
	}
	if _, err := os.Stat(filepath.Dir(selected)); !os.IsNotExist(err) {
		t.Fatal("generated value completion created the selected directory")
	}
}

func TestConfigSetPreservesMachineTextAndSanitizesHumanValue(t *testing.T) {
	t.Parallel()
	const value = "saved\x1b[31m<&>\u2028@example.invalid"
	for _, mode := range []string{"json", "compact", "human"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "set", "email", value, "--config", path, "--output="+mode, "--no-input")
		if exit != 0 || stderr != "" {
			t.Fatalf("literal scalar publication failed: exit=%d out=%q stderr=%q", exit, stdout, stderr)
		}
		if mode == "human" {
			if strings.ContainsAny(stdout, "\x1b\r") {
				t.Fatalf("human value retained terminal controls: %q", stdout)
			}
		} else if out := decodeConfigSet(t, mode, stdout); out.Value != value || !out.Changed || !out.Created {
			t.Fatalf("machine value changed literal text: %+v", out)
		}
		stdout, _, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "get", "email", "--config", path, "--output=json")
		if out := decodeConfigGet(t, "json", stdout); exit != 0 || out.Value != value || !out.Present {
			t.Fatal("published TOML changed the literal scalar")
		}
	}
}

func TestConfigSetInvalidExistingDeclarationsRemainUntouched(t *testing.T) {
	t.Parallel()
	for _, original := range []string{"unknown=true\n", "read_only=1\n", "email=[\n", "email='x\xffy'\n"} {
		path := filepath.Join(t.TempDir(), "invalid.toml")
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, preview := range []bool{true, false} {
			args := []string{"config", "set", "email", "new@example.invalid", "--config", path, "--output=json", "--no-input"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var result apiFrame
			if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "config_invalid" {
				t.Fatalf("invalid file was accepted or hidden: exit=%d out=%q", exit, stdout)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != original {
				t.Fatal("invalid file declarations were partially published or discarded")
			}
		}
	}
}

func TestConfigSetInvalidUTF8RefusesBeforeCreatingFile(t *testing.T) {
	t.Parallel()
	if !xos.IsUnix() {
		t.Skip("this guard needs native argv to preserve invalid UTF-8 bytes")
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	value := string([]byte{'x', 0xff, 'y'})
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "config", "set", "email", value, "--config", path, "--output=json", "--no-input")
	var result apiFrame
	if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "arg_value_invalid" || result.Errors[0].Field != "VALUE" {
		t.Fatalf("invalid scalar was accepted or changed before refusal: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid text created local state")
	}
}

func TestConfigSetIORefusalNeverReportsPublicationOrChangesSelectedDirectory(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	marker := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact"} {
		for _, preview := range []bool{true, false} {
			args := []string{"config", "set", "read_only", "false", "--config", path, "--output=" + mode, "--no-input"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var result apiFrame
			// Descriptor validation refuses a directory as invalid settings
			// input before attempting to consume its contents.
			if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != "config_invalid" || len(result.Data) != 0 && string(result.Data) != "null" {
				t.Fatalf("I/O refusal fabricated success or data: exit=%d out=%q", exit, stdout)
			}
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatal("I/O refusal changed the selected directory")
	}
	contents, err := os.ReadFile(marker)
	if err != nil || string(contents) != "unchanged" {
		t.Fatal("I/O refusal changed existing contents")
	}
}
