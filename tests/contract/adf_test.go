package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func adfData(t *testing.T, stdout, mode, op string, success bool) json.RawMessage {
	t.Helper()
	if mode == "compact" && success {
		return json.RawMessage(stdout)
	}
	var f frame
	if json.Unmarshal([]byte(stdout), &f) != nil || f.OK != success || f.Meta.Command != op {
		t.Fatalf("ADF operation lost its frame: %q", stdout)
	}
	return f.Data
}

func TestADFConvertSharesBodyConverterAndRequiresNoJira(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_READ_ONLY=true", "AGENT=1"}
	for _, mode := range []string{"json", "compact"} {
		const markdown = "# Hello\n\n**bold** and <>&\n\n- first\n- second"
		stdout, stderr, exit := runBinaryEnvInputStarted(t, env, markdown, nil, "adf", "convert", "--output="+mode)
		var out envelope.ADFConvertOutput
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out) != nil || out.Lossy || out.Diagnostics == nil {
			t.Fatalf("local conversion failed: exit=%d stderr=%q", exit, stderr)
		}
		doc, _, err := adf.FromMarkdownLossy(markdown)
		if err != nil {
			t.Fatal(err)
		}
		want, err := doc.Marshal()
		if err != nil || string(want) != string(out.Document) || adf.Validate(out.Document) != nil {
			t.Fatal("public conversion differs from the shared body converter or failed validation")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("local conversion contacted Jira")
	}
}

func TestADFConvertStrictLossRetainsCandidateAndExecutableCorrection(t *testing.T) {
	t.Parallel()
	input := filepath.Join(t.TempDir(), "notes 'quoted'.md")
	if err := os.WriteFile(input, []byte("<script>dropped</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact"} {
		stdout, _, exit := runBinary(t, "adf", "convert", "--input", input, "--output="+mode)
		var f frame
		var failure envelope.Error
		var out envelope.ADFConvertOutput
		if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "adf_content_loss" {
			t.Fatal("strict conversion did not refuse actual content loss")
		}
		if json.Unmarshal(f.Data, &out) != nil || !out.Lossy || len(out.Diagnostics) == 0 || adf.Validate(out.Document) != nil {
			t.Fatal("strict refusal discarded diagnostics or the valid candidate")
		}
		if len(failure.Suggestions) != 1 {
			t.Fatal("strict refusal offered no executable recovery")
		}
		args, err := shell.Split(strings.TrimPrefix(failure.Suggestions[0], "jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, _, exit = runBinary(t, args...)
		if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "adf.convert", true), &out) != nil || !out.Lossy || len(out.Diagnostics) == 0 || adf.Validate(out.Document) != nil {
			t.Fatal("offered recovery did not preserve an inspectable best-effort candidate")
		}
		for _, option := range []string{"--adf-best-effort", "--adf-strict=false"} {
			stdout, _, exit = runBinary(t, "adf", "convert", "--input", input, "--output="+mode, option)
			if exit != 0 || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out) != nil || !out.Lossy || len(out.Diagnostics) == 0 {
				t.Fatal("explicit loss acceptance lost compact-visible diagnostics")
			}
		}
	}
}

func TestADFRenderPreservesFutureInputAndReportsLoss(t *testing.T) {
	t.Parallel()
	const raw = `{"type":"doc","version":1,"future":"<>&\u2028","content":[{"type":"paragraph","content":[{"type":"text","text":"visible","marks":[{"type":"textColor","attrs":{"color":"#aa55cc"}}]}]},{"type":"newNode","content":[{"type":"paragraph","content":[{"type":"text","text":"nested content"}]}]}]}`
	input := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(input, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinary(t, "adf", "render", "--input", input, "--output="+mode)
		var out envelope.ADFRenderOutput
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &out) != nil || !out.Lossy {
			t.Fatal("future ADF could not be reviewed")
		}
		if !slices.Equal(out.LossyConstructs, []string{"doc", "newNode", "textColor"}) || !strings.Contains(out.Markdown, "visible") || !strings.Contains(out.Markdown, "nested content") {
			t.Fatal("projection hid future content or its approximation")
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || string(after) != raw {
		t.Fatal("review changed its original file")
	}
}

func TestADFInputRefusalsAndHumanSanitization(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"convert", "render"} {
		for _, input := range []string{"", "   \n", string([]byte{0xff})} {
			stdout, _, exit := runBinaryEnvInputStarted(t, nil, input, nil, "adf", route, "--output=json")
			var f frame
			var failure envelope.Error
			if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "flag_value_invalid" || failure.Field != "input" {
				t.Fatal("empty or invalid text did not fail as caller input")
			}
		}
	}
	for _, wrapper := range []string{`{"body":{"type":"doc","version":1,"content":[]}}`, `null`, `[]`} {
		_, _, exit := runBinaryEnvInputStarted(t, nil, wrapper, nil, "adf", "render", "--output=json")
		if exit != 3 {
			t.Fatal("render guessed an ADF wrapper")
		}
	}
	const hostile = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"visible\u001b[31mRED\u0007"}]}]}`
	stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, hostile, nil, "adf", "render", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "visible") || strings.ContainsAny(stdout, "\x1b\x07") {
		t.Fatal("human ADF projection bypassed sanitization")
	}
	stdout, stderr, exit = runBinaryEnvInputStarted(t, nil, hostile, nil, "adf", "render", "--output=json")
	var out envelope.ADFRenderOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "adf.render", true), &out) != nil || !strings.Contains(out.Markdown, "\x1b") || !strings.Contains(out.Markdown, "\x07") {
		t.Fatal("human sanitization mutated the machine projection")
	}
}

func TestADFConversionAndProjectionPipe(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, "**Hello**", nil, "adf", "convert", "--jq=.data.document")
	if exit != 0 || stderr != "" || adf.Validate([]byte(stdout)) != nil {
		t.Fatal("conversion extraction did not produce submit-ready native ADF")
	}
	stdout, stderr, exit = runBinaryEnvInputStarted(t, nil, stdout, nil, "adf", "render", "--jq=.data.markdown")
	if exit != 0 || stderr != "" || stdout != "**Hello**\n\n" {
		t.Fatalf("local ADF pipe failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

// Execute the examples from actual help rather than a duplicate recipe list.
func TestADFPublishedHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	for name, content := range map[string]string{
		"notes.md":      "**Hello**",
		"document.json": `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Hello"}]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{"convert", "render"} {
		help, _, exit := runBinary(t, "adf", route, "--help", "--color=never")
		if exit != 0 {
			t.Fatal("ADF help failed")
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
			for i, argument := range args {
				if argument == "notes.md" || argument == "document.json" {
					args[i] = filepath.Join(directory, argument)
				}
			}
			stdout, stderr, exit := runBinary(t, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("published ADF example failed: %q exit=%d stderr=%q", line, exit, stderr)
			}
			if strings.Contains(line, "--jq") {
				if route == "convert" && adf.Validate([]byte(stdout)) != nil || route == "render" && stdout != "Hello\n\n" {
					t.Fatal("published extraction example returned the wrong artifact")
				}
			} else {
				_ = adfData(t, stdout, "json", "adf."+route, true)
			}
			count++
		}
		if count != 2 {
			t.Fatal("ADF help examples changed without execution coverage")
		}
	}
}
