//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type paritySchemaNode struct {
	Name, Path string
	Flags      []struct{ Name, Type string }
	Children   []paritySchemaNode
	Extensions map[string]json.RawMessage
	Output     json.RawMessage `json:"output_schema"`
	HasOutput  *bool           `json:"has_output_schema"`
}

func paritySchemaArtifact(t *testing.T, stdout []byte, lane, mode, operation string) []byte {
	t.Helper()
	if !jsontext.Value(stdout).IsValid() {
		t.Fatal("schema output contains duplicate names or incomplete JSON")
	}
	if lane == "before" || mode == "human" {
		return stdout
	}
	var data map[string]json.RawMessage
	if mode == "json" {
		data = parityAuthoringCleanData(t, stdout, lane, operation)
	} else if json.Unmarshal(stdout, &data) != nil {
		t.Fatal("invalid compact schema wrapper")
	}
	if len(data) != 1 || len(data["schema"]) == 0 {
		t.Fatal("current schema lost its typed artifact wrapper")
	}
	return data["schema"]
}

func paritySchemaTree(raw []byte, lane, selected string) (map[string]paritySchemaNode, bool) {
	selectedTokens := strings.Fields(selected)
	if len(selectedTokens) == 0 || !jsontext.Value(raw).IsValid() || (lane != "before" && lane != "after") {
		return nil, false
	}
	var root paritySchemaNode
	if json.Unmarshal(raw, &root) != nil || root.Path != selected || root.Name != selectedTokens[len(selectedTokens)-1] {
		return nil, false
	}
	version := `"3.2.0"`
	if lane == "after" {
		version = `"1"`
	}
	if !parityAuthoringJSON(root.Extensions["contract_version"], []byte(version)) {
		return nil, false
	}
	nodes := make(map[string]paritySchemaNode)
	var walk func(paritySchemaNode) bool
	walk = func(node paritySchemaNode) bool {
		tokens := strings.Fields(node.Path)
		if len(tokens) == 0 || strings.Join(tokens, " ") != node.Path || node.Name != tokens[len(tokens)-1] {
			return false
		}
		if _, duplicate := nodes[node.Path]; duplicate {
			return false
		}
		nodes[node.Path] = node
		for _, child := range node.Children {
			if child.Path != node.Path+" "+child.Name || !walk(child) {
				return false
			}
		}
		return true
	}
	return nodes, walk(root)
}

// Resolve only local JSON pointers against the extracted artifact. The small
// independent reader never imports Docent or the CLI's registered schemas.
func paritySchemaObject(artifact, value json.RawMessage) map[string]json.RawMessage {
	if !jsontext.Value(artifact).IsValid() || !jsontext.Value(value).IsValid() {
		return nil
	}
	for range 16 {
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || object == nil {
			return nil
		}
		raw, reference := object["$ref"]
		if !reference {
			return object
		}
		var pointer string
		if json.Unmarshal(raw, &pointer) != nil || !strings.HasPrefix(pointer, "#/") {
			return nil
		}
		value = artifact
		for _, token := range strings.Split(strings.TrimPrefix(pointer, "#/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			var members map[string]json.RawMessage
			if json.Unmarshal(value, &members) != nil {
				return nil
			}
			value = members[token]
		}
	}
	return nil
}

func parityVersionSchema(artifact, shape json.RawMessage, lane string) bool {
	if lane != "before" && lane != "after" {
		return false
	}
	object := paritySchemaObject(artifact, shape)
	if string(object["type"]) != `"object"` {
		return false
	}
	var properties map[string]json.RawMessage
	var required []string
	if json.Unmarshal(object["properties"], &properties) != nil || json.Unmarshal(object["required"], &required) != nil {
		return false
	}
	names := []string{"branch", "build_by", "build_time", "commit", "summary", "version"}
	wantRequired := slices.Clone(names)
	if lane == "after" {
		names = []string{"build_date", "commit", "dirty", "go_version", "platform", "version"}
		wantRequired = []string{"go_version", "platform", "version"}
	}
	actualNames := make([]string, 0, len(properties))
	for name := range properties {
		actualNames = append(actualNames, name)
	}
	slices.Sort(actualNames)
	slices.Sort(required)
	if !slices.Equal(actualNames, names) || !slices.Equal(required, wantRequired) {
		return false
	}
	for _, name := range names {
		property := paritySchemaObject(artifact, properties[name])
		wantType := `"string"`
		if lane == "after" {
			switch name {
			case "dirty":
				wantType = `"boolean"`
			case "platform":
				wantType = `"object"`
				var members map[string]json.RawMessage
				var platformRequired []string
				if json.Unmarshal(property["properties"], &members) != nil || len(members) != 2 || json.Unmarshal(property["required"], &platformRequired) != nil {
					return false
				}
				slices.Sort(platformRequired)
				if !slices.Equal(platformRequired, []string{"arch", "os"}) {
					return false
				}
				for _, member := range []string{"os", "arch"} {
					if string(paritySchemaObject(artifact, members[member])["type"]) != `"string"` {
						return false
					}
				}
			}
		}
		if string(property["type"]) != wantType {
			return false
		}
	}
	return true
}

func paritySchemaCensus(nodes map[string]paritySchemaNode, expected string) bool {
	paths := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(expected), "\n") {
		path := strings.TrimSpace(line)
		if path == "" || strings.Join(strings.Fields(path), " ") != path || paths[path] {
			return false
		}
		if _, present := nodes[path]; !present {
			return false
		}
		paths[path] = true
	}
	return len(nodes) == len(paths)
}

