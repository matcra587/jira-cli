package adf_test

import "testing"

// A Markdown image converts to its alt text carrying a link mark to the
// image URL — ADF cannot embed external images by URL, so the clickable
// reference is the faithful fallback.
func TestFromMarkdownImageBecomesAltTextLink(t *testing.T) {
	doc, _ := mdConvert(t, "![screenshot](https://example.com/p.png)\n")

	node := mdChild(t, mdBlock(t, doc, 0), 0)
	if node.Type() != "text" || node.Text() != "screenshot" {
		t.Fatalf("node = %q/%q, want a text node carrying the alt text", node.Type(), node.Text())
	}
	marks := node.Marks()
	if len(marks) != 1 || marks[0].Type() != "link" {
		t.Fatalf("marks = %+v, want a single link mark", marks)
	}
	if href := mdHref(t, marks[0]); href != "https://example.com/p.png" {
		t.Fatalf("link href = %q, want the image URL", href)
	}
}

// An image with an empty alt uses the URL as the display text.
func TestFromMarkdownImageEmptyAltFallsBackToURL(t *testing.T) {
	doc, _ := mdConvert(t, "![](https://example.com/p.png)\n")
	if got := mdChild(t, mdBlock(t, doc, 0), 0).Text(); got != "https://example.com/p.png" {
		t.Fatalf("text = %q, want the URL fallback", got)
	}
}

// Surrounding inline marks thread onto the degraded link, and the result
// passes schema validation.
func TestFromMarkdownImageInheritsMarksAndValidates(t *testing.T) {
	doc, _ := mdConvert(t, "**![alt](https://example.com/p.png)**\n")

	types := mdMarkTypes(mdChild(t, mdBlock(t, doc, 0), 0))
	if !types["strong"] || !types["link"] {
		t.Fatalf("marks = %v, want strong + link", types)
	}
	mdValidate(t, doc)
}
