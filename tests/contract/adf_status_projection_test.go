package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestADFStatusProjectionBinaryRoundTrip(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "AGENT=1", "JIRA_READ_ONLY=true"}
	for _, tc := range []struct{ name, raw string }{
		{name: "identity and style", raw: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"https://example.test/"},{"type":"status","attrs":{"text":"READY &amp; 雪","color":"#12abEF","localId":"owned-id","style":"bold"}},{"type":"text","text":"after"}]}]}`},
		{name: "empty optionals", raw: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"green","localId":"","style":""}}]}]}`},
		{name: "status panel", raw: `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"blue","localId":"owned-id","style":""}}]}]}]}`},
	} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				stdout, stderr, exit := runBinaryEnvInputStarted(t, env, tc.raw, nil, "adf", "render", "--output="+mode)
				var fields map[string]json.RawMessage
				if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &fields) != nil || len(fields) != 3 {
					t.Fatal("status projection changed machine contract", exit, stderr)
				}
				var projection envelope.ADFRenderOutput
				if json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &projection) != nil || projection.Lossy || len(projection.LossyConstructs) != 0 || !strings.Contains(projection.Markdown, ":status[") {
					t.Fatal("proven native status lost its directive inverse")
				}
				stdout, stderr, exit = runBinaryEnvInputStarted(t, env, projection.Markdown, nil, "adf", "convert", "--output="+mode)
				var converted envelope.ADFConvertOutput
				if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &converted) != nil || converted.Lossy || len(converted.Diagnostics) != 0 {
					t.Fatal("strict status inverse failed", exit, stderr)
				}
				equal, err := adf.StructuralEqual([]byte(tc.raw), converted.Document)
				if err != nil || !equal || adf.Validate(converted.Document) != nil {
					t.Fatal("actual binary lost independently authored status fields", err)
				}
				_, stderr, exit = runBinaryEnvInputStarted(t, env, string(converted.Document), nil, "adf", "validate", "--output="+mode)
				if exit != 0 || stderr != "" {
					t.Fatal("actual binary refused its proven status document", exit, stderr)
				}
			})
		}
		t.Run(tc.name+"/human", func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, tc.raw, nil, "adf", "render", "--output=human", "--color=never")
			if exit != 0 || stderr != "" || !strings.Contains(stdout, "READY") || strings.Contains(stdout, "Markdown losses") {
				t.Fatal("human status rendering changed or reports disproven loss", exit, stderr)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("local status projection contacted Jira")
	}
}

func TestADFStatusProjectionBinaryOpaqueStateAndDiscovery(t *testing.T) {
	t.Parallel()
	const raw = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":"bold","future":true}}]}]}`
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinaryEnvInputStarted(t, nil, raw, nil, "adf", "render", "--output="+mode)
		var projection envelope.ADFRenderOutput
		if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "adf.render", true), &projection) != nil || !projection.Lossy || !slices.Contains(projection.LossyConstructs, "status") || !slices.Contains(projection.LossyConstructs, "paragraph") {
			t.Fatal("unknown native status state became silently editable", exit, stderr)
		}
		for _, source := range []string{`:status[READY]{localId}`, `:status[READY]{style}`, `:status[READY]{localId=one localId=two}`, `:status[READY]{style=one style=two}`} {
			stdout, _, exit = runBinaryEnvInputStarted(t, nil, source, nil, "adf", "convert", "--output="+mode)
			var candidate envelope.ADFConvertOutput
			if exit != 3 || json.Unmarshal(adfData(t, stdout, mode, "adf.convert", false), &candidate) != nil || !candidate.Lossy || len(candidate.Diagnostics) != 1 || adf.Validate(candidate.Document) != nil {
				t.Fatal("strict conversion accepted invalid status identity attributes", exit)
			}
		}
	}
	for _, args := range [][]string{{"adf", "convert", "--help"}, {"adf", "render", "--help"}, {"agent", "guide", "machine-output"}} {
		stdout, stderr, exit := runBinary(t, args...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "localId") || !strings.Contains(stdout, "style") {
			t.Fatal("status field preservation is missing from discovery", args, exit)
		}
	}
	stdout, stderr, exit := runBinary(t, "adf", "explain-node", "status", "--output=json")
	var explain envelope.ADFExplainOutput
	if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "adf.explain-node", true), &explain) != nil || len(explain.Entries) == 0 {
		t.Fatal("status discovery failed", exit)
	}
	for _, row := range explain.Entries {
		if !row.Capabilities.Author || !row.Capabilities.Render || row.Capabilities.Edit {
			t.Fatal("status projection overstated issue editor acceptance", row)
		}
	}
}