func TestParitySchemaCensusRejectsSameCountRenameAndFlattening(t *testing.T) {
	const renamed = `{"name":"jira","path":"jira","extensions":{"contract_version":"1"},"children":[{"name":"other","path":"jira other"}]}`
	t.Run("same count rename", func(t *testing.T) {
		nodes, ok := paritySchemaTree([]byte(renamed), "after", "jira")
		if !ok {
			t.Fatal("coherent foreign fixture malformed")
		}
		if paritySchemaCensus(nodes, "jira\njira version") {
			t.Fatal("census admitted a renamed command with unchanged count")
		}
	})
	t.Run("flattened hierarchy", func(t *testing.T) {
		raw := `{"name":"jira","path":"jira","extensions":{"contract_version":"1"},"children":[{"name":"adf convert","path":"jira adf convert"}]}`
		if _, ok := paritySchemaTree([]byte(raw), "after", "jira"); ok {
			t.Fatal("schema admitted a flattened multi-token command name")
		}
	})
}

func TestParityVersionSchemaRejectsIncorrectPropertyTypes(t *testing.T) {
	const before = `{"type":"object","properties":{"version":{"type":"string"},"commit":{"type":"string"},"branch":{"type":"string"},"build_time":{"type":"string"},"build_by":{"type":"string"},"summary":{"type":"string"}},"required":["version","commit","branch","build_time","build_by","summary"]}`
	const after = `{"type":"object","properties":{"version":{"type":"string"},"commit":{"type":"string"},"build_date":{"type":"string"},"dirty":{"type":"boolean"},"go_version":{"type":"string"},"platform":{"type":"object","properties":{"os":{"type":"string"},"arch":{"type":"string"}},"required":["os","arch"]}},"required":["version","go_version","platform"]}`
	for _, tc := range []struct{ name, lane, original, wrong string }{
		{"legacy build time", "before", before, strings.Replace(before, `"build_time":{"type":"string"}`, `"build_time":{"type":"number"}`, 1)},
		{"dirty boolean", "after", after, strings.Replace(after, `"dirty":{"type":"boolean"}`, `"dirty":{"type":"string"}`, 1)},
		{"Go version string", "after", after, strings.Replace(after, `"go_version":{"type":"string"}`, `"go_version":{"type":"number"}`, 1)},
		{"platform object", "after", after, strings.Replace(after, `"platform":{"type":"object"`, `"platform":{"type":"string"`, 1)},
		{"OS string", "after", after, strings.Replace(after, `"os":{"type":"string"}`, `"os":{"type":"number"}`, 1)},
		{"architecture required", "after", after, strings.Replace(after, `"required":["os","arch"]`, `"required":["os"]`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !parityVersionSchema([]byte(tc.original), []byte(tc.original), tc.lane) {
				t.Fatal("independent version schema refused")
			}
			if parityVersionSchema([]byte(tc.wrong), []byte(tc.wrong), tc.lane) {
				t.Fatal("version schema admitted an incorrect public field type")
			}
		})
	}
}

