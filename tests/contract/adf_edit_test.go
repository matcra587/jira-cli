package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Keep this native fixture independent of the Markdown converter: unknown
// state and raw escape spelling must survive the public editing boundary.
const anchoredSource = `{"type":"doc","version":1,"future":"<>&\u2028","content":[{"type":"paragraph","content":[{"type":"text","text":"original"}]},{"type":"futureBlock","attrs":{"id":9007199254740993,"extra":"\u003c"},"content":[{"type":"text","text":"protected"}]}]}`

type editArtifact struct {
	Markdown string `json:"markdown"`
	Regions  []struct {
		ID        string `json:"id"`
		NodeType  string `json:"node_type"`
		Protected bool   `json:"protected"`
	} `json:"regions"`
	Document    json.RawMessage `json:"document"`
	Diagnostics []struct {
		Kind   string `json:"kind"`
		Region string `json:"region"`
		Reason string `json:"reason"`
	} `json:"diagnostics"`
}

func editFile(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func prepareAnchored(t *testing.T, source, mode string) editArtifact {
	t.Helper()
	stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_READ_ONLY=true"}, source, nil, "adf", "edit", "prepare", "--output="+mode)
	var out editArtifact
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.edit.prepare", true), &out) != nil {
		t.Fatalf("prepare exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	return out
}

func TestADFAnchoredPrepareAndApplyPreserveNativeBytes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			prepared := prepareAnchored(t, anchoredSource, mode)
			if len(prepared.Regions) != 2 || prepared.Regions[0].Protected || !prepared.Regions[1].Protected || prepared.Regions[1].NodeType != "futureBlock" || !strings.Contains(prepared.Markdown, prepared.Regions[1].ID) {
				t.Fatal("prepare lost native protection or anchor metadata")
			}
			original := editFile(t, "source.json", anchoredSource)
			markdown := editFile(t, "edited.md", prepared.Markdown)
			stdout, stderr, exit := runBinary(t, "adf", "edit", "apply", "--input", original, "--markdown-file", markdown, "--output="+mode)
			var out editArtifact
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.edit.apply", true), &out) != nil || string(out.Document) != anchoredSource || out.Diagnostics == nil || len(out.Diagnostics) != 0 {
				t.Fatalf("unchanged apply lost bytes: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			for path, want := range map[string]string{original: anchoredSource, markdown: prepared.Markdown} {
				after, err := os.ReadFile(path)
				if err != nil || string(after) != want {
					t.Fatal("local editing changed an input file")
				}
			}
		})
	}
}

