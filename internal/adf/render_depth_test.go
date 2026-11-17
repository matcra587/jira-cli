package adf_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestRenderDeepContentIsBoundedWithoutChangingNativeDocument(t *testing.T) {
	const leaf = `{"type":"paragraph","content":[{"type":"text","text":"nested leaf"}]}`
	for _, test := range []struct{ name, prefix, suffix, outer string }{
		{name: "blockquote", prefix: `{"type":"blockquote","content":[`, suffix: `]}`},
		{name: "panel", prefix: `{"type":"panel","content":[`, suffix: `]}`},
		{name: "expand", prefix: `{"type":"expand","content":[`, suffix: `]}`},
		{name: "layout", prefix: `{"type":"layoutColumn","content":[`, suffix: `]}`},
		{name: "list", prefix: `{"type":"bulletList","content":[{"type":"listItem","content":[`, suffix: `]}]}`},
		{
			name:   "table",
			prefix: `{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[`,
			suffix: `]}]}]}`,
		},
		{name: "readable fallback", prefix: `{"type":"futureBlock","content":[`, suffix: `]}`, outer: "decisionItem"},
		{name: "table fallback", prefix: `{"type":"futureBlock","content":[`, suffix: `]}`, outer: "table"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := leaf
			for range 256 {
				body = test.prefix + body + test.suffix
			}
			if test.outer != "" {
				body = `{"type":"` + test.outer + `","content":[` + body + `]}`
			}
			doc := mustParse(t, `{"type":"doc","version":1,"content":[`+body+`]}`)
			before, err := doc.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			got := adf.Render(doc, fakeStyler{}, 0)
			if !strings.Contains(got, "nesting limit") || strings.Contains(got, "nested leaf") {
				t.Fatal("deep content was rendered without an explicit nesting-limit placeholder")
			}
			after, err := doc.Marshal()
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("human render changed the preserved native document", err)
			}
		})
	}
}