func TestParityActualBinarySchemaDiscovery(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			// Selecting a binary requires its independently frozen reference.
			reference, err := os.ReadFile("testdata/parity-schema-" + lane.name + ".txt")
			if err != nil {
				t.Fatalf("read schema reference: %v", err)
			}
			expectedPaths := string(reference)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
			defer server.Close()
			home := t.TempDir()
			run := func(mode string, wantExit int, args ...string) []byte {
				t.Helper()
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if result.Exit != wantExit || calls.Load() != 0 || (wantExit == 0 && len(result.Stderr) != 0) {
					t.Fatalf("local schema failed: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
				}
				return result.Stdout
			}
			invoke := func(mode string, args ...string) []byte {
				t.Helper()
				return run(mode, 0, args...)
			}
			check := func(name string) { evidence.Checks[name] = append(evidence.Checks[name], lane.name) }
			var baseline []byte
			for _, mode := range []string{"json", "compact", "human"} {
				stdout := invoke(mode, "agent", "schema")
				artifact := paritySchemaArtifact(t, stdout, lane.name, mode, "agent.schema")
				nodes, ok := paritySchemaTree(artifact, lane.name, "jira")
				if !ok || !paritySchemaCensus(nodes, expectedPaths) {
					t.Fatal("schema tree differs from the independently admitted canonical path set")
				}
				version, found := nodes["jira version"]
				if !found || version.HasOutput == nil || !*version.HasOutput || len(version.Output) != 0 {
					t.Fatal("default routing artifact lost its version shape marker")
				}
				if baseline == nil {
					baseline = artifact
				} else if !parityAuthoringJSON(artifact, baseline) {
					t.Fatal("schema artifact changed across output modes")
				}
				check("C010.full." + mode)
				if mode != "human" {
					root := paritySchemaArtifact(t, invoke(mode), lane.name, mode, "root")
					if !parityAuthoringJSON(root, baseline) {
						t.Fatal("bare machine root differs from agent schema artifact")
					}
					check("C001.machine." + mode)
				}
			}
			for _, path := range []string{"version", "jira version"} {
				artifact := paritySchemaArtifact(t, invoke("json", "agent", "schema", "--path", path), lane.name, "json", "agent.schema")
				nodes, ok := paritySchemaTree(artifact, lane.name, "jira version")
				version := nodes["jira version"]
				if !ok || len(nodes) != 1 || !parityVersionSchema(artifact, version.Output, lane.name) {
					t.Fatalf("scoped schema lost declared version data: %s", artifact)
				}
				flags := make(map[string]string)
				for _, flag := range version.Flags {
					if _, duplicate := flags[flag.Name]; duplicate {
						t.Fatal("schema duplicates a local flag")
					}
					flags[flag.Name] = flag.Type
				}
				wantFlags := map[string]string{"detailed": "bool"}
				if lane.name == "after" {
					wantFlags = map[string]string{"help": "bool"}
				}
				if len(flags) != len(wantFlags) {
					t.Fatal("selected schema flag census differs")
				}
				for name, kind := range wantFlags {
					if flags[name] != kind {
						t.Fatal("selected schema changed a declared flag")
					}
				}
				check("C010.path." + path)
			}
			artifact := paritySchemaArtifact(t, invoke("json", "agent", "schema", "--shapes"), lane.name, "json", "agent.schema")
			nodes, ok := paritySchemaTree(artifact, lane.name, "jira")
			if !ok || !paritySchemaCensus(nodes, expectedPaths) || !parityVersionSchema(artifact, nodes["jira version"].Output, lane.name) {
				t.Fatal("full shape artifact lost a resolvable version schema")
			}
			defaultNodes, ok := paritySchemaTree(baseline, lane.name, "jira")
			if !ok {
				t.Fatal("default schema baseline became unavailable")
			}
			for path, node := range defaultNodes {
				if node.HasOutput != nil && *node.HasOutput && paritySchemaObject(artifact, nodes[path].Output) == nil {
					t.Fatalf("--shapes omitted a declared output shape for %s", path)
				}
			}
			check("C010.shapes")
			for _, tc := range []struct {
				name, beforeCode, afterCode string
				args                        []string
			}{
				{"unknown-path", "agent_topic_unknown", "flag_value_invalid", []string{"--path=jira missing-parity-command"}},
				{"conflicting-shapes", "validation_failed", "validation_failed", []string{"--path=version", "--shapes"}},
			} {
				stdout := run("json", 3, append([]string{"agent", "schema"}, tc.args...)...)
				parityAuthoringData(t, stdout, lane.name, "agent.schema", 3)
				var frame struct {
					Data   json.RawMessage
					Errors []struct{ Code, Field string }
				}
				code := tc.beforeCode
				if lane.name == "after" {
					code = tc.afterCode
				}
				if json.Unmarshal(stdout, &frame) != nil || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != code || (lane.name == "after" && tc.name == "unknown-path" && frame.Errors[0].Field != "path") {
					t.Fatalf("schema refusal lost its stable classification: %s", stdout)
				}
				check("C010.refuse." + tc.name)
			}
		})
	}
	saveParityLocal(t, "schema", evidence)
}

func TestParitySchemaOracleRejectsForeignIdentityAndBrokenReferences(t *testing.T) {
	const valid = `{"name":"jira","path":"jira","extensions":{"contract_version":"1"},"children":[{"name":"version","path":"jira version"}]}`
	if _, ok := paritySchemaTree([]byte(valid), "after", "jira"); !ok {
		t.Fatal("independent schema fixture refused")
	}
	for _, wrong := range []string{strings.Replace(valid, `"path":"jira"`, `"path":"jira-other"`, 1), strings.Replace(valid, `"path":"jira version"`, `"path":"jira issue"`, 1), strings.Replace(valid, `"contract_version":"1"`, `"contract_version":"2"`, 1), strings.Replace(valid, `"name":"version"`, `"name":"wrong","name":"version"`, 1)} {
		if _, ok := paritySchemaTree([]byte(wrong), "after", "jira"); ok {
			t.Fatal("schema oracle accepted a foreign, malformed or duplicate identity")
		}
	}
	for _, shape := range []string{`{"$ref":"https://fixture.invalid/schema"}`, `{"$ref":"#/$defs/missing"}`, `{"$ref":"#"}`} {
		if paritySchemaObject([]byte(`{}`), []byte(shape)) != nil {
			t.Fatal("schema oracle followed an unavailable reference")
		}
	}
}