func TestADFAnchoredApplyEditsWithoutRebuildingProtectedBlocks(t *testing.T) {
	t.Parallel()
	prepared := prepareAnchored(t, anchoredSource, "json")
	for _, tc := range []struct{ name, markdown, want string }{
		{name: "replacement", markdown: strings.Replace(prepared.Markdown, "original", "**edited**", 1), want: `"text":"edited","marks":[{"type":"strong"}]`},
		{name: "insertion", markdown: prepared.Markdown + "added block\n", want: `"text":"added block"`},
		{name: "deletion", markdown: prepared.Markdown[strings.Index(prepared.Markdown, "<!-- jira-adf:begin "+prepared.Regions[1].ID):], want: `"text":"protected"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runBinary(t, "adf", "edit", "apply", "--input", editFile(t, "source.json", anchoredSource), "--markdown-file", editFile(t, "edited.md", tc.markdown), "--output=compact")
			var out editArtifact
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &out) != nil || !strings.Contains(string(out.Document), tc.want) || !strings.Contains(string(out.Document), `{"type":"futureBlock","attrs":{"id":9007199254740993,"extra":"\u003c"},"content":[{"type":"text","text":"protected"}]}`) || !strings.Contains(string(out.Document), `"future":"<>&\u2028"`) {
				t.Fatalf("anchored edit lost native state: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if tc.name == "deletion" && strings.Contains(string(out.Document), "original") {
				t.Fatal("deleted editable region survived")
			}
		})
	}
}

func TestADFAnchoredFailuresRetainDiagnosticsWithoutCandidate(t *testing.T) {
	t.Parallel()
	prepared := prepareAnchored(t, anchoredSource, "json")
	for _, mode := range []string{"json", "compact"} {
		for _, tc := range []struct{ name, markdown, kind string }{
			{name: "changed protected region", markdown: strings.Replace(prepared.Markdown, "protected\n", "tampered\n", 1), kind: "protected_region"},
			{name: "deleted protected region", markdown: "", kind: "protected_region"},
			{name: "broken anchor", markdown: strings.Replace(prepared.Markdown, "jira-adf:end", "jira-adf:begin", 1), kind: "invalid_anchor"},
			{name: "unknown anchor", markdown: strings.ReplaceAll(prepared.Markdown, prepared.Regions[0].ID, "unknown"), kind: "invalid_anchor"},
			{name: "lossy authored content", markdown: prepared.Markdown + "\n<script>discarded</script>\n", kind: "lossy_content"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				stdout, stderr, exit := runBinary(t, "adf", "edit", "apply", "--input", editFile(t, "source.json", anchoredSource), "--markdown-file", editFile(t, "edited.md", tc.markdown), "--output="+mode)
				var f frame
				var out editArtifact
				if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || json.Unmarshal(f.Data, &out) != nil || len(out.Document) != 0 || len(out.Diagnostics) != 1 || out.Diagnostics[0].Kind != tc.kind || out.Diagnostics[0].Reason == "" {
					t.Fatalf("unsafe edit failed without proper diagnostics: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
			})
		}
	}
}

func TestADFAnchoredMergeKeepsCurrentExtensionsAndRejectsConflicts(t *testing.T) {
	t.Parallel()
	prepared := prepareAnchored(t, anchoredSource, "json")
	markdown := editFile(t, "edited.md", strings.Replace(prepared.Markdown, "original", "edited", 1))
	for _, tc := range []struct {
		name, current string
		success       bool
		kind          string
	}{
		{name: "current root extension", current: strings.Replace(anchoredSource, `"future":"<>&\u2028"`, `"future":"remote root"`, 1), success: true},
		{name: "competing edit", current: strings.Replace(anchoredSource, "original", "competing", 1), kind: "merge_conflict"},
		{name: "ambiguous protected identity", current: strings.Replace(anchoredSource, `"text":"protected"`, `"text":"remote protected"`, 1), kind: "ambiguous_identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runBinary(t, "adf", "edit", "merge", "--input", editFile(t, "source.json", anchoredSource), "--markdown-file", markdown, "--current", editFile(t, "current.json", tc.current), "--output=json")
			var f frame
			var out editArtifact
			if tc.success && stderr != "" || json.Unmarshal([]byte(stdout), &f) != nil || f.OK != tc.success || json.Unmarshal(f.Data, &out) != nil {
				t.Fatalf("merge frame exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if tc.success {
				if exit != 0 || !strings.Contains(string(out.Document), `"text":"edited"`) || !strings.Contains(string(out.Document), `"future":"remote root"`) {
					t.Fatal("merge ignored current or local edit")
				}
				return
			}
			if exit != 3 || len(out.Document) != 0 || len(out.Diagnostics) != 1 || out.Diagnostics[0].Kind != tc.kind {
				t.Fatalf("merge refusal lost diagnostics: %q", stdout)
			}
		})
	}
}

func TestADFAnchoredEmptyEditedInputDeletesEditableDocument(t *testing.T) {
	t.Parallel()
	const source = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"remove me"}]}]}`
	stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, "", nil, "adf", "edit", "apply", "--input", editFile(t, "source.json", source), "--markdown-file", "-", "--output=compact")
	var out editArtifact
	if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &out) != nil || string(out.Document) != `{"type":"doc","version":1,"content":[]}` {
		t.Fatalf("empty authored edit was refused: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestADFAnchoredEmptySourceHasArrayRegions(t *testing.T) {
	t.Parallel()
	prepared := prepareAnchored(t, `{"type":"doc","version":1,"content":[]}`, "json")
	if prepared.Markdown != "" || prepared.Regions == nil || len(prepared.Regions) != 0 {
		t.Fatal("empty native source did not expose an empty region array")
	}
}

func TestADFAnchoredInputRefusalsNameTheOffendingField(t *testing.T) {
	t.Parallel()
	source := editFile(t, "source.json", anchoredSource)
	for _, tc := range []struct {
		name         string
		args         []string
		stdin, field string
	}{
		{name: "missing edited input", args: []string{"apply", "--input", "does-not-exist.json"}, field: "markdown-file"},
		{name: "missing current input", args: []string{"merge", "--input", "does-not-exist.json", "--markdown-file", "does-not-exist.md"}, field: "current"},
		{name: "two stdin consumers", args: []string{"apply", "--markdown-file", "-"}, field: "markdown-file"},
		{name: "three stdin consumers", args: []string{"merge", "--markdown-file", "-", "--current", "-"}, field: "markdown-file"},
		{name: "malformed original", args: []string{"prepare"}, stdin: "null", field: "input"},
		{name: "wrong source version", args: []string{"prepare"}, stdin: `{"type":"doc","version":2,"content":[]}`, field: "input"},
		{name: "invalid edited UTF8", args: []string{"apply", "--input", source, "--markdown-file", "-"}, stdin: string([]byte{0xff}), field: "markdown-file"},
		{name: "oversized edited input", args: []string{"apply", "--input", source, "--markdown-file", "-"}, stdin: strings.Repeat("x", (1<<20)+1), field: "markdown-file"},
		{name: "malformed current", args: []string{"merge", "--input", source, "--markdown-file", editFile(t, "edited.md", ""), "--current", "-"}, stdin: "null", field: "current"},
		{name: "wrong current version", args: []string{"merge", "--input", source, "--markdown-file", editFile(t, "edited.md", ""), "--current", "-"}, stdin: `{"type":"doc","version":2,"content":[]}`, field: "current"},
		{name: "duplicate current key", args: []string{"merge", "--input", source, "--markdown-file", editFile(t, "edited.md", ""), "--current", "-"}, stdin: `{"type":"doc","version":1,"future":1,"future":2,"content":[]}`, field: "current"},
		{name: "duplicate source key", args: []string{"prepare"}, stdin: `{"type":"doc","version":1,"future":1,"future":2,"content":[]}`, field: "input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"adf", "edit"}, tc.args...)
			args = append(args, "--output=json")
			stdout, _, exit := runBinaryEnvInputStarted(t, nil, tc.stdin, nil, args...)
			var f frame
			var failure envelope.Error
			if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "flag_value_invalid" || failure.Field != tc.field || string(f.Data) != "null" {
				t.Fatalf("input refusal lost field: exit=%d stdout=%q", exit, stdout)
			}
		})
	}
}

func TestADFAnchoredPublishedHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	prepared := prepareAnchored(t, anchoredSource, "json")
	for name, value := range map[string]string{"document.json": anchoredSource, "edited.md": prepared.Markdown, "current.json": anchoredSource} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, verb := range []string{"prepare", "apply", "merge"} {
		t.Run(verb, func(t *testing.T) {
			help, stderr, exit := runBinary(t, "adf", "edit", verb, "--help", "--color=never")
			if exit != 0 || stderr != "" {
				t.Fatal("editing help failed")
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
				for i, value := range args {
					if value == "document.json" || value == "edited.md" || value == "current.json" {
						args[i] = filepath.Join(directory, value)
					}
				}
				stdout, stderr, exit := runBinary(t, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("editing example failed: %q exit=%d stderr=%q", line, exit, stderr)
				}
				if strings.Contains(line, "--jq") {
					if verb == "prepare" {
						if strings.TrimSuffix(stdout, "\n") != prepared.Markdown {
							t.Fatal("prepare example lost anchored text")
						}
					} else if equal, err := adf.StructuralEqual([]byte(stdout), []byte(anchoredSource)); err != nil || !equal {
						t.Fatalf("candidate extraction lost native state: %q", stdout)
					}
				} else {
					_ = adfData(t, stdout, "json", "adf.edit."+verb, true)
				}
				count++
			}
			if count != 2 {
				t.Fatal("editing examples changed without executable coverage")
			}
		})
	}
}
