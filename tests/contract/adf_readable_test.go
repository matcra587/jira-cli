package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

const readableADFDocument = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"account-test","text":"@Alex"}},{"type":"text","text":" "},{"type":"emoji","attrs":{"shortName":":smile:","text":"🙂"}}]},{"type":"expand","attrs":{"title":"Details"},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"nestedExpand","attrs":{"title":"More"},"content":[{"type":"paragraph","content":[{"type":"text","text":"omega"}]}]}]},{"type":"layoutSection","content":[{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]},{"type":"taskList","attrs":{"localId":"tasks"},"content":[{"type":"taskItem","attrs":{"localId":"todo","state":"TODO"},"content":[{"type":"text","text":"pending"}]},{"type":"taskItem","attrs":{"localId":"done","state":"DONE"},"content":[{"type":"text","text":"completed"}]}]},{"type":"decisionList","attrs":{"localId":"decisions"},"content":[{"type":"decisionItem","attrs":{"localId":"choice","state":"DECIDED"},"content":[{"type":"text","text":"chosen"}]}]}]}`

func requireReadableHuman(t *testing.T, stdout string) {
	t.Helper()
	for _, want := range []string{"@Alex", "🙂", "Details", "alpha", "More", "omega", "Column 1 (50%)", "left", "Column 2 (50%)", "right", "[ ] pending", "[x] completed", "Decision (DECIDED)", "chosen"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human read lost %q: %q", want, stdout)
		}
	}
	if strings.ContainsAny(stdout, "\x1b\a") {
		t.Fatal("plain human read leaked controls")
	}
}

func TestADFReadableNativeHumanAndClosedMachine(t *testing.T) {
	t.Parallel()
	input := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(input, []byte(readableADFDocument), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinary(t, "adf", "render", "--input", input, "--output=human", "--color=never")
	if exit != 0 || stderr != "" {
		t.Fatalf("local native read failed: exit=%d stderr=%q", exit, stderr)
	}
	requireReadableHuman(t, stdout)
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit = runBinary(t, "adf", "render", "--input", input, "--output="+mode, "--color=always")
		var out map[string]json.RawMessage
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &out) != nil || len(out) != 3 {
			t.Fatal("native human rendering broadened machine fields")
		}
		var markdown string
		if json.Unmarshal(out["markdown"], &markdown) != nil || markdown != readableMachineMarkdown || string(out["lossy"]) != "true" || string(out["lossy_constructs"]) != readableMachineLosses {
			t.Fatalf("native human rendering changed baseline machine projection: %q", stdout)
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || string(after) != readableADFDocument {
		t.Fatal("human read rewrote native input")
	}
}

func TestADFReadableIssueAndCommentShareReadOnlyRenderer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("human rendering attempted a write", r.Method)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/issue/ABC-1":
			_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":{"summary":"Native text","description":` + readableADFDocument + `}}`))
		case "/rest/api/3/issue/ABC-1/comment":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":1,"comments":[{"id":"10002","body":` + readableADFDocument + `}]}`))
		default:
			t.Error("unexpected native read", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"issue", "view", "ABC-1", "--comments=0"}, {"issue", "comment", "list", "ABC-1"}} {
		stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), append(args, "--output=human", "--color=never")...)
		if exit != 0 {
			t.Fatal("native human route failed", stdout)
		}
		requireReadableHuman(t, stdout)
	}
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "view", "ABC-1", "--comments=0", "--output=json")
	var frame struct {
		Data struct {
			Issue struct {
				Fields struct{ Description json.RawMessage }
			}
		}
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || string(frame.Data.Issue.Fields.Description) != readableADFDocument {
		t.Fatal("native machine read altered document bytes")
	}
}

func TestADFReadableCatalogReportsHumanOnlyCapabilities(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "agent", "adf-matrix", "--output=json")
	var out envelope.ADFSupportOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "agent.adf-matrix", true), &out) != nil {
		t.Fatal("catalog read failed")
	}
	wanted := map[string]bool{"mention": false, "emoji": false, "expand": false, "nestedExpand": false, "layoutSection": false, "layoutColumn": false, "taskList": false, "taskItem": false, "blockTaskItem": false, "decisionList": false, "decisionItem": false}
	for _, row := range out.Entries {
		if _, ok := wanted[row.Name]; row.Kind == "node" && ok {
			if !row.TerminalRender || row.Capabilities.Edit {
				t.Fatalf("human capability inaccurate: %s", row.Definition)
			}
			wanted[row.Name] = true
			if (row.Name == "expand" || row.Name == "nestedExpand" || row.Name == "layoutSection" || row.Name == "layoutColumn") && (row.Capabilities.Author || row.Capabilities.Render || row.MarkdownProjection != "placeholder") {
				t.Fatalf("human rendering overstated Markdown support: %s", row.Definition)
			}
		}
	}
	for name, seen := range wanted {
		if !seen {
			t.Error("missing native human capability", name)
		}
	}
}

const (
	readableMachineMarkdown = "@Alex 🙂\n\n\\[expand\\]alpha\n\n\\[nestedExpand\\]omega\n\n\\[layoutSection\\]\\[layoutColumn\\]left\n\n\\[layoutColumn\\]right\n\n- [ ] pending\n- [x] completed\n\n- \\<\\> chosen\n"
	readableMachineLosses   = `["decisionItem","decisionList","emoji","expand","layoutColumn","layoutSection","mention","nestedExpand","taskItem","taskList","text"]`
)
