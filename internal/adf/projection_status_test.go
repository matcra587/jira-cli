package adf_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestFromMarkdownStatusIdentityAttributes(t *testing.T) {
	for _, tc := range []struct{ name, markdown, attrs string }{
		{name: "identity", markdown: `:status[READY]{localId=owned-id color=green}`, attrs: `{"text":"READY","color":"green","localId":"owned-id"}`},
		{name: "style", markdown: `:status[READY]{style=bold}`, attrs: `{"text":"READY","color":"neutral","style":"bold"}`},
		{name: "both", markdown: `:status[READY]{color=#12abEF localId="owned-id" style='bold'}`, attrs: `{"text":"READY","color":"#12abEF","localId":"owned-id","style":"bold"}`},
		{name: "empty identity", markdown: `:status[READY]{color=blue localId=""}`, attrs: `{"text":"READY","color":"blue","localId":""}`},
		{name: "empty style", markdown: `:status[READY]{style=''}`, attrs: `{"text":"READY","color":"neutral","style":""}`},
		{name: "empty both", markdown: `:status[READY]{localId="" style=""}`, attrs: `{"text":"READY","color":"neutral","localId":"","style":""}`},
		{name: "escaped values", markdown: `:status[READY]{color=green localId="&#34;\\_&#10;雪" style="&amp;amp; &#34;"}`, attrs: `{"text":"READY","color":"green","localId":"\"\\_\n雪","style":"&amp; \""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.markdown)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			statusAssertNativeDocument(t, doc, statusParagraph(tc.attrs, "", ""))
		})
	}
}

func TestFromMarkdownStatusIdentityRefusesInvalidAttributes(t *testing.T) {
	for _, attributes := range []string{
		`localId=one localId=two`, `style=one style=two`,
		`localId`, `style`, `color`, `localid=one`, `text=other`,
	} {
		t.Run(attributes, func(t *testing.T) {
			literal := ":status[READY]{" + attributes + "}"
			doc, warnings := mdConvert(t, literal)
			mdValidate(t, doc)
			if len(statusNodes(doc)) != 0 || len(warnings) != 1 || !warnings[0].Lossy || warnings[0].NodeType != "status directive" || statusPlainText(doc) != literal {
				t.Fatal("invalid status attributes lost their literal fallback and diagnostic", warnings)
			}
		})
	}
}

func TestStatusProjectionNativeRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, attrs string }{
		{name: "named", attrs: `{"text":"READY","color":"green"}`},
		{name: "hex", attrs: `{"text":"READY","color":"#12abEF"}`},
		{name: "identity", attrs: `{"text":"READY","color":"blue","localId":"owned-id"}`},
		{name: "style", attrs: `{"text":"READY","color":"purple","style":"bold"}`},
		{name: "both", attrs: `{"text":"READY","color":"neutral","localId":"owned-id","style":"bold"}`},
		{name: "empty optionals", attrs: `{"text":"READY","color":"red","localId":"","style":""}`},
		{name: "punctuation", attrs: `{"text":"*Ready* [雪] ` + "`" + ` &amp; &lt; :status[other]","color":"yellow","localId":"\\_\"{}[]","style":"&amp; \"quoted\""}`},
		{name: "edge spaces", attrs: `{"text":" Ready ","color":"green"}`},
		{name: "control whitespace", attrs: `{"text":"Ready\r\nnow\t雪","color":"green","localId":"id\r\nvalue\t","style":"style\nvalue"}`},
		{name: "url label", attrs: `{"text":"https://example.test/?q=雪","color":"green","localId":"https://example.test/id","style":"bold"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := statusParagraph(tc.attrs, "", "")
			doc, session := prepareEdit(t, raw)
			mdValidate(t, doc)
			projection := adf.ToMarkdownLossy(doc)
			if len(projection.LossyConstructs) != 0 || !strings.Contains(projection.Markdown, ":status[") {
				t.Fatal("native status fields have no lossless directive inverse", projection)
			}
			converted, warnings := mdConvert(t, projection.Markdown)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, converted)
			statusAssertNativeDocument(t, converted, raw)
			if len(session.Regions()) != 1 || session.Regions()[0].Protected {
				t.Fatal("proven status paragraph is protected")
			}
			unchanged, err := session.Apply(session.Markdown())
			if err != nil || !bytes.Equal(editBytes(t, unchanged), []byte(raw)) {
				t.Fatal("unchanged native status bytes changed", err)
			}
			if !bytes.Equal(editBytes(t, doc), []byte(raw)) {
				t.Fatal("native projection changed its source")
			}
		})
	}
}

