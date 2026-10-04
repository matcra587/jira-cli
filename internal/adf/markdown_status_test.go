package adf_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestFromMarkdownStatusAuthoring(t *testing.T) {
	for _, tc := range []struct{ name, source, text, color string }{
		{name: "default neutral", source: `:status[READY]`, text: "READY", color: "neutral"},
		{name: "empty attributes", source: `:status[READY]{}`, text: "READY", color: "neutral"},
		{name: "neutral", source: `:status[READY]{color=neutral}`, text: "READY", color: "neutral"},
		{name: "purple", source: `:status[READY]{color=purple}`, text: "READY", color: "purple"},
		{name: "blue", source: `:status[READY]{color=blue}`, text: "READY", color: "blue"},
		{name: "red", source: `:status[READY]{color=red}`, text: "READY", color: "red"},
		{name: "yellow", source: `:status[READY]{color=yellow}`, text: "READY", color: "yellow"},
		{name: "green", source: `:status[READY]{color=green}`, text: "READY", color: "green"},
		{name: "hex", source: `:status[READY]{color=#12abEF}`, text: "READY", color: "#12abEF"},
		{name: "double quotes", source: `:status[READY]{color="green"}`, text: "READY", color: "green"},
		{name: "single quotes", source: `:status[READY]{color='#12abEF'}`, text: "READY", color: "#12abEF"},
		{name: "attribute spacing", source: `:status[READY]{ color = green }`, text: "READY", color: "green"},
		{name: "decoded attributes", source: `:status[READY]{color="&#103;reen"}`, text: "READY", color: "green"},
		{name: "escaped label", source: `:status[\*Ready\* \[label\] &amp; &amp;lt; &#00065; 雪]{color=blue}`, text: "*Ready* [label] & &lt; A 雪", color: "blue"},
		{name: "nested brackets", source: `:status[Ready [now]]{color=green}`, text: "Ready [now]", color: "green"},
		{name: "punctuation boundary", source: `(:status[READY]{color=green})`, text: "READY", color: "green"},
		{name: "surrounding text", source: `before :status[READY]{color=green} after`, text: "READY", color: "green"},
		{name: "label edge whitespace", source: `:status[ Ready ]{color=green}`, text: " Ready ", color: "green"},
		{name: "leading hash", source: `:status[# Ready]{color=green}`, text: "# Ready", color: "green"},
		{name: "plain line break", source: ":status[Ready\nnow]{color=green}", text: "Ready now", color: "green"},
		{name: "unmatched backtick", source: ":status[Ready `literal]{color=green}", text: "Ready `literal", color: "green"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			statuses := statusNodes(doc)
			if len(statuses) != 1 {
				t.Fatalf("authored %d status nodes, want one", len(statuses))
			}
			status, err := adf.AsStatus(&statuses[0])
			if err != nil || status.Attrs.Text != tc.text || status.Attrs.Color != tc.color {
				t.Fatalf("status = %#v, %v; want text %q color %q", status, err, tc.text, tc.color)
			}
			if len(statuses[0].Marks()) != 0 || len(statuses[0].Content()) != 0 || status.Attrs.LocalID != "" || status.Attrs.Style != "" {
				t.Fatal("status authoring invented native state")
			}
			if tc.name == "surrounding text" && statusPlainText(doc) != "before  after" {
				t.Fatalf("surrounding text changed: %q", statusPlainText(doc))
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownStatusLiteralBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{name: "escaped colon", source: `\:status[READY]{color=green}`, want: `:status[READY]{color=green}`},
		{name: "inline code", source: "`:status[READY]{color=green}`", want: `:status[READY]{color=green}`},
		{name: "fenced code", source: "```text\n:status[READY]{color=green}\n```", want: `:status[READY]{color=green}`},
		{name: "indented code", source: "    :status[READY]{color=green}", want: `:status[READY]{color=green}`},
		{name: "other name", source: `:state[READY]{color=green}`, want: `:state[READY]{color=green}`},
		{name: "leaf directive", source: `::status[READY]{color=green}`, want: `::status[READY]{color=green}`},
		{name: "container directive", source: `:::status[READY]{color=green}`, want: `:::status[READY]{color=green}`},
		{name: "unclosed label", source: `:status[READY`, want: `:status[READY`},
		{name: "unclosed attributes", source: `:status[READY]{color=green`, want: `:status[READY]{color=green`},
		{name: "unclosed quote", source: `:status[READY]{color="green}`, want: `:status[READY]{color="green}`},
		{name: "URL token", source: `https://example.test/:status[READY]{color=green}`, want: `https://example.test/:status[READY]{color=green}`},
		{name: "URL query", source: `https://example.test/?value=:status[READY]{color=green}`, want: `https://example.test/?value=:status[READY]{color=green}`},
		{name: "opaque URL", source: `mailto:person@example.test?subject=:status[READY]{color=green}`, want: `mailto:person@example.test?subject=:status[READY]{color=green}`},
		{name: "URN", source: `urn:example?value=:status[READY]{color=green}`, want: `urn:example?value=:status[READY]{color=green}`},
		{name: "protocol relative URL", source: `//example.test/?value=:status[READY]{color=green}`, want: `//example.test/?value=:status[READY]{color=green}`},
		{name: "wrapped opaque URL", source: `(mailto:person@example.test?subject=:status[READY]{color=green})`, want: `(mailto:person@example.test?subject=:status[READY]{color=green})`},
		{name: "link destination", source: `[open](https://example.test/:status[READY])`, want: "open"},
		{name: "emoji spelling", source: `:status:`, want: `:status:`},
		{name: "invalid attribute name", source: `:status[READY]{-flag=green}`, want: `:status[READY]{-flag=green}`},
		{name: "invalid unquoted value", source: `:status[READY]{color=green>}`, want: `:status[READY]{color=green>}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			if len(statusNodes(doc)) != 0 {
				t.Fatal("literal context authored a status")
			}
			if statusPlainText(doc) != tc.want {
				t.Fatalf("literal content = %q, want %q", statusPlainText(doc), tc.want)
			}
			mdValidate(t, doc)
		})
	}
}

func TestStatusLiteralNativePanelRoundTrip(t *testing.T) {
	for _, literal := range []string{":status", ":status[READY]{color=green}", `\:status[READY]`, "before :status after"} {
		t.Run(literal, func(t *testing.T) {
			encoded, err := json.Marshal(literal)
			if err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":` + string(encoded) + `}]}]}]}`)
			doc, err := adf.ParseDocument(original)
			if err != nil {
				t.Fatal(err)
			}
			projection := adf.ToMarkdownLossy(doc)
			if len(projection.LossyConstructs) != 0 {
				t.Fatalf("literal text lost panel representability: %v", projection.LossyConstructs)
			}
			converted, warnings := mdConvert(t, projection.Markdown)
			mdAssertNoWarnings(t, warnings)
			raw, err := converted.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			equal, err := adf.StructuralEqual(original, raw)
			if err != nil || !equal {
				t.Fatalf("projection changed native literal text: %s, %v", raw, err)
			}
			session, err := adf.PrepareEdit(doc)
			if err != nil || len(session.Regions()) != 1 || session.Regions()[0].Protected {
				t.Fatalf("representable native literal became protected: %v", err)
			}
			unchanged, err := session.Apply(session.Markdown())
			if err != nil {
				t.Fatal(err)
			}
			raw, err = unchanged.Marshal()
			if err != nil || string(raw) != string(original) {
				t.Fatal("unchanged native literal bytes changed")
			}
		})
	}
}

