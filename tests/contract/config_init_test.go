package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"

	configcmd "github.com/matcra587/jira-cli/internal/cli/config"
	cfg "github.com/matcra587/jira-cli/internal/config"
)

type configInitReceipt struct {
	DryRun      bool   `json:"dry_run"`
	Path        string `json:"path"`
	BaseURL     string `json:"base_url"`
	Email       string `json:"email"`
	WouldCreate bool   `json:"would_create"`
	Created     bool   `json:"created"`
}

func decodeConfigInit(t *testing.T, mode, stdout string) configInitReceipt {
	t.Helper()
	raw := []byte(stdout)
	if mode == "json" {
		var frame struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
			Meta struct {
				Command string `json:"command"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil || !frame.OK || frame.Meta.Command != "config.init" {
			t.Fatalf("invalid initialization envelope: %v %q", err, stdout)
		}
		raw = frame.Data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dry_run", "path", "base_url", "email", "would_create", "created"} {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			t.Fatalf("initialization omitted %s: %q", name, stdout)
		}
	}
	var out configInitReceipt
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConfigInitCreatesOnlyExplicitAccountMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("configuration initialization contacted Jira")
	}))
	defer server.Close()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "new-parent", "settings.toml")
			env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_API_TOKEN=", "JIRA_EMAIL=environment@example.invalid", "JIRA_BASE_URL=https://environment.example.invalid", "JIRA_TIMEOUT=19s", "JIRA_READ_ONLY=true"}
			args := []string{"config", "init", "--config", path, "--base-url", server.URL, "--email", "explicit@example.invalid", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			for _, preview := range []bool{true, false} {
				invocation := append([]string(nil), args...)
				if preview {
					invocation = append(invocation, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, env, invocation...)
				if exit != 0 || stderr != "" {
					t.Fatalf("initialization failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
				if mode == "human" {
					want := "created config"
					if preview {
						want = "would create config"
					}
					if !strings.Contains(stdout, want) || !strings.Contains(stdout, path) {
						t.Fatalf("human initialization lost publication state or path: %q", stdout)
					}
				} else {
					out := decodeConfigInit(t, mode, stdout)
					if out.DryRun != preview || out.Created == preview || !out.WouldCreate || out.Path != path || out.BaseURL != server.URL || out.Email != "explicit@example.invalid" {
						t.Fatalf("initialization lost explicit values or publication state: %+v", out)
					}
				}
				if preview {
					if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
						t.Fatal("initialization preview created its parent")
					}
				}
			}
			contents, err := os.ReadFile(path)
			if err != nil || cfg.ValidateFile(contents) != nil {
				t.Fatalf("initialization did not publish a valid file: %q %v", contents, err)
			}
			want := "base_url = '" + server.URL + "'\nemail = 'explicit@example.invalid'\n"
			// TOML encoding is not the contract; independently read the two
			// stored declarations and reject additional effective settings.
			file, err := cfg.OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			for key, expected := range map[string]string{"base_url": server.URL, "email": "explicit@example.invalid"} {
				setting, err := file.Setting(key)
				if err != nil || !setting.Present || setting.Value != expected {
					t.Fatalf("stored %s differs from explicit input: %+v %v", key, setting, err)
				}
			}
			if len(strings.Split(strings.TrimSpace(string(contents)), "\n")) != len(strings.Split(strings.TrimSpace(want), "\n")) {
				t.Fatalf("initialization persisted defaults, environment or credentials: %q", contents)
			}
			if !xos.IsWindows() {
				for target, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
					info, err := os.Stat(target)
					if err != nil || info.Mode().Perm() != mode {
						t.Fatalf("initialization did not secure %s: %v %v", target, info, err)
					}
				}
			}
		})
	}
}

func TestConfigInitRequiresCompleteExplicitInputBeforeFileIO(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "neither"},
		{name: "missing email", args: []string{"--base-url=https://example.invalid"}},
		{name: "missing site", args: []string{"--email=person@example.invalid"}},
		{name: "blank email", args: []string{"--base-url=https://example.invalid", "--email=  "}},
		{name: "blank site", args: []string{"--base-url=", "--email=person@example.invalid"}},
		{name: "malformed site", args: []string{"--base-url=example.invalid", "--email=person@example.invalid"}},
		{name: "credential site", args: []string{"--base-url=https://person:" + "private-marker" + "@example.invalid", "--email=person@example.invalid"}},
		{name: "remote plaintext", args: []string{"--base-url=http://example.invalid", "--email=person@example.invalid"}},
		{name: "invalid email utf8", args: []string{"--base-url=https://example.invalid", "--email=\xff"}},
		{name: "invalid site utf8", args: []string{"--base-url=https://example.invalid/\xff", "--email=person@example.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "new-parent", "settings.toml")
			args := append([]string{"config", "init", "--config", path, "--output=json", "--no-input"}, tc.args...)
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_BASE_URL=https://environment.example.invalid", "JIRA_EMAIL=environment@example.invalid", "JIRA_KEYRING=false"}, args...)
			var frame struct {
				OK     bool            `json:"ok"`
				Data   json.RawMessage `json:"data"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			code := "arg_value_invalid"
			if tc.name == "neither" || strings.HasPrefix(tc.name, "missing ") {
				code = "validation_failed"
			}
			if err := json.Unmarshal([]byte(stdout), &frame); err != nil || exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || string(frame.Data) != "null" {
				t.Fatalf("invalid input did not refuse before initialization: exit=%d stdout=%q stderr=%q err=%v", exit, stdout, stderr, err)
			}
			if strings.Contains(stdout+stderr, "private-marker") {
				t.Fatal("initialization disclosed a credential-bearing site")
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("partial or invalid initialization touched the destination")
			}
		})
	}
}

