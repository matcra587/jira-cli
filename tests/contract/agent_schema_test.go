package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"
	"github.com/matcra587/docent"

	agentcmd "github.com/matcra587/jira-cli/internal/cli/agent"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func readSchema(t *testing.T, stdout, mode string) (docent.Command, json.RawMessage) {
	t.Helper()
	var payload envelope.AgentSchemaOutput
	switch mode {
	case "json":
		var f frame
		if err := json.Unmarshal([]byte(stdout), &f); err != nil || !f.OK || f.Meta.Command != "agent.schema" {
			t.Fatal("schema discovery did not use the shared success envelope")
		}
		if err := json.Unmarshal(f.Data, &payload); err != nil {
			t.Fatal(err)
		}
	case "compact":
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatal(err)
		}
	default:
		payload.Schema = []byte(stdout)
	}
	var tree docent.Command
	if err := json.Unmarshal(payload.Schema, &tree); err != nil || tree.Path == "" {
		t.Fatal("discovery did not emit a command schema artifact")
	}
	return tree, payload.Schema
}

func TestAgentSchemaHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	cmd, _, err := agentcmd.New(agentcmd.Deps{}).Find([]string{"schema"})
	if err != nil {
		t.Fatal(err)
	}
	var examples [][]string
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil || len(args) == 0 {
			t.Fatal("published schema example is not an executable invocation")
		}
		examples = append(examples, args)
	}
	if len(examples) != 3 {
		t.Fatal("schema examples disappeared without workflow coverage")
	}
	for _, agent := range []bool{false, true} {
		for i, args := range examples {
			env := []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
			if agent {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("published example %d failed with agent=%t: exit=%d stderr=%q", i, agent, exit, stderr)
			}
			switch i {
			case 0:
				readSchema(t, stdout, "json")
			case 1:
				tree, _ := readSchema(t, stdout, "human") // --jq extracts the artifact itself
				if tree.Path != "jira api post" || tree.OutputSchema == nil {
					t.Fatal("published projection did not extract the selected artifact with shapes")
				}
			case 2:
				tree, _ := readSchema(t, stdout, "compact")
				if tree.Path != "jira" || len(tree.Defs) == 0 {
					t.Fatal("published shape example did not embed the full schema")
				}
			}
		}
	}
}

func TestAgentSchemaAutoUsesCompactData(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=false"}, "agent", "schema", "--path=api get")
	if exit != 0 || stderr != "" {
		t.Fatal("agent environment schema discovery failed")
	}
	tree, _ := readSchema(t, stdout, "compact")
	if tree.Path != "jira api get" || !tree.ReadOnly || tree.OutputSchema == nil {
		t.Fatal("agent auto mode lost the selected read operation or output schema")
	}
}

