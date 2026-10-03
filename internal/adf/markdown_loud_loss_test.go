package adf_test

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// These tests pin the loud-loss contract: the converter never drops content
// silently. Every construct it cannot represent yields a lossy warning
// naming the construct and its source position, so strict mode can abort
// and best-effort callers can see exactly what went missing. The one
// deliberate exception is the link-reference definition, which is parser
// metadata rather than content — the links that use it still render.

func TestFromMarkdownHTMLBlockWarnsLossy(t *testing.T) {
	doc, warnings := mdConvert(t, "intro\n\n<div>\nraw block\n</div>\n\noutro")
	var found *adf.Warning
	for i, w := range warnings {
		if w.NodeType == "raw HTML block" {
			found = &warnings[i]
		}
	}
	if found == nil {
		t.Fatalf("dropped HTML block must warn, got %+v", warnings)
	}
	if !found.Lossy {
		t.Fatalf("HTML block drop must be lossy (strict aborts), got %+v", *found)
	}
	if found.Path == "" || !strings.Contains(found.Message, "line 3") {
		t.Fatalf("HTML block warning must be source-mapped to line 3: %+v", *found)
	}
	// The convertible content survives around the drop.
	if kinds := mdKinds(doc); len(kinds) != 2 {
		t.Fatalf("intro and outro paragraphs must survive, got %v", kinds)
	}
}

func TestFromMarkdownInlineHTMLWarnsLossy(t *testing.T) {
	_, warnings := mdConvert(t, "before <span>x</span> after")
	var found bool
	for _, w := range warnings {
		if w.NodeType == "inline raw HTML" && w.Lossy {
			found = true
		}
	}
	if !found {
		t.Fatalf("dropped inline HTML must warn lossy, got %+v", warnings)
	}
}

// TestFromMarkdownLinkReferenceDefinitionIsTheSilentException documents the
// single construct that drops without a warning: the reference definition
// line itself. It is not content — the link that references it converts
// with its resolved destination, so nothing the author wrote is lost.
func TestFromMarkdownLinkReferenceDefinitionIsTheSilentException(t *testing.T) {
	doc, warnings := mdConvert(t, "see [the docs][ref]\n\n[ref]: https://example.com/docs\n")
	mdAssertNoWarnings(t, warnings)

	var href string
	var walk func(nodes []adf.Node)
	walk = func(nodes []adf.Node) {
		for i := range nodes {
			for _, m := range nodes[i].Marks() {
				if m.Type() == "link" {
					href = mdHref(t, m)
				}
			}
			walk(nodes[i].Content())
		}
	}
	walk(doc.Content())
	if href != "https://example.com/docs" {
		t.Fatalf("reference link must resolve its definition, got href %q", href)
	}
}
