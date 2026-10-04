package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

const richADFDocument = `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Review required"},{"type":"hardBreak"},{"type":"status","attrs":{"text":"Ready","color":"#aa55cc"}}]}]},{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Name"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Value"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"東京"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"abcdefghijklmno"}]}]}]}]}]}`

func TestADFRichHumanRenderingKeepsClosedMachineContract(t *testing.T) {
	t.Parallel()
	doc, err := adf.ParseDocument([]byte(richADFDocument))
	if err != nil || adf.Validate([]byte(richADFDocument)) != nil {
		t.Fatal("invalid independently authored native fixture", err)
	}
	input := filepath.Join(t.TempDir(), "rich.json")
	if err := os.WriteFile(input, []byte(richADFDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinary(t, "adf", "render", "--input", input, "--output=human", "--color=never")
	if exit != 0 || stderr != "" || strings.ContainsAny(stdout, "\x1b\a") {
		t.Fatalf("rich human failed: exit=%d output=%q stderr=%q", exit, stdout, stderr)
	}
	for _, want := range []string{"Warning", "Review required", "Ready", "Name", "Value", "東京", "abcdefghijklmno", "│"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human lost %q: %q", want, stdout)
		}
	}
	expected := adf.ToMarkdownLossy(doc)
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit = runBinary(t, "adf", "render", "--input", input, "--output="+mode, "--color=always")
		var out map[string]json.RawMessage
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &out) != nil || len(out) != 3 {
			t.Fatalf("machine render leaked human/native fields: %q stderr=%q", stdout, stderr)
		}
		var markdown string
		// The known status and its complete panel now survive conversion;
		// the table remains an explicit approximation.
		if json.Unmarshal(out["markdown"], &markdown) != nil || markdown != expected.Markdown || string(out["lossy"]) != "true" || string(out["lossy_constructs"]) != `["table"]` {
			t.Fatal("rich rendering changed machine projection", out)
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || string(after) != richADFDocument {
		t.Fatal("render rewrote native input")
	}
}

func TestIssueDescriptionsAndCommentsShareRichADFRenderer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/issue/ABC-1":
			_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":{"summary":"Native rendering","description":` + richADFDocument + `}}`))
		case "/rest/api/3/issue/ABC-1/comment":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":1,"comments":[{"id":"10002","body":` + richADFDocument + `}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"issue", "view", "ABC-1", "--comments=0"}, {"issue", "comment", "list", "ABC-1"}} {
		args = append(args, "--output=human", "--color=never")
		stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), args...)
		if exit != 0 {
			t.Fatalf("human read failed: %q", stdout)
		}
		for _, want := range []string{"Warning", "Review required", "Ready", "東京", "abcdefghijklmno", "│"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("shared route %v lost %q: %q", args, want, stdout)
			}
		}
	}
}

func TestADFRichUnsupportedRecoveryNamesNativeInput(t *testing.T) {
	t.Parallel()
	const raw = `{"type":"doc","version":1,"content":[{"type":"futureNode","content":[{"type":"paragraph","content":[{"type":"text","text":"future child"}]}]}]}`
	stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, raw, nil, "adf", "render", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "futureNode — see the original native input") || strings.Contains(stdout, "see --output=json") {
		t.Fatalf("local unsupported recovery points to lossy machine artifact: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, _, exit = runBinaryEnvInputStarted(t, nil, raw, nil, "adf", "render", "--output=json")
	if exit != 0 || !strings.Contains(stdout, "future child") {
		t.Fatal("machine projection lost its documented unknown child text")
	}
}

func TestIssueNativeReadDoesNotNormalizeCellAttrs(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/ABC-1" {
			t.Error("unexpected issue read", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":{"description":` + richADFDocument + `}}`))
	}))
	defer server.Close()
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "view", "ABC-1", "--comments=0", "--output=json")
	var frame struct {
		Data struct {
			Issue struct {
				Fields struct{ Description json.RawMessage }
			}
		}
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil {
		t.Fatal("native issue read failed")
	}
	equal, err := adf.StructuralEqual([]byte(richADFDocument), frame.Data.Issue.Fields.Description)
	if err != nil || !equal {
		t.Fatal("CLI added, removed or changed native cell attrs")
	}
}
