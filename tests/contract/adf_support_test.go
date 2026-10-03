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

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestADFSupportRegistryBinaryModesAndTruth(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_READ_ONLY=true", "AGENT=1"}
	var first *envelope.ADFSupportOutput
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "agent", "adf-matrix", "--output="+mode)
		var out envelope.ADFSupportOutput
		payload := adfData(t, stdout, mode, "agent.adf-matrix", true)
		if exit != 0 || stderr != "" || json.Unmarshal(payload, &out) != nil || len(out.Entries) == 0 || out.SchemaSHA256 != adf.SchemaSHA256 || !out.Unknown.PreserveNative || out.Unknown.Validate || out.Unknown.Edit || out.Unknown.NodeProjection != "placeholder" || out.Unknown.MarkProjection != "dropped" {
			t.Fatal("local ADF discovery lost its typed implementation contract")
		}
		if first == nil {
			first = &out
		} else if !reflect.DeepEqual(*first, out) {
			t.Fatal("machine modes returned different support facts")
		}
		var aligned, plain, panel, nativeColor bool
		for _, row := range out.Entries {
			if row.Capabilities.Edit {
				t.Fatal("preservation was reported as lossless editing")
			}
			switch row.Definition {
			case "paragraph_with_alignment_node":
				aligned = row.NodeBounds != nil && row.NodeBounds.Marks != nil && slices.Contains(row.NodeBounds.Marks.Allowed, "alignment")
			case "paragraph_with_no_marks_node":
				plain = row.NodeBounds != nil && row.NodeBounds.Marks != nil && row.NodeBounds.Marks.Max == 0
			case "panel_node":
				panel = row.Capabilities.Render && !row.TerminalRender && row.MarkdownProjection == "approximate" && !row.Capabilities.Author
			case "textColor_mark":
				nativeColor = row.Capabilities.Preserve && row.Capabilities.Validate && row.Capabilities.Submit && !row.Capabilities.Author && !row.Capabilities.Render && row.MarkdownProjection == "dropped"
			}
		}
		if !aligned || !plain || !panel || !nativeColor {
			t.Fatal("matrix hid context differences or overstated actual rendering/authoring")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("local support discovery contacted Jira")
	}
	stdout, stderr, exit := runBinary(t, "agent", "adf-matrix", "--jq", `.data.entries[] | select(.definition=="panel_node") | .terminal_render`)
	if exit != 0 || stderr != "" || stdout != "false\n" {
		t.Fatal("support-matrix jq extraction changed a false capability")
	}
	stdout, stderr, exit = runBinary(t, "agent", "adf-matrix", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Local ADF support") || !strings.Contains(stdout, "panel_node") || !strings.Contains(stdout, "lossless editing") {
		t.Fatal("human discovery lost capability rows or their limitations")
	}
}

func TestADFSupportPublishedHelpExamplesAndRefusals(t *testing.T) {
	t.Parallel()
	help, _, exit := runBinary(t, "agent", "adf-matrix", "--help", "--color=never")
	if exit != 0 {
		t.Fatal("matrix help failed")
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
		stdout, stderr, exit := runBinary(t, args...)
		if exit != 0 || stderr != "" {
			t.Fatalf("published support example failed: %q", line)
		}
		if strings.Contains(line, "--jq") {
			if !strings.Contains(stdout, "paragraph_with_no_marks_node") || strings.Contains(stdout, "panel_node") {
				t.Fatal("help extraction misrepresented Markdown authorship")
			}
		} else {
			_ = adfData(t, stdout, "json", "agent.adf-matrix", true)
		}
		count++
	}
	if count != 2 {
		t.Fatal("published support examples changed without execution coverage")
	}
	stdout, _, exit := runBinary(t, "agent", "adf-matrix", "unexpected", "--output=compact")
	var f frame
	var failure envelope.Error
	if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || f.Meta.Command != "agent.adf-matrix" || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "arg_count_invalid" {
		t.Fatal("matrix accepted an unsupported operand or lost its failure frame")
	}
	stdout, _, exit = runBinary(t, "agent", "schema", "--path=agent adf-matrix", "--output=json")
	if exit != 0 || !strings.Contains(stdout, "schema_sha256") || !strings.Contains(stdout, "markdown_projection") || !strings.Contains(stdout, "node_bounds") {
		t.Fatal("runtime discovery lost the matrix output shape")
	}
}
