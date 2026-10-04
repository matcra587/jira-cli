package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	inspectedPlain = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}`
	inspectedPanel = `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}]}`
)

func TestADFInspectionBinaryValidityAndNoRequests(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "AGENT=1", "JIRA_READ_ONLY=true"}
	for _, route := range []string{"validate", "lint"} {
		for _, mode := range []string{"json", "compact"} {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, inspectedPlain, nil, "adf", route, "--output="+mode)
			var out envelope.ADFValidateOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf."+route, true), &out) != nil || !out.Valid || out.Diagnostics == nil || len(out.Diagnostics) != 0 || out.SchemaSHA256 != adf.SchemaSHA256 {
				t.Fatalf("local native inspection failed: %s/%s exit%d", route, mode, exit)
			}
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "adf", "explain-node", "paragraph", "--output=json")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "paragraph_with_no_marks_node") {
		t.Fatal("local explain failed")
	}
	if requests.Load() != 0 {
		t.Fatal("local native inspection contacted Jira")
	}
}

func TestADFInspectionRefusalsRetainNamedDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, input string }{
		{"invalid enum", `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":7},"content":[]}]}`},
		{"invalid context", `{"type":"doc","version":1,"content":[{"type":"text","text":"bad root"}]}`},
		{"missing content", `{"type":"doc","version":1}`},
		{"wrapper", `{"body":` + inspectedPlain + `}`},
		{"trailing", inspectedPlain + ` {}`},
		{"malformed", `{"type":"doc",`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, route := range []string{"validate", "lint"} {
				for _, mode := range []string{"json", "compact"} {
					stdout, _, exit := runBinaryEnvInputStarted(t, nil, tc.input, nil, "adf", route, "--output="+mode)
					var f frame
					var out envelope.ADFValidateOutput
					var failure envelope.Error
					if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || f.Meta.Command != "adf."+route || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "adf_invalid" || failure.Field != "input" || failure.Hint == "" {
						t.Fatalf("wrong refusal: %s/%s exit%d %s", route, mode, exit, stdout)
					}
					if json.Unmarshal(f.Data, &out) != nil || out.Valid || len(out.Diagnostics) == 0 || out.SchemaSHA256 != adf.SchemaSHA256 {
						t.Fatal("refusal discarded invalid report")
					}
					for _, d := range out.Diagnostics {
						if d.Message == "" {
							t.Fatal("diagnostic has no cause")
						}
					}
					if tc.name == "invalid enum" {
						found := false
						for _, d := range out.Diagnostics {
							found = found || d.InstanceLocation == "/content/0/attrs/level" && d.KeywordLocation != ""
						}
						if !found {
							t.Fatal("report hid failing attribute location")
						}
					}
				}
			}
		})
	}
}

func TestADFLintReportsProjectionSeparatelyFromValidity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input             string
		valid, available, lossy bool
		types                   []string
		exit                    int
	}{
		{name: "plain", input: inspectedPlain, valid: true, available: true, types: []string{}},
		{name: "panel", input: inspectedPanel, valid: true, available: true, types: []string{}},
		{name: "opaque panel", input: strings.Replace(inspectedPanel, `"panelType":"warning"`, `"panelType":"warning","localId":"keep"`, 1), valid: true, available: true, lossy: true, types: []string{"panel"}},
		{name: "future", input: `{"type":"doc","version":1,"content":[{"type":"futureWidget","content":[{"type":"paragraph","content":[{"type":"text","text":"kept"}]}]}]}`, available: true, lossy: true, types: []string{"futureWidget"}, exit: 3},
		{name: "malformed", input: `{"type":"doc",`, types: []string{}, exit: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []string{"json", "compact"} {
				stdout, _, exit := runBinaryEnvInputStarted(t, nil, tc.input, nil, "adf", "lint", "--output="+mode)
				var out envelope.ADFLintOutput
				if exit != tc.exit || json.Unmarshal(adfData(t, stdout, mode, "adf.lint", tc.valid), &out) != nil || out.Valid != tc.valid || out.ProjectionAvailable != tc.available || out.Lossy != tc.lossy || out.LossyConstructs == nil || !slices.Equal(out.LossyConstructs, tc.types) {
					t.Fatalf("schema/projection facts hidden or confused: exit%d %s", exit, stdout)
				}
			}
		})
	}
}

func TestADFExplainPreservesContextsMarksAndRecovery(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinary(t, "adf", "explain-node", "paragraph", "--output="+mode)
		var out envelope.ADFExplainOutput
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.explain-node", true), &out) != nil || out.SchemaSHA256 != adf.SchemaSHA256 || len(out.Entries) != 5 {
			t.Fatal("explain collapsed paragraph contexts")
		}
		var plain, alignment bool
		for _, row := range out.Entries {
			switch row.Definition {
			case "paragraph_with_no_marks_node":
				plain = row.NodeBounds != nil && row.NodeBounds.Marks != nil && row.NodeBounds.Marks.Max == 0
			case "paragraph_with_alignment_node":
				alignment = row.NodeBounds != nil && row.NodeBounds.Marks != nil && slices.Contains(row.NodeBounds.Marks.Allowed, "alignment")
			}
			if row.Capabilities.Edit {
				t.Fatal("explain claimed editor acceptance")
			}
		}
		if !plain || !alignment {
			t.Fatal("context bounds were flattened")
		}
		stdout, _, exit = runBinary(t, "adf", "explain-node", "link_mark", "--output="+mode)
		if exit != 0 || json.Unmarshal(adfData(t, stdout, mode, "adf.explain-node", true), &out) != nil || len(out.Entries) != 1 || out.Entries[0].Kind != "mark" || out.Entries[0].MarkBounds == nil || !slices.Contains(out.Entries[0].MarkBounds.Attrs.Required, "href") {
			t.Fatal("mark definition lost required attributes")
		}
	}
	stdout, _, exit := runBinary(t, "adf", "explain-node", "futureWidget", "--output=compact")
	var f frame
	var failure envelope.Error
	if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "adf_type_unknown" || failure.Field != "TYPE" || !slices.Contains(failure.Suggestions, "panel") || !slices.Contains(failure.Suggestions, "paragraph_with_no_marks_node") || !strings.Contains(failure.Hint, "jira agent adf-matrix") {
		t.Fatal("unknown identity did not offer truthful discovery")
	}
	stdout, _, exit = runBinary(t, "agent", "adf-matrix", "--jq", `.data.entries[] | select(.definition=="panel_node") | .name`)
	if exit != 0 || stdout != "panel\n" {
		t.Fatal("discovery correction is not executable")
	}
	stdout, _, exit = runBinary(t, "adf", "explain-node", "panel", "--jq", `.data.entries[0].capabilities.author`)
	if exit != 0 || stdout != "true\n" {
		t.Fatal("panel authoring discovery remains stale")
	}
}

