package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestADFPanelProjectionBinaryRoundTripAndLocalBoundary(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "AGENT=1", "JIRA_READ_ONLY=true"}
	for _, tc := range []struct{ kind, marker string }{{kind: "info", marker: "NOTE"}, {kind: "tip", marker: "TIP"}, {kind: "note", marker: "IMPORTANT"}, {kind: "warning", marker: "WARNING"}, {kind: "error", marker: "CAUTION"}} {
		t.Run(tc.kind, func(t *testing.T) {
			raw := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"` + tc.kind + `"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}]}`
			input := filepath.Join(t.TempDir(), "panel.json")
			if err := os.WriteFile(input, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"json", "compact"} {
				stdout, stderr, exit := runBinaryEnv(t, env, "adf", "render", "--input", input, "--output="+mode)
				var fields map[string]json.RawMessage
				if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &fields) != nil || len(fields) != 3 {
					t.Fatal("panel broadened machine contract", exit, stdout, stderr)
				}
				var markdown string
				if json.Unmarshal(fields["markdown"], &markdown) != nil || markdown != "> [!"+tc.marker+"]\n>\n> native\n" || string(fields["lossy"]) != "false" || string(fields["lossy_constructs"]) != "[]" {
					t.Fatal("representable panel projection incorrect", stdout)
				}
				stdout, stderr, exit = runBinaryEnvInputStarted(t, env, markdown, nil, "adf", "convert", "--output="+mode)
				var converted envelope.ADFConvertOutput
				if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &converted) != nil || converted.Lossy || len(converted.Diagnostics) != 0 {
					t.Fatal("strict inverse conversion failed", exit, stdout, stderr)
				}
				equal, err := adf.StructuralEqual([]byte(raw), converted.Document)
				if err != nil || !equal {
					t.Fatal("actual binary panel conversion changed structure", err)
				}
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "adf", "render", "--input", input, "--output=human", "--color=never")
			if exit != 0 || stderr != "" || !strings.Contains(stdout, "native") || strings.Contains(stdout, "Markdown losses") {
				t.Fatal("human panel rendering incorrectly reports loss", exit, stdout, stderr)
			}
			after, err := os.ReadFile(input)
			if err != nil || string(after) != raw {
				t.Fatal("projection rewrote native source")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("local panel conversion contacted Jira")
	}
}

func TestADFPanelProjectionBinaryOpaqueStateAndDiscovery(t *testing.T) {
	t.Parallel()
	raw := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning","localId":"keep"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}]}`
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, raw, nil, "adf", "render", "--output="+mode)
		var fields map[string]json.RawMessage
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &fields) != nil || len(fields) != 3 || string(fields["lossy"]) != "true" || string(fields["lossy_constructs"]) != `["panel"]` {
			t.Fatal("opaque panel loss disappeared", exit, stdout, stderr)
		}
	}
	stdout, stderr, exit := runBinary(t, "adf", "render", "--help")
	if exit != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "structural round-trip comparison") {
		t.Fatal("help does not explain conditional panel projection", stdout)
	}
	stdout, stderr, exit = runBinary(t, "agent", "guide", "safe-mutation")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Native IDs, extra attributes") {
		t.Fatal("guide lost native panel protection", stdout)
	}
	stdout, stderr, exit = runBinary(t, "adf", "explain-node", "panel", "--output=json")
	var explain envelope.ADFExplainOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "adf.explain-node", true), &explain) != nil || len(explain.Entries) == 0 {
		t.Fatal("panel discovery failed", stdout)
	}
	for _, row := range explain.Entries {
		if !row.Capabilities.Author || !row.Capabilities.Render || row.Capabilities.Edit {
			t.Fatal("projection overstated editor acceptance", row)
		}
	}
}