func TestStatusProjectionNativeTextAdjacency(t *testing.T) {
	const attrs = `{"text":"READY","color":"green","localId":"owned-id","style":"bold"}`
	for _, before := range []string{
		"before", "123", "雪", ":", "/", "https://example.test/", "mailto:person@example.test?subject=", "//example.test/", `\:status[other]`, `&amp;`, "*[]{}<>`|~", "\\", "before ",
	} {
		t.Run(before, func(t *testing.T) {
			raw := statusParagraph(attrs, before, "after")
			doc, session := prepareEdit(t, raw)
			projection := adf.ToMarkdownLossy(doc)
			if len(projection.LossyConstructs) != 0 || session.Regions()[0].Protected {
				t.Fatal("native text/status adjacency became lossy", projection)
			}
			converted, warnings := mdConvert(t, projection.Markdown)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, converted)
			statusAssertNativeDocument(t, converted, raw)
		})
	}
}

func TestStatusProjectionCompletedInlineAdjacency(t *testing.T) {
	const status = `{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":"bold"}}`
	for _, tc := range []struct{ name, preceding string }{
		{name: "link", preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "code", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"code"}]}`},
		{name: "strong", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strong"}]}`},
		{name: "strike", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strike"}]}`},
		{name: "status", preceding: `{"type":"status","attrs":{"text":"https://example.test","color":"blue","localId":"preceding-id","style":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + tc.preceding + "," + status + `]}]}`
			doc, session := prepareEdit(t, raw)
			projection := adf.ToMarkdownLossy(doc)
			if len(projection.LossyConstructs) != 0 || session.Regions()[0].Protected {
				t.Fatal("completed inline node lost status inverse", projection)
			}
			converted, warnings := mdConvert(t, projection.Markdown)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, converted)
			statusAssertNativeDocument(t, converted, raw)
		})
	}
}

func TestStatusProjectionOpaqueStateStaysProtected(t *testing.T) {
	for _, tc := range []struct{ name, status string }{
		{name: "unknown attribute", status: `{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":"bold","future":true}}`},
		{name: "unknown field", status: `{"type":"status","attrs":{"text":"READY","color":"green"},"future":true}`},
		{name: "empty marks", status: `{"type":"status","attrs":{"text":"READY","color":"green"},"marks":[]}`},
		{name: "unsupported marks", status: `{"type":"status","attrs":{"text":"READY","color":"green"},"marks":[{"type":"strong"}]}`},
		{name: "empty children", status: `{"type":"status","attrs":{"text":"READY","color":"green"},"content":[]}`},
		{name: "null identity", status: `{"type":"status","attrs":{"text":"READY","color":"green","localId":null}}`},
		{name: "numeric identity", status: `{"type":"status","attrs":{"text":"READY","color":"green","localId":42}}`},
		{name: "invalid color", status: `{"type":"status","attrs":{"text":"READY","color":"orange"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + tc.status + `]}]}`
			doc, session := prepareEdit(t, raw)
			if !slices.Contains(adf.ToMarkdownLossy(doc).LossyConstructs, "status") || !session.Regions()[0].Protected {
				t.Fatal("unrepresentable native status lost its protection")
			}
			unchanged, err := session.Apply(session.Markdown())
			if err != nil || !bytes.Equal(editBytes(t, unchanged), []byte(raw)) {
				t.Fatal("unchanged opaque status bytes changed", err)
			}
		})
	}
}

func TestStatusProjectionEditsPreserveIdentityAndStyle(t *testing.T) {
	const attrs = `{"text":"READY","color":"green","localId":"owned-id","style":"bold"}`
	raw := statusParagraph(attrs, "before ", " after")
	_, session := prepareEdit(t, raw)
	if session.Regions()[0].Protected {
		t.Fatal("native status label/color cannot be edited")
	}
	edited := strings.NewReplacer("READY", "DONE", `color="green"`, `color="#12abEF"`).Replace(session.Markdown())
	changed, err := session.Apply(edited)
	if err != nil {
		t.Fatal(err)
	}
	mdValidate(t, changed)
	statusAssertNativeDocument(t, changed, statusParagraph(`{"text":"DONE","color":"#12abEF","localId":"owned-id","style":"bold"}`, "before ", " after"))
}

func TestStatusProjectionNativeMerge(t *testing.T) {
	const first = `{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":"green","localId":"first-id","style":"bold"}}]}`
	const second = `{"type":"paragraph","content":[{"type":"status","attrs":{"text":"OTHER","color":"blue","localId":"second-id","style":""}}]}`
	raw := `{"version":1,"type":"doc","content":[` + first + "," + second + `]}`
	_, session := prepareEdit(t, raw)
	for _, region := range session.Regions() {
		if region.Protected {
			t.Fatal("native status merge source is protected")
		}
	}
	local := strings.Replace(session.Markdown(), "READY", "LOCAL", 1)
	remote := mustParse(t, strings.Replace(raw, `"OTHER"`, `"REMOTE"`, 1))
	merged, err := session.Merge(local, remote)
	if err != nil {
		t.Fatal("disjoint status edits refused", err)
	}
	statusAssertNativeDocument(t, merged, strings.NewReplacer(`"READY"`, `"LOCAL"`, `"OTHER"`, `"REMOTE"`).Replace(raw))
	conflicting := mustParse(t, strings.Replace(raw, `"READY"`, `"REMOTE"`, 1))
	merged, err = session.Merge(local, conflicting)
	var conflict *adf.EditError
	if merged != nil || !errors.As(err, &conflict) || conflict.Kind != adf.EditMergeConflict {
		t.Fatal("concurrent status edits lost conflict refusal", err)
	}
}

