package contract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigEditPreviewSelectsExplicitPathWithoutFileOrCredentials(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing", "selected.toml")
			editor := jiraBinary(t)
			env := []string{"JIRA_EDITOR=" + shell.Quote(editor), "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN="}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "edit", "--config", path, "--dry-run", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("preview failed: exit=%d stderr=%q", exit, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, path) || !strings.Contains(stdout, editor) || !strings.Contains(stdout, "would open") {
					t.Fatalf("human preview lost selected file/editor: %q", stdout)
				}
			} else {
				var out envelope.ConfigEditOutput
				if err := json.Unmarshal(adfData(t, stdout, mode, "config.edit", true), &out); err != nil || out.Path != path || !out.DryRun || out.Changed || out.Created || len(out.Editor) != 1 || out.Editor[0] != editor {
					t.Fatalf("preview receipt incorrect: %#v, %v", out, err)
				}
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preview created the config directory: %v", err)
			}
		})
	}
}

func TestConfigEditHeadlessRefusalKeepsInvalidSelectedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "selected.toml")
	const original = "not valid toml ["
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := runBinaryEnv(t, []string{"JIRA_EDITOR=" + shell.Quote(jiraBinary(t)), "JIRA_KEYRING=false", "AGENT=1"}, "config", "edit", "--config", path, "--output=json")
	var result apiFrame
	if json.Unmarshal([]byte(stdout), &result) != nil || result.OK || exit != 3 || len(result.Errors) != 1 || result.Errors[0].Code != "terminal_required" || !strings.Contains(result.Errors[0].Message, path) {
		t.Fatalf("headless refusal lost selected path: exit=%d stdout=%q", exit, stdout)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != original {
		t.Fatalf("refused editor changed the config: %q, %v", after, err)
	}
}

func TestConfigEditPreviewSanitizesHumanPathButKeepsMachineBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "selected\x1b[2J\x07.toml")
	env := []string{"JIRA_EDITOR=" + shell.Quote(jiraBinary(t)), "JIRA_KEYRING=false"}
	stdout, stderr, exit := runBinaryEnv(t, env, "config", "edit", "--config", path, "--dry-run", "--output=human")
	if exit != 0 || strings.ContainsAny(stdout, "\x1b\x07") || !strings.Contains(stdout, "selected") {
		t.Fatalf("human preview emitted controls: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "config", "edit", "--config", path, "--dry-run", "--output=json")
	var out envelope.ConfigEditOutput
	if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "config.edit", true), &out) != nil || out.Path != path {
		t.Fatalf("machine preview changed literal path: exit=%d stderr=%q", exit, stderr)
	}
}
