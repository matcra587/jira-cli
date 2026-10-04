package adf_test

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestFromMarkdownTextDecodesCommonMarkSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{name: "escaped punctuation", source: `a \*literal\* \[label\] \\path`, want: `a *literal* [label] \path`},
		{name: "named references", source: `a &amp; &lt;tag&gt; &copy; &NotEqualTilde;`, want: "a & <tag> © ≂̸"},
		{name: "numeric references", source: `a &#65; &#x41; &#00065; &#x1F642;`, want: "a A A A 🙂"},
		{name: "one decoding pass", source: `a &amp;lt; &amp;#65; &bsol;*`, want: `a &lt; &#65; \*`},
		{name: "escaped ampersand", source: `a \&amp; \&#65;`, want: `a &amp; &#65;`},
		{name: "non escapes", source: `a \q &unknown; &amp &#12345678;`, want: `a \q &unknown; &amp &#12345678;`},
		{name: "invalid numeric scalar", source: `a &#0; &#xD800; &#x110000;`, want: "a � � �"},
		{name: "plain Unicode", source: "a 雪 👩🏽‍💻 é", want: "a 雪 👩🏽‍💻 é"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			var text strings.Builder
			for _, child := range mdBlock(t, doc, 0).Content() {
				if child.Type() != "text" || len(child.Marks()) != 0 {
					t.Fatalf("literal source became formatted or non-text content: %s", child.Type())
				}
				text.WriteString(child.Text())
			}
			if text.String() != tc.want {
				t.Fatalf("decoded text = %q, want %q", text.String(), tc.want)
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownTextKeepsCodeLiteral(t *testing.T) {
	for _, source := range []string{"`\\* &amp; &#65;`", "```text\n\\* &amp; &#65;\n```", "    \\* &amp; &#65;"} {
		doc, warnings := mdConvert(t, source)
		mdAssertNoWarnings(t, warnings)
		node := mdChild(t, mdBlock(t, doc, 0), 0)
		if node.Text() != `\* &amp; &#65;` {
			t.Fatalf("code text decoded Markdown syntax: %q", node.Text())
		}
		mdValidate(t, doc)
	}
}

func TestFromMarkdownTextDecodePreservesContexts(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{name: "paragraph", source: `before \*literal\* after`, want: "before *literal* after"},
		{name: "heading", source: `# \[Heading\] &amp;`, want: "[Heading] &"},
		{name: "marked text", source: `**bold &amp; \*literal\***`, want: "bold & *literal*"},
		{name: "link label", source: `[a &amp; \[label\]](https://example.test)`, want: "a & [label]"},
		{name: "table cell", source: "| H |\n| - |\n| a &amp; \\*literal\\* |", want: "Ha & *literal*"},
		{name: "alert body", source: "> [!NOTE]\n> a &amp; \\*literal\\*", want: "a & *literal*"},
		{name: "task text", source: `- [ ] a &amp; \*literal\*`, want: "a & *literal*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			var content strings.Builder
			var walk func(*adf.Node)
			walk = func(node *adf.Node) {
				if node.Type() == "text" {
					content.WriteString(node.Text())
				}
				for _, child := range node.Content() {
					walk(&child)
				}
			}
			walk(&doc.Node)
			if content.String() != tc.want {
				t.Fatalf("decoded context = %q, want %q", content.String(), tc.want)
			}
			mdValidate(t, doc)
		})
	}
}

func TestMarkdownEscapedNativePanelRoundTrip(t *testing.T) {
	original := []byte(`{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"*literal* [label] \\path &lt; &#65;"}]}]}]}`)
	doc, err := adf.ParseDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	projection := adf.ToMarkdownLossy(doc)
	if len(projection.LossyConstructs) != 0 {
		t.Fatalf("representable native text is still lossy: %v", projection.LossyConstructs)
	}
	converted, warnings := mdConvert(t, projection.Markdown)
	mdAssertNoWarnings(t, warnings)
	raw, err := converted.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	equal, err := adf.StructuralEqual(original, raw)
	if err != nil || !equal {
		t.Fatalf("projection changed literal text: %s, %v", raw, err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Regions()) != 1 || session.Regions()[0].Protected {
		t.Fatal("representable literal text remained protected")
	}
	unchanged, err := session.Apply(session.Markdown())
	if err != nil {
		t.Fatal(err)
	}
	raw, err = unchanged.Marshal()
	if err != nil || string(raw) != string(original) {
		t.Fatal("unchanged native source bytes changed")
	}
}