func TestStatusProjectionPanelRoundTripAndEdit(t *testing.T) {
	const raw = `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"before "},{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":""}},{"type":"text","text":" after"}]}]}]}`
	doc, session := prepareEdit(t, raw)
	projection := adf.ToMarkdownLossy(doc)
	if len(projection.LossyConstructs) != 0 || session.Regions()[0].Protected {
		t.Fatal("known native status made its panel protected", projection)
	}
	converted, warnings := mdConvert(t, projection.Markdown)
	mdAssertNoWarnings(t, warnings)
	mdValidate(t, converted)
	statusAssertNativeDocument(t, converted, raw)
	changed, err := session.Apply(strings.Replace(session.Markdown(), "READY", "DONE", 1))
	if err != nil {
		t.Fatal(err)
	}
	mdValidate(t, changed)
	statusAssertNativeDocument(t, changed, strings.Replace(raw, `"READY"`, `"DONE"`, 1))
	unchanged, err := session.Apply(session.Markdown())
	if err != nil || !bytes.Equal(editBytes(t, unchanged), []byte(raw)) {
		t.Fatal("unchanged status panel bytes changed", err)
	}
}

func TestStatusProjectionStructuralEdits(t *testing.T) {
	const first = `{"type":"status","attrs":{"text":"FIRST","color":"green","localId":"first-id","style":"bold"}}`
	const second = `{"type":"status","attrs":{"text":"SECOND","color":"blue","localId":"second-id","style":""}}`
	const third = `{"type":"status","attrs":{"text":"THIRD","color":"red","localId":"third-id","style":"bold"}}`
	const firstMD = `:status[FIRST]{color=green localId=first-id style=bold}`
	const secondMD = `:status[SECOND]{color=blue localId=second-id style=""}`
	const thirdMD = `:status[THIRD]{color=red localId=third-id style=bold}`
	wrap := func(content string) string {
		return `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + content + `]}]}`
	}
	raw := wrap(first + "," + second)
	doc, session := prepareEdit(t, raw)
	if session.Regions()[0].Protected {
		t.Fatal("known native status structure is protected")
	}
	projected := strings.TrimSpace(adf.ToMarkdownLossy(doc).Markdown)
	for _, tc := range []struct{ name, markdown, content string }{
		{name: "reorder", markdown: secondMD + firstMD, content: second + "," + first},
		{name: "insert", markdown: firstMD + thirdMD + secondMD, content: first + "," + third + "," + second},
		{name: "delete", markdown: secondMD, content: second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := session.Apply(strings.Replace(session.Markdown(), projected, tc.markdown, 1))
			if err != nil {
				t.Fatal(err)
			}
			mdValidate(t, changed)
			statusAssertNativeDocument(t, changed, wrap(tc.content))
		})
	}
}

func TestStatusProjectionParagraphProofProtectsNativeStructure(t *testing.T) {
	const status = `{"type":"status","attrs":{"text":"READY","color":"green","localId":"owned-id","style":"bold"}}`
	for _, tc := range []struct{ name, paragraph string }{
		{name: "split URI run", paragraph: `{"type":"paragraph","content":[{"type":"text","text":"https://"},{"type":"text","text":"example.test/"},` + status + `]}`},
		{name: "explicit empty text marks", paragraph: `{"type":"paragraph","content":[{"type":"text","text":"before","marks":[]},` + status + `]}`},
		{name: "paragraph extension", paragraph: `{"type":"paragraph","future":true,"content":[` + status + `]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"version":1,"type":"doc","content":[` + tc.paragraph + `]}`
			doc, session := prepareEdit(t, raw)
			projection := adf.ToMarkdownLossy(doc)
			if !slices.Contains(projection.LossyConstructs, "paragraph") || !session.Regions()[0].Protected {
				t.Fatal("native field presence or text segmentation was approximated silently", projection)
			}
			unchanged, err := session.Apply(session.Markdown())
			if err != nil || !bytes.Equal(editBytes(t, unchanged), []byte(raw)) {
				t.Fatal("protected native paragraph bytes changed", err)
			}
			if tc.name == "split URI run" {
				converted, warnings := mdConvert(t, projection.Markdown)
				mdAssertNoWarnings(t, warnings)
				mdValidate(t, converted)
				statusAssertNativeDocument(t, converted, statusParagraph(`{"text":"READY","color":"green","localId":"owned-id","style":"bold"}`, "https://example.test/", ""))
			}
		})
	}
}

func statusParagraph(attrs, before, after string) string {
	var content []string
	for _, value := range []string{before, after} {
		if value == "" {
			content = append(content, "")
			continue
		}
		raw, _ := json.Marshal(value)
		content = append(content, `{"type":"text","text":`+string(raw)+`}`)
	}
	content = slices.Insert(content, 1, `{"type":"status","attrs":`+attrs+`}`)
	content = slices.DeleteFunc(content, func(value string) bool { return value == "" })
	return `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + strings.Join(content, ",") + `]}]}`
}

func statusAssertNativeDocument(t *testing.T, doc *adf.Document, expected string) {
	t.Helper()
	raw, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	equal, err := adf.StructuralEqual([]byte(expected), raw)
	if err != nil || !equal {
		t.Fatalf("native status document changed: %s, %v", raw, err)
	}
}
