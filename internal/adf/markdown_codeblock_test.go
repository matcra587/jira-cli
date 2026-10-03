package adf_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Markdown-input layer: a fenced ```lang block in Markdown MUST
// produce an ADF codeBlock with attrs.language=lang. The hint is what
// Jira (and downstream renderers) use to syntax-highlight; dropping it
// silently turns a Go snippet into anonymous text.
func TestFromMarkdownPreservesFencedCodeLanguage(t *testing.T) {
	cases := map[string]string{
		"```go\nfunc main() {}\n```":       "go",
		"```python\nprint('hi')\n```":      "python",
		"```rust\nfn main() {}\n```":       "rust",
		"```typescript\nconst x = 1;\n```": "typescript",
	}
	for md, want := range cases {
		t.Run(want, func(t *testing.T) {
			doc, _ := mdConvert(t, md)
			block, err := adf.AsCodeBlock(mdFindCodeBlock(t, doc))
			if err != nil {
				t.Fatalf("AsCodeBlock: %v", err)
			}
			if block.Attrs.Language != want {
				t.Fatalf("language for fenced ```%s = %q, want %q", want, block.Attrs.Language, want)
			}
		})
	}
}

// Unhinted code blocks (indented, or fenced without a language) MUST carry
// attrs.language as an explicit empty string: Jira renders an attr-less
// codeBlock with its default language (java), so "no attr" silently
// java-highlights arbitrary output. Asserted on the raw attrs rather than
// the typed view, because the generated CodeBlockAttrs tags Language
// omitempty — a decoded "" cannot tell an empty value from an absent key,
// which is exactly the distinction this test exists to pin.
func TestFromMarkdownUnhintedCodeCarriesEmptyLanguage(t *testing.T) {
	for name, md := range map[string]string{
		"indented":       "    indented code\n    second line\n",
		"fenced-no-hint": "```\nplain output\n```",
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := mdConvert(t, md)
			raw := mdFindCodeBlock(t, doc).Attrs()
			if raw == nil {
				t.Fatal("code block must carry attrs (Jira defaults attr-less blocks to java)")
			}
			var attrs map[string]json.RawMessage
			if err := json.Unmarshal(raw, &attrs); err != nil {
				t.Fatalf("decode attrs: %v", err)
			}
			lang, has := attrs["language"]
			if !has {
				t.Fatalf("code block must carry attrs.language; got %s", raw)
			}
			if string(lang) != `""` {
				t.Fatalf("attrs.language = %s, want an empty string for an unhinted block", lang)
			}
		})
	}
}

// An empty fenced block must produce a codeBlock with NO content array —
// the ADF schema requires text nodes to be non-empty, so an empty text
// child is a validation rejection waiting to happen.
func TestFromMarkdownEmptyCodeBlockHasNoTextChild(t *testing.T) {
	for name, md := range map[string]string{
		"empty":        "```\n```",
		"newline-only": "```go\n\n```",
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := mdConvert(t, md)
			block := mdFindCodeBlock(t, doc)
			if content := block.Content(); len(content) != 0 {
				t.Fatalf("empty code block must have no content; got %+v", content)
			}
			// The substrate distinguishes "no content key" from an empty
			// array, and only the former is what the schema wants here.
			if _, ok := block.Field("content"); ok {
				t.Error("empty code block must not carry a content key at all")
			}
		})
	}
}

// The fence's trailing newline is Markdown syntax, not code: the text child
// must not carry it, or Jira renders a spurious blank line.
func TestFromMarkdownCodeBlockTrimsTrailingNewline(t *testing.T) {
	doc, _ := mdConvert(t, "```go\nfunc main() {}\n```")
	block := mdFindCodeBlock(t, doc)
	if content := block.Content(); len(content) != 1 {
		t.Fatalf("code block content = %+v, want one text child", content)
	}
	if got, want := mdChild(t, block, 0).Text(), "func main() {}"; got != want {
		t.Fatalf("code block text = %q, want %q", got, want)
	}
}

// Every shape this converter emits for code blocks must pass schema
// validation — the whole point of the fixes is surviving Jira's schema.
func TestFromMarkdownCodeBlocksPassValidation(t *testing.T) {
	for name, md := range map[string]string{
		"hinted":   "```go\nfunc main() {}\n```",
		"unhinted": "```\nplain\n```",
		"empty":    "```\n```",
		"indented": "    indented code\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := mdConvert(t, md)
			mdValidate(t, doc)
		})
	}
}

func mdFindCodeBlock(t *testing.T, doc *adf.Document) *adf.Node {
	t.Helper()
	content := doc.Content()
	for i := range content {
		if content[i].Type() == "codeBlock" {
			return &content[i]
		}
	}
	t.Fatalf("no codeBlock node in converted document; got %v", mdKinds(doc))
	return nil
}