func TestFromMarkdownStatusRefusesUnrepresentableState(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{name: "invalid named color", source: `:status[READY]{color=orange}`},
		{name: "short hex", source: `:status[READY]{color=#abc}`},
		{name: "empty label", source: `:status[]{color=green}`},
		{name: "bare directive", source: `:status`},
		{name: "unknown attribute", source: `:status[READY]{color=green title=secret}`},
		{name: "unknown valid-looking attribute", source: `:status[READY]{title=green}`},
		{name: "id shorthand", source: `:status[READY]{#opaque color=green}`},
		{name: "class shorthand", source: `:status[READY]{.wide color=green}`},
		{name: "boolean attribute", source: `:status[READY]{color=green flag}`},
		{name: "leading boolean attribute", source: `:status[READY]{flag color=green}`},
		{name: "duplicate color", source: `:status[READY]{color=red color=green}`},
		{name: "formatted label", source: `:status[**READY**]{color=green}`},
		{name: "code label", source: ":status[`READY`]{color=green}"},
		{name: "linked label", source: `:status[[READY](https://example.test)]{color=green}`},
		{name: "inherited strong", source: `**:status[READY]{color=green}**`},
		{name: "inherited link", source: `[:status[READY]{color=green}](https://example.test)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, "first\n\n"+tc.source)
			if len(warnings) != 1 || !warnings[0].Lossy || warnings[0].NodeType != "status directive" || !strings.HasPrefix(warnings[0].Path, "line 3, col ") {
				t.Fatalf("unsupported state lacks one source-mapped lossy warning: %#v", warnings)
			}
			if len(statusNodes(doc)) != 0 || !strings.Contains(statusPlainText(doc), ":status") {
				t.Fatal("unsupported candidate erased syntax or emitted invalid status")
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownStatusSchemaContexts(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{name: "heading", source: "# :status[READY]{color=green}"},
		{name: "list", source: "- :status[READY]{color=green}"},
		{name: "task", source: "- [ ] :status[READY]{color=green}"},
		{name: "quote", source: "> :status[READY]{color=green}"},
		{name: "panel", source: "> [!NOTE]\n> :status[READY]{color=green}"},
		{name: "table", source: "| H |\n| - |\n| :status[READY]{color=green} |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			if len(statusNodes(doc)) != 1 {
				t.Fatal("context did not author one native status")
			}
			mdValidate(t, doc)
		})
	}
}

func TestStatusAuthoringKeepsNativeRegionsProtected(t *testing.T) {
	original := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":"opaque","future":true}}]}]}`)
	doc, err := adf.ParseDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil || len(session.Regions()) != 1 || !session.Regions()[0].Protected {
		t.Fatalf("native status state became editable: %v", err)
	}
	unchanged, err := session.Apply(session.Markdown())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := unchanged.Marshal()
	if err != nil || string(raw) != string(original) {
		t.Fatal("unchanged native status bytes changed")
	}
}

func statusNodes(doc *adf.Document) []adf.Node {
	var statuses []adf.Node
	var walk func(adf.Node)
	walk = func(n adf.Node) {
		if n.Type() == "status" {
			statuses = append(statuses, n)
		}
		for _, child := range n.Content() {
			walk(child)
		}
	}
	walk(doc.Node)
	return statuses
}

func statusPlainText(doc *adf.Document) string {
	var out strings.Builder
	var walk func(adf.Node)
	walk = func(n adf.Node) {
		out.WriteString(n.Text())
		for _, child := range n.Content() {
			walk(child)
		}
	}
	walk(doc.Node)
	return out.String()
}