func TestConfigInitDefaultAndRelativePathsUseSelectedFilesystemOwner(t *testing.T) {
	t.Parallel()
	for _, selected := range []string{"default", "relative"} {
		t.Run(selected, func(t *testing.T) {
			work := t.TempDir()
			xdg := filepath.Join(work, "selected-config")
			path := filepath.Join(xdg, "jira", "config.toml")
			args := []string{"config", "init", "--base-url=https://example.invalid", "--email=person@example.invalid", "--no-input", "--output=json"}
			if selected == "relative" {
				path = filepath.Join(work, "settings with spaces.toml")
				args = append(args, "--config=settings with spaces.toml")
			}
			stdout, stderr, exit := runBinaryIOInDirStarted(t, work, []string{"XDG_CONFIG_HOME=" + xdg, "JIRA_KEYRING=false"}, nil, nil, nil, args...)
			out := decodeConfigInit(t, "json", stdout)
			if exit != 0 || stderr != "" || out.Path != path || !out.Created || out.DryRun {
				t.Fatalf("initialization lost selected path: %d %q %q", exit, stdout, stderr)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigInitHumanSanitizesControlsWhileMachineRetainsValues(t *testing.T) {
	t.Parallel()
	const email = "person\x1b[31m\r@example.invalid"
	for _, mode := range []string{"human", "json", "compact"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		stdout, stderr, exit := runBinary(t, "config", "init", "--config", path, "--base-url=https://example.invalid", "--email", email, "--dry-run", "--output="+mode)
		if exit != 0 || stderr != "" {
			t.Fatalf("controlled metadata failed: %d %q %q", exit, stdout, stderr)
		}
		if mode == "human" {
			if strings.ContainsAny(stdout, "\x1b\r") {
				t.Fatalf("human initialization retained terminal controls: %q", stdout)
			}
		} else if out := decodeConfigInit(t, mode, stdout); out.Email != email || out.Created || !out.DryRun {
			t.Fatalf("machine preview lost literal input or claimed publication: %+v", out)
		}
	}
}

func TestConfigInitDiscoveryAndPublishedHelpExamples(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "schema", "--path=config init", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("initialization discovery failed: %d %q %q", exit, stdout, stderr)
	}
	tree, _ := readSchema(t, stdout, "json")
	if tree.Path != "jira config init" || tree.ReadOnly || tree.OutputSchema == nil {
		t.Fatal("initialization discovery lost path, local mutation class or output shape")
	}
	for _, name := range []string{"base-url", "email", "dry-run"} {
		found := false
		for _, flag := range tree.Flags {
			if flag.Name == name {
				found = true
				if name != "dry-run" && !flag.Required {
					t.Fatalf("discovery advertised required --%s as optional", name)
				}
			}
		}
		if !found {
			t.Fatalf("discovery omitted --%s", name)
		}
	}
	properties, ok := tree.OutputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("initialization output shape has no properties")
	}
	for _, name := range []string{"dry_run", "path", "base_url", "email", "would_create", "created"} {
		if properties[name] == nil {
			t.Fatalf("initialization output schema omitted %s", name)
		}
	}
	cmd, _, err := configcmd.New(configcmd.Deps{}).Find([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		for _, agent := range []bool{false, true} {
			env := []string{"XDG_CONFIG_HOME=" + t.TempDir(), "JIRA_KEYRING=false"}
			if agent {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" || stdout == "" {
				t.Fatalf("published initialization example failed: %q agent=%t exit=%d stderr=%q", line, agent, exit, stderr)
			}
		}
		count++
	}
	if count != 2 {
		t.Fatal("initialization examples lost workflow coverage")
	}
}

func TestConfigInitSymlinkedDefaultAndNestedSelectedPaths(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() {
		t.Skip("symlink creation requires an enabled Windows privilege")
	}
	for _, selected := range []string{"default", "explicit"} {
		t.Run(selected, func(t *testing.T) {
			dir := t.TempDir()
			target, link := filepath.Join(dir, "actual"), filepath.Join(dir, "selected")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(target, "jira", "config.toml")
			args := []string{"config", "init", "--base-url=https://example.invalid", "--email=person@example.invalid", "--no-input", "--output=json"}
			if selected == "explicit" {
				path = filepath.Join(target, "missing", "nested", "settings.toml")
				args = append(args, "--config", filepath.Join(link, "missing", "nested", "settings.toml"))
			}
			for _, preview := range []bool{true, false} {
				invocation := append([]string(nil), args...)
				if preview {
					invocation = append(invocation, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, []string{"XDG_CONFIG_HOME=" + link, "JIRA_KEYRING=false"}, invocation...)
				out := decodeConfigInit(t, "json", stdout)
				if exit != 0 || stderr != "" || out.Path != path || out.Created == preview || out.DryRun != preview {
					t.Fatalf("linked initialization lost resolved destination or publication: %d %q %q", exit, stdout, stderr)
				}
				if preview {
					if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
						t.Fatal("linked preview created missing parents")
					}
				}
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigInitRefusesExistingFileWithoutReadingItsValues(t *testing.T) {
	t.Parallel()
	for _, original := range []string{"", "# preserve authored bytes\nemail='original@example.invalid'\n", "invalid = [", "api_token='private-marker'\n"} {
		for _, preview := range []bool{true, false} {
			path := filepath.Join(t.TempDir(), "settings.toml")
			if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"config", "init", "--config", path, "--base-url=https://example.invalid", "--email=person@example.invalid", "--no-input", "--output=json"}
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var frame struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &frame); err != nil || exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "config_changed" {
				t.Fatalf("existing initialization was not refused: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, "private-marker") {
				t.Fatal("existing secret appeared in initialization output")
			}
			contents, err := os.ReadFile(path)
			after, statErr := os.Stat(path)
			if err != nil || statErr != nil || string(contents) != original || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("initialization replaced an existing file or its metadata")
			}
		}
	}
}
