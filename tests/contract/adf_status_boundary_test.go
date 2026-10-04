package contract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestADFStatusBinaryInlineBoundaries(t *testing.T) {
	t.Parallel()
	const ready = `{"type":"status","attrs":{"text":"READY","color":"green"}}`
	for _, tc := range []struct{ name, source, preceding string }{
		{name: "link", source: `[open](https://example.test):status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "strong-link", source: `**[open](https://example.test)**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"strong"},{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "link-then-strong", source: `[open](https://example.test)**!**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]},{"type":"text","text":"!","marks":[{"type":"strong"}]}`},
		{name: "code", source: "`https://example.test`:status[READY]{color=green}", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"code"}]}`},
		{name: "autolink", source: `<https://example.test>:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "status", source: `:status[https://example.test]{color=blue}:status[READY]{color=green}`, preceding: `{"type":"status","attrs":{"text":"https://example.test","color":"blue"}}`},
		{name: "strong", source: `**https://example.test**:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strong"}]}`},
		{name: "strike", source: `~~https://example.test~~:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strike"}]}`},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, tc.source, nil, "adf", "convert", "--output="+mode)
				if exit != 0 || stderr != "" {
					t.Fatalf("inline boundary conversion: exit %d, stderr %q", exit, stderr)
				}
				var candidate []byte
				if mode == "human" {
					start := strings.IndexByte(stdout, '{')
					if start < 0 {
						t.Fatal("human conversion omitted native candidate")
					}
					candidate = []byte(stdout[start:])
				} else {
					var out envelope.ADFConvertOutput
					if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out); err != nil {
						t.Fatal(err)
					}
					if out.Lossy || len(out.Diagnostics) != 0 {
						t.Fatal("completed inline owner made following status lossy")
					}
					candidate = out.Document
				}
				want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + tc.preceding + `,` + ready + `]}]}`)
				equal, err := adf.StructuralEqual(want, candidate)
				if err != nil || !equal || adf.Validate(candidate) != nil {
					t.Fatalf("binary changed inline owner or following status: %s, %v", candidate, err)
				}
			})
		}
	}
}

func TestADFStatusBinaryFormattedURLLiteral(t *testing.T) {
	t.Parallel()
	const literal = `mailto:person@example.test?subject=:status[READY]{color=green}`
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_READ_ONLY=true", "AGENT=1"}, "**"+literal+"**", nil, "adf", "convert", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("formatted URL conversion: exit %d, stderr %q", exit, stderr)
			}
			var out envelope.ADFConvertOutput
			if err := json.Unmarshal(adfData(t, stdout, mode, "adf.convert", true), &out); err != nil {
				t.Fatal(err)
			}
			doc, err := adf.ParseDocument(out.Document)
			if err != nil || doc.Validate() != nil || out.Lossy || len(out.Diagnostics) != 0 {
				t.Fatal("formatted URL became invalid or lossy")
			}
			blocks := doc.Content()
			if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
				t.Fatal("formatted URL changed block")
			}
			var text strings.Builder
			for _, child := range blocks[0].Content() {
				if child.Type() != "text" || len(child.Marks()) != 1 || child.Marks()[0].Type() != "strong" {
					t.Fatal("URL authored status or changed formatting")
				}
				text.WriteString(child.Text())
			}
			if text.String() != literal {
				t.Fatal("formatted URL changed text")
			}
		})
	}
}