func TestAgentSchemaDiscoversActualTreeWithoutCredentials(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
	var first json.RawMessage
	for _, mode := range []string{"json", "compact", "human"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "agent", "schema", "--output="+mode)
		if exit != 0 || stderr != "" {
			t.Fatalf("schema %s failed: exit %d, stderr %q", mode, exit, stderr)
		}
		tree, artifact := readSchema(t, stdout, mode)
		if tree.Name != "jira" || tree.Extensions["contract_version"] != "1" {
			t.Fatal("discovery lost the tool and contract identity")
		}
		var paths []string
		var walk func(docent.Command)
		walk = func(node docent.Command) {
			paths = append(paths, node.Path)
			for _, flag := range node.Flags {
				if slices.Contains([]string{"json", "compact", "human", "plain", "@complete", "@shell"}, flag.Name) || (flag.Name == "yes" && node.Path != "jira auth logout") {
					t.Fatalf("hidden flag %q was advertised at %s", flag.Name, node.Path)
				}
			}
			if node.OutputSchema != nil || node.InputSchema != nil {
				t.Fatal("default routing artifact embedded shape bodies")
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(tree)
		for _, path := range []string{"jira agent schema", "jira api get", "jira api post", "jira issue create", "jira auth status", "jira config list", "jira version", "jira completion"} {
			if !slices.Contains(paths, path) {
				t.Fatalf("fully assembled discovery omitted %s", path)
			}
		}
		api, ok := docent.FindByPath(tree, "jira api post")
		if !ok || !api.HasOutputSchema || api.ReadOnly || len(api.Aliases) != 1 || api.Aliases[0] != "POST" {
			t.Fatal("raw write discovery lost shape, method alias or mutation class")
		}
		read, ok := docent.FindByPath(tree, "jira api get")
		if !ok || !read.ReadOnly || !read.HasOutputSchema {
			t.Fatal("raw read discovery lost its shared read classification or shape")
		}
		schema, ok := docent.FindByPath(tree, "jira agent schema")
		if !ok {
			t.Fatal("discovery omitted itself")
		}
		found := false
		for _, flag := range schema.Flags {
			if flag.Name == "path" {
				found = true
				if !slices.Equal(flag.Enum, slices.Sorted(slices.Values(paths))) {
					t.Fatal("schema path help/completion enum differs from the actual command tree")
				}
			}
		}
		if !found {
			t.Fatal("discovery omitted its path flag")
		}
		if mode == "json" {
			first = artifact
		} else {
			var a, b any
			if json.Unmarshal(first, &a) != nil || json.Unmarshal(artifact, &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatal("output mode changed the discovery artifact")
			}
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "agent", "schema", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatal("repeated schema invocation failed")
	}
	_, again := readSchema(t, stdout, "json")
	if string(first) != string(again) || requests.Load() != 0 {
		t.Fatal("schema artifact is nondeterministic or discovery contacted Jira")
	}
}

func TestAgentSchemaSubtreesShapesAndRecovery(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"api post", "jira api post"} {
		stdout, stderr, exit := runBinary(t, "agent", "schema", "--path", path, "--output=json")
		if exit != 0 || stderr != "" {
			t.Fatalf("subtree failed: %d %q", exit, stderr)
		}
		tree, _ := readSchema(t, stdout, "json")
		if tree.Path != "jira api post" || len(tree.Children) != 0 || tree.OutputSchema == nil || tree.HasOutputSchema || tree.Extensions["contract_version"] != "1" {
			t.Fatal("subtree selector did not emit the requested operation with its shape")
		}
		props := tree.OutputSchema["properties"].(map[string]any)
		for _, field := range []string{"request", "attempted", "response", "dry_run"} {
			if props[field] == nil {
				t.Fatalf("raw mutation output schema omitted %s", field)
			}
		}
		if !strings.Contains(tree.Extensions["long"].(string), "--force") || !strings.Contains(tree.Extensions["examples"].(string), "--dry-run") {
			t.Fatal("subtree omitted the actual raw workflow and safety help")
		}
		args, ok := tree.Extensions["arguments"].(map[string]any)
		usage, hasUsage := tree.Extensions["usage"].(string)
		if !ok || args["min"] != float64(1) || args["max"] != float64(1) || !hasUsage || !strings.Contains(usage, "post ENDPOINT") {
			t.Fatal("raw schema lost required endpoint usage or declared argument bounds")
		}
		positions, ok := args["positions"].([]any)
		if !ok || len(positions) != 1 || positions[0].(map[string]any)["name"] != "ENDPOINT" {
			t.Fatal("raw schema lost the declared endpoint position")
		}
	}
	stdout, stderr, exit := runBinary(t, "agent", "schema", "--shapes", "--output=compact")
	if exit != 0 || stderr != "" {
		t.Fatal("full shape emission failed")
	}
	tree, _ := readSchema(t, stdout, "compact")
	if len(tree.Defs) == 0 {
		t.Fatal("repeated shape bodies were not pooled into the artifact's definitions")
	}
	var inspect func(docent.Command)
	inspect = func(node docent.Command) {
		if node.HasOutputSchema {
			t.Fatal("shape embedding retained an omitted-shape marker")
		}
		if ref, ok := node.OutputSchema["$ref"].(string); ok && tree.Defs[strings.TrimPrefix(ref, "#/$defs/")] == nil {
			t.Fatal("pooled shape reference cannot resolve against the extracted artifact")
		}
		for _, child := range node.Children {
			inspect(child)
		}
	}
	inspect(tree)
	for _, mode := range []string{"json", "compact"} {
		stdout, _, exit := runBinary(t, "agent", "schema", "--path=missing", "--output="+mode)
		var failed struct {
			OK     bool `json:"ok"`
			Errors []struct {
				Code        string   `json:"code"`
				Field       string   `json:"field"`
				Suggestions []string `json:"suggestions"`
			} `json:"errors"`
		}
		if exit != 3 || json.Unmarshal([]byte(stdout), &failed) != nil || failed.OK || len(failed.Errors) != 1 || failed.Errors[0].Code != "flag_value_invalid" || failed.Errors[0].Field != "path" {
			t.Fatal("unknown schema path did not retain the shared input taxonomy")
		}
		if !slices.Equal(failed.Errors[0].Suggestions, []string{"jira agent schema --output=json"}) {
			t.Fatal("unknown schema path has no actionable discovery recovery")
		}
		// Execute the literal offered correction; merely naming it is insufficient.
		stdout, stderr, exit := runBinary(t, "agent", "schema", "--output=json")
		if exit != 0 || stderr != "" {
			t.Fatal("offered schema correction failed")
		}
		readSchema(t, stdout, "json")
		_, _, exit = runBinary(t, "agent", "schema", "--path=api", "--shapes", "--output="+mode)
		if exit != 3 {
			t.Fatal("conflicting schema selectors were accepted")
		}
	}
	stdout, stderr, exit = runBinary(t, "agent", "schema", "--path=api post", "--jq", ".data.schema.path")
	if exit != 0 || stderr != "" || stdout != "jira api post\n" {
		t.Fatal("actual help projection did not extract the selected schema artifact")
	}
}
