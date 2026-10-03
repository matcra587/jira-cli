package contract

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigAliasTableKeepsLiteralValuesAndFileProvenance(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			t.Cleanup(server.Close)
			const content = "keyring = false\n[aliases]\n'my.inbox' = \"issue list --status 'To Do,In Progress'\"\n"
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=false", "JIRA_ALIASES=must not replace file aliases"}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "list", "--config", path, "--output="+mode)
			if exit != 0 || calls.Load() != 0 {
				t.Fatalf("alias-bearing config failed or contacted Jira: exit=%d stderr=%q", exit, stderr)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatal("settings inspection rewrote the alias-bearing config")
			}
			if mode == "human" {
				if !strings.Contains(stdout, "my.inbox") || !strings.Contains(stdout, "To Do,In Progress") || !strings.Contains(stdout, "file") {
					t.Fatal("human settings omitted literal alias data or provenance")
				}
				return
			}
			payload := []byte(stdout)
			if mode == "json" {
				var f frame
				if err := json.Unmarshal(payload, &f); err != nil || !f.OK || f.Meta.Command != "config.list" {
					t.Fatal("alias settings lost their shared success envelope")
				}
				payload = f.Data
			}
			var out envelope.ConfigListOutput
			if err := json.Unmarshal(payload, &out); err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, setting := range out.Settings {
				if setting.Key == "aliases" {
					found++
					var values map[string]string
					if err := json.Unmarshal([]byte(setting.Value), &values); err != nil || !maps.Equal(values, map[string]string{"my.inbox": "issue list --status 'To Do,In Progress'"}) || setting.Source != "file" || setting.Secret {
						t.Fatal("alias settings changed literal data, source or redaction")
					}
				}
			}
			if found != 1 || !out.FileExists || out.Path != path {
				t.Fatal("config inspection lost the alias table or selected file")
			}
		})
	}
}

func TestConfigMalformedAliasTableFailsBeforeHTTP(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[aliases]\ninbox = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_ALIASES=invalid"}, "config", "list", "--config", path, "--output=json")
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil || f.OK || exit != cli.ExitValidation || len(f.Errors) != 1 || calls.Load() != 0 {
		t.Fatalf("invalid file alias type reached HTTP or returned success: exit=%d stderr=%q", exit, stderr)
	}
}

func TestConfigAliasHumanOutputSanitizesControlsWithoutChangingMachineValues(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	const content = `[aliases]
inbox = "version\u001b[2J\u0007\u0085hidden\u009b"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
	stdout, stderr, exit := runBinaryEnv(t, env, "config", "list", "--config", path, "--output=human")
	if exit != 0 || strings.ContainsAny(stdout, "\x1b\a\u0085\u009b") || !strings.Contains(stdout, "hidden") {
		t.Fatalf("human alias settings emitted controls or lost visible text: exit=%d stderr=%q", exit, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "config", "list", "--config", path, "--output=json")
	var f struct {
		OK   bool                      `json:"ok"`
		Data envelope.ConfigListOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &f); err != nil || !f.OK || exit != 0 {
		t.Fatalf("machine alias settings failed: exit=%d stderr=%q", exit, stderr)
	}
	for _, setting := range f.Data.Settings {
		if setting.Key == "aliases" {
			var values map[string]string
			if err := json.Unmarshal([]byte(setting.Value), &values); err != nil || values["inbox"] != "version\x1b[2J\a\u0085hidden\u009b" {
				t.Fatal("human sanitization changed the original machine alias value")
			}
			return
		}
	}
	t.Fatal("machine settings omitted the alias table")
}
