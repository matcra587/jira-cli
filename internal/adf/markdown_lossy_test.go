package adf_test

// Honest Markdown→ADF conversion tests.
//
// FromMarkdownLossy must emit a structured warning for every Markdown
// construct it cannot represent faithfully in the supported ADF node
// set. Silent drops hide content loss from callers and from strict-mode
// abort gates.

import (
	"strings"
	"testing"
)

// A GFM table converts to a real ADF table node — no warning, no drop.
func TestFromMarkdownLossyTableConvertsWithoutWarning(t *testing.T) {
	doc, warnings := mdConvert(t, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	mdAssertNoWarnings(t, warnings)
	if kinds := mdKinds(doc); len(kinds) != 1 || kinds[0] != "table" {
		t.Fatalf("top-level nodes = %v, want a single table", kinds)
	}
}

// An image degrades to an alt-text link and reports the downgrade as a
// non-lossy warning — the reference survives, so strict mode is not blocked.
func TestFromMarkdownLossyImageWarnsWithoutBlockingStrict(t *testing.T) {
	_, warnings := mdConvert(t, "![alt](https://example.com/p.png)\n")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly the image downgrade notice", warnings)
	}
	if warnings[0].Lossy {
		t.Error("image downgrade must be Lossy=false: the link preserves the reference")
	}
}

// Plain supported Markdown converts with no warnings.
func TestFromMarkdownLossyPlainParagraphNoWarnings(t *testing.T) {
	doc, warnings := mdConvert(t, "hello **world**\n")
	mdAssertNoWarnings(t, warnings)
	if len(doc.Content()) == 0 {
		t.Fatal("expected non-empty document")
	}
}

// Reference link definitions are parser metadata once goldmark resolves
// them onto the link node; dropping the definition line must not become
// a lossy conversion warning.
func TestFromMarkdownLossyReferenceLinkDefinitionDoesNotWarn(t *testing.T) {
	doc, warnings := mdConvert(t, "[example][ref]\n\n[ref]: https://example.com \"title\"\n")
	mdAssertNoWarnings(t, warnings)
	if kinds := mdKinds(doc); len(kinds) != 1 {
		t.Fatalf("top-level nodes = %v, want one paragraph", kinds)
	}
	node := mdChild(t, mdBlock(t, doc, 0), 0)
	if node.Text() != "example" {
		t.Fatalf("text = %q, want %q", node.Text(), "example")
	}
	marks := node.Marks()
	if len(marks) != 1 || marks[0].Type() != "link" {
		t.Fatalf("expected one link mark; got %+v", marks)
	}
	if got := mdHref(t, marks[0]); got != "https://example.com" {
		t.Fatalf("href = %q, want %q", got, "https://example.com")
	}
}

// Headings, lists, code fences, and emphasis are all supported — no
// warnings for a document built only from those.
func TestFromMarkdownLossySupportedConstructsNoWarnings(t *testing.T) {
	md := "# Title\n\n" +
		"Some *emphasis* and **strong** and a [link](https://x).\n\n" +
		"- one\n- two\n\n" +
		"1. first\n2. second\n\n" +
		"```go\nfunc main() {}\n```\n"
	doc, warnings := mdConvert(t, md)
	mdAssertNoWarnings(t, warnings)
	mdValidate(t, doc)
}

// A blockquote converts to a real ADF blockquote node — no warning.
func TestFromMarkdownLossyBlockquoteConvertsWithoutWarning(t *testing.T) {
	doc, warnings := mdConvert(t, "> quoted line\n")
	mdAssertNoWarnings(t, warnings)
	if kinds := mdKinds(doc); len(kinds) != 1 || kinds[0] != "blockquote" {
		t.Fatalf("top-level nodes = %v, want a single blockquote", kinds)
	}
}

// The warning message must name the unsupported construct so callers
// and agents can act on it.
func TestFromMarkdownLossyWarningNamesConstruct(t *testing.T) {
	_, warnings := mdConvert(t, "![alt](https://example.com/p.png)\n")
	if len(warnings) == 0 {
		t.Fatal("expected a warning")
	}
	named := false
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w.Message), "image") {
			named = true
		}
	}
	if !named {
		t.Errorf("warning should name the 'image' construct; got %+v", warnings)
	}
}

// Hard line breaks inside a paragraph are representable as a hardBreak
// node — conversion must preserve them, not collapse the break.
func TestFromMarkdownLossyHardBreakPreserved(t *testing.T) {
	doc, _ := mdConvert(t, "line one  \nline two\n")
	if !mdContainsType(doc.Content(), "hardBreak") {
		t.Errorf("hard break should convert to a hardBreak node; kinds=%v", mdKinds(doc))
	}
}
