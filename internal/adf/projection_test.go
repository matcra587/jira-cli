package adf_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestMarkdownEmptyAndDroppedContentRemainValid(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "<script>gone</script>"} {
		t.Run(input, func(t *testing.T) {
			doc, _, err := adf.FromMarkdownLossy(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				t.Fatalf("converter omitted required content after empty/dropped input: %v", err)
			}
		})
	}
}

func TestMarkdownProjectionCommonShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, content, want string }{
		{"literal punctuation", `[{"type":"paragraph","content":[{"type":"text","text":"[x] *literal* #heading"}]}]`, "\\[x\\] \\*literal\\* \\#heading\n"},
		{"heading and marks", `[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Bold","marks":[{"type":"strong"}]}]}]`, "## **Bold**\n"},
		{"ordered restart", `[{"type":"orderedList","attrs":{"order":4},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}]}]`, "4. first\n5. second\n"},
		{"multiblock list", `[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]},{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}]}]`, "- first\n  \n  second\n"},
		{"quote", `[{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]},{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}]`, "> first\n>\n> second\n"},
		{"fence in code", `[{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"first\n\u0060\u0060\u0060\nlast"}]}]`, "````go\nfirst\n```\nlast\n````\n"},
		{"backtick span", `[{"type":"paragraph","content":[{"type":"text","text":"\u0060value\u0060","marks":[{"type":"code"}]}]}]`, "`` `value` ``\n"},
		{"link destination", `[{"type":"paragraph","content":[{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://example.com/a(b)"}}]}]}]`, "[site](<https://example.com/a(b)>)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := adf.ParseDocument([]byte(`{"type":"doc","version":1,"content":` + tc.content + `}`))
			if err != nil {
				t.Fatal(err)
			}
			out := adf.ToMarkdownLossy(doc)
			if out.Markdown != tc.want || len(out.LossyConstructs) != 0 {
				t.Fatalf("projection changed content: got %q losses=%v want %q", out.Markdown, out.LossyConstructs, tc.want)
			}
			converted, _, err := adf.FromMarkdownLossy(out.Markdown)
			if err != nil || converted.Validate() != nil {
				t.Fatal("projection did not produce valid authorable Markdown")
			}
		})
	}
}

func TestMarkdownProjectionReportsSemanticLossAndPreservesSource(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"version":1,"type":"doc","futureRoot":"<>&\u2028","content":[{"type":"paragraph","attrs":{"alignment":"center"},"content":[{"type":"text","text":"hello","marks":[{"type":"textColor","attrs":{"color":"#ff0000"}},{"type":"strong","futureMark":1}]}]},{"type":"futureNode","attrs":{"private":"value"},"content":[{"type":"paragraph","content":[{"type":"text","text":"retained child"}]}]},{"type":"status","attrs":{"text":"Ready","color":"#aa55cc"}}]}`)
	doc, err := adf.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	out := adf.ToMarkdownLossy(doc)
	if !slices.Equal(out.LossyConstructs, []string{"doc", "futureNode", "paragraph", "status", "strong", "textColor"}) || !strings.Contains(out.Markdown, "retained child") || !strings.Contains(out.Markdown, `\[futureNode\]`) {
		t.Fatalf("projection hid loss or unsupported content: %+v", out)
	}
	after, err := doc.Marshal()
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("review projection modified original ADF")
	}
}

func TestMarkdownProjectionTableTaskAndMediaAreExplicitlyLossy(t *testing.T) {
	t.Parallel()
	doc, err := adf.ParseDocument([]byte(`{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"one|two"}]}]}]}]},{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"taskItem","attrs":{"localId":"item","state":"DONE"},"content":[{"type":"text","text":"done"}]}]},{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"asset","type":"file","collection":"files"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out := adf.ToMarkdownLossy(doc)
	for _, loss := range []string{"table", "taskList", "taskItem", "mediaSingle", "media"} {
		if !slices.Contains(out.LossyConstructs, loss) {
			t.Fatalf("projection hid %s loss", loss)
		}
	}
	if !strings.Contains(out.Markdown, "- [x] done") || !strings.Contains(out.Markdown, `\[attachment: asset\]`) || !strings.Contains(out.Markdown, `one\|two`) {
		t.Fatal("loss report replaced readable content")
	}
}

func TestMarkdownProjectionReportsWhitespaceApproximation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, content string
		losses        []string
	}{
		{"empty paragraph", `[{"type":"paragraph","content":[]}]`, []string{"paragraph"}},
		{"marked boundary space", `[{"type":"paragraph","content":[{"type":"text","text":" bold ","marks":[{"type":"strong"}]}]}]`, []string{"strong", "text"}},
		{"embedded line break", `[{"type":"paragraph","content":[{"type":"text","text":"first\nsecond"}]}]`, []string{"text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"doc","version":1,"content":` + tc.content + `}`)
			doc, err := adf.ParseDocument(raw)
			if err != nil {
				t.Fatal(err)
			}
			out := adf.ToMarkdownLossy(doc)
			if !slices.Equal(out.LossyConstructs, tc.losses) {
				t.Fatalf("Markdown normalization was hidden: got %v want %v", out.LossyConstructs, tc.losses)
			}
			after, err := doc.Marshal()
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("projection changed its source")
			}
		})
	}
}
