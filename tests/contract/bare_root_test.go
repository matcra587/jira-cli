package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/matcra587/docent"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestBareRootDiscoversSameTreeAsAgentSchema(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN="}
	for _, tc := range []struct {
		name, mode string
		args, env  []string
	}{
		{"json", "json", []string{"--output=json"}, nil},
		{"compact", "compact", []string{"--output=compact"}, nil},
		{"piped auto", "json", nil, nil},
		{"agent auto", "compact", nil, []string{"AGENT=1"}},
		{"agent explicit json", "json", []string{"--output=json"}, []string{"AGENT=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnv(t, slices.Concat(env, tc.env), tc.args...)
			if exit != 0 || stderr != "" {
				t.Fatal("credential-free bare discovery failed")
			}
			payload := []byte(stdout)
			if tc.mode == "json" {
				var f frame
				if json.Unmarshal(payload, &f) != nil || !f.OK || f.Meta.Command != "root" {
					t.Fatal("bare discovery lost its root invocation frame")
				}
				payload = f.Data
			}
			var out envelope.AgentSchemaOutput
			if json.Unmarshal(payload, &out) != nil || len(out.Schema) == 0 {
				t.Fatal("bare discovery returned no schema")
			}
			var tree docent.Command
			if json.Unmarshal(out.Schema, &tree) != nil || tree.Path != "jira" || len(tree.Children) == 0 || tree.OutputSchema != nil || !tree.ReadOnly {
				t.Fatal("bare discovery lost canonical routing, read class or shape stripping")
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "agent", "schema", "--output=json")
			if exit != 0 || stderr != "" {
				t.Fatal("comparison discovery failed")
			}
			_, artifact := readSchema(t, stdout, "json")
			if !bytes.Equal(artifact, out.Schema) {
				t.Fatal("bare discovery differs from the same installed agent schema")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("local discovery contacted Jira")
	}
}

func TestBareRootFiltersAndRetainsDiscoveryOnFilterFailure(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--output=json", "--jq=.data.schema.path"},
		{"--output=compact", "--jq=.schema.path"},
		{"--jq=.data.schema.path"},
	} {
		stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=false"}, args...)
		if exit != 0 || stderr != "" || stdout != "jira\n" {
			t.Fatalf("bare-root filtering failed: args=%q exit=%d stdout=%q stderr=%q", args, exit, stdout, stderr)
		}
	}
	for _, mode := range []string{"json", "compact"} {
		prefix := ".data.schema"
		if mode == "compact" {
			prefix = ".schema"
		}
		stdout, stderr, exit := runBinary(t, "--output="+mode, "--jq="+prefix+".missing[]")
		var f frame
		if exit != 3 || !strings.Contains(stderr, "jq expression failed:") || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || f.Meta.Command != "root" || len(f.Errors) != 1 {
			t.Fatal("root filter failure did not retain its typed failure frame")
		}
		var failure envelope.Error
		if json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "jq_eval_failed" {
			t.Fatal("root filter failure changed error classification")
		}
		var out envelope.AgentSchemaOutput
		if json.Unmarshal(f.Data, &out) != nil || !strings.Contains(string(out.Schema), `"path":"jira"`) {
			t.Fatal("failed root projection discarded unfiltered discovery data")
		}
	}
}

func TestBareRootHumanUsesNativeHelpStream(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "--output=human")
	if exit != 0 || stdout != "" {
		t.Fatal("bare human invocation did not preserve the native help stream")
	}
	for _, want := range []string{"Usage", "jira", "agent", "issue", "guide", "version"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("native help omitted %q", want)
		}
	}
}

func TestBareRootNativeHelpHonorsExplicitColor(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"always", "never"} {
		stdout, stderr, exit := runBinaryEnv(t, []string{"NO_COLOR=1", "CLICOLOR_FORCE=1"}, "--output=human", "--color="+mode)
		if exit != 0 || stdout != "" || !strings.Contains(stderr, "Usage") || strings.Contains(stderr, "\x1b[") != (mode == "always") {
			t.Fatalf("explicit help color policy was lost: %s", mode)
		}
	}
}

func TestBareRootPublishedExamplesExecute(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatal("could not discover installed root examples")
	}
	var f frame
	var out envelope.AgentSchemaOutput
	var tree docent.Command
	if json.Unmarshal([]byte(stdout), &f) != nil || json.Unmarshal(f.Data, &out) != nil || json.Unmarshal(out.Schema, &tree) != nil {
		t.Fatal("root example discovery returned invalid data")
	}
	examples, ok := tree.Extensions["examples"].(string)
	if !ok {
		t.Fatal("runtime schema omitted root examples")
	}
	count := 0
	for line := range strings.SplitSeq(examples, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=false"}, args...)
		if exit != 0 || stderr != "" {
			t.Fatalf("published root example failed: %q", line)
		}
		switch count {
		case 0:
			var f frame
			if json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || f.Meta.Command != "root" {
				t.Fatal("root frame example lost its operation")
			}
		case 1:
			if !strings.Contains(stdout, "jira agent\n") || !strings.Contains(stdout, "jira issue\n") {
				t.Fatal("root routing projection omitted actual commands")
			}
		case 2:
			var contract map[string]any
			if json.Unmarshal([]byte(stdout), &contract) != nil || contract["json"] == nil || contract["compact"] == nil || contract["envelope"] == nil {
				t.Fatal("root output-contract projection lost its documented shape")
			}
		}
		count++
	}
	if count != 3 {
		t.Fatal("root examples changed without execution coverage")
	}
}

func TestBareRootBrokenPipeUsesOutputExitContract(t *testing.T) {
	t.Parallel()
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	for _, tc := range []struct {
		name   string
		args   []string
		stderr bool
	}{
		{"root json", []string{"--output=json"}, false},
		{"root compact", []string{"--output=compact"}, false},
		{"version json", []string{"version", "--output=json"}, false},
		{"native help stderr", []string{"--output=human"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Close() }()
			directory := t.TempDir()
			cmd := exec.CommandContext(t.Context(), jiraBinary(t), tc.args...)
			cmd.Dir = directory
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + directory, "NO_COLOR=1", "TERM=dumb", "JIRA_KEYRING=false", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1"}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if tc.stderr {
				cmd.Stderr = writer
			} else {
				cmd.Stdout = writer
			}
			err = cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 8 {
				t.Fatalf("closed output pipe bypassed exit 8: %v", err)
			}
			if tc.stderr && stdout.Len() != 0 {
				t.Fatal("native help failure wrote machine stdout")
			}
		})
	}
}