func TestADFInspectionInputPreservationHelpAndSchemas(t *testing.T) {
	t.Parallel()
	input := filepath.Join(t.TempDir(), "document.json")
	if err := os.WriteFile(input, []byte(inspectedPanel), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"validate", "lint", "explain-node"} {
		help, _, exit := runBinary(t, "adf", route, "--help", "--color=never")
		if exit != 0 {
			t.Fatal("help failed")
		}
		count := 0
		for line := range strings.SplitSeq(help, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil {
				t.Fatal(err)
			}
			for i, arg := range args {
				if arg == "document.json" {
					args[i] = input
				}
			}
			stdout, stderr, exit := runBinary(t, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("published example failed %q", line)
			}
			if !strings.Contains(line, "--jq") {
				_ = adfData(t, stdout, "json", "adf."+route, true)
			} else {
				want := "true\n"
				if route == "lint" {
					want = "[]\n"
				}
				if route == "explain-node" {
					want = "0\n"
				}
				if stdout != want {
					t.Fatalf("help returned wrong artifact: %q", stdout)
				}
			}
			count++
		}
		if count != 2 {
			t.Fatal("help example count changed without execution")
		}
		stdout, stderr, exit := runBinary(t, "agent", "schema", "--path=adf "+route, "--output=json")
		tree, _ := readSchema(t, stdout, "json")
		if exit != 0 || stderr != "" || !tree.ReadOnly || tree.OutputSchema == nil {
			t.Fatal("inspection surface lost typed read schema")
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || string(after) != inspectedPanel {
		t.Fatal("inspection changed source file")
	}
	stdout, stderr, exit := runBinary(t, "adf", "lint", "--input", input, "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Lossy projection:") || !strings.Contains(stdout, "false") || !strings.Contains(stdout, "true") {
		t.Fatal("human lint hides loss-free panel result")
	}
}

func TestADFExplainCompletionUsesCatalogWithoutCredentials(t *testing.T) {
	t.Parallel()
	env := []string{"JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_BASE_URL="}
	stdout, stderr, exit := runBinaryEnv(t, env, "--@complete=adftype", "--@shell=bash", "--", "adf", "explain-node")
	names := strings.Fields(stdout)
	if exit != 0 || stderr != "" || !slices.IsSorted(names) || !slices.Contains(names, "paragraph_with_no_marks_node") || !slices.Contains(names, "link_mark") || !slices.Contains(names, "panel") || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Fatalf("catalog completion unavailable or malformed: exit%d %q %q", exit, stdout, stderr)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "nu", "elvish", "pwsh"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "completion", shell)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "adftype") {
			t.Fatalf("%s completion did not carry registered local predictor", shell)
		}
	}
}

func TestADFLintIntegralJSONVersionsKeepSchemaResult(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"1.0", "1e0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			raw := `{"type":"doc","version":` + version + `,"content":[]}`
			for _, route := range []string{"validate", "lint"} {
				stdout, _, exit := runBinaryEnvInputStarted(t, nil, raw, nil, "adf", route, "--output=json")
				var out envelope.ADFLintOutput
				if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "adf."+route, true), &out) != nil || !out.Valid || len(out.Diagnostics) != 0 {
					t.Fatalf("schema-valid integral number failed %s", route)
				}
				if route == "lint" && (out.ProjectionAvailable || out.ProjectionDiagnostic == "") {
					t.Fatal("unavailable numeric-version projection lost its cause")
				}
			}
		})
	}
}

func TestADFExplainHumanReportsDecisiveContextBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		identity  string
		fragments []string
	}{
		{"paragraph_with_no_marks_node", []string{"Maximum marks", "0", "Allowed marks"}},
		{"link_mark", []string{"Required attributes", "href", "Allowed attributes", "title"}},
		{"panel_node", []string{"Minimum children", "1", "Maximum children", "unbounded", "Open attributes", "false"}},
	} {
		t.Run(tc.identity, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinary(t, "adf", "explain-node", tc.identity, "--output=human")
			if exit != 0 || stderr != "" {
				t.Fatal("human explain failed")
			}
			if tc.identity == "paragraph_with_no_marks_node" && !regexp.MustCompile(`(?m)^Maximum marks:\s+0$`).MatchString(stdout) {
				t.Fatal("human paragraph context changed mark maximum")
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(stdout, fragment) {
					t.Fatalf("human context hides %q", fragment)
				}
			}
		})
	}
}
