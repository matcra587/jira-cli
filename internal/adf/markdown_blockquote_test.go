package adf_test

import (
	"strings"
	"testing"
)

// A blockquote converts to the ADF blockquote node wrapping its blocks.
func TestFromMarkdownBlockquoteShape(t *testing.T) {
	doc, warnings := mdConvert(t, "> first line\n>\n> second para\n")
	mdAssertNoWarnings(t, warnings)

	quote := mdBlock(t, doc, 0)
	if quote.Type() != "blockquote" {
		t.Fatalf("node type = %q, want blockquote", quote.Type())
	}
	children := quote.Content()
	if len(children) != 2 {
		t.Fatalf("blockquote has %d children, want 2 paragraphs", len(children))
	}
	for i := range children {
		if children[i].Type() != "paragraph" {
			t.Errorf("blockquote child type = %q, want paragraph", children[i].Type())
		}
	}
}

// The original failure case: "- >50 keys" parses as a blockquote nested in
// a list item. ADF list items cannot contain blockquotes, so the quoted
// content is hoisted into the item with a non-lossy downgrade — the create
// must succeed in strict mode, not abort.
func TestFromMarkdownBlockquoteInListItemHoisted(t *testing.T) {
	doc, warnings := mdConvert(t, "- >50 keys\n")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly the hoist downgrade", warnings)
	}
	if warnings[0].Lossy {
		t.Error("hoist downgrade must be Lossy=false: the content survives")
	}

	item := mdChild(t, mdBlock(t, doc, 0), 0)
	if item.Type() != "listItem" {
		t.Fatalf("node = %q, want listItem", item.Type())
	}
	for _, child := range item.Content() {
		if child.Type() == "blockquote" {
			t.Fatalf("listItem must not contain a blockquote; got %v", item.Content())
		}
	}
	if text := mdChild(t, item, 0, 0).Text(); !strings.Contains(text, "50 keys") {
		t.Fatalf("hoisted text = %q, want the quoted content preserved", text)
	}
	mdValidate(t, doc)
}

// Nested quotes flatten into the parent (the schema forbids blockquote in
// blockquote) and quoted headings become paragraphs, both non-lossy.
func TestFromMarkdownBlockquoteDegradesForbiddenChildren(t *testing.T) {
	doc, warnings := mdConvert(t, "> outer\n> > inner\n\n> # quoted heading\n")
	for _, w := range warnings {
		if w.Lossy {
			t.Errorf("downgrade must be non-lossy; got %+v", w)
		}
	}
	for _, quote := range doc.Content() {
		for _, child := range quote.Content() {
			if child.Type() == "blockquote" || child.Type() == "heading" {
				t.Fatalf("blockquote child %q is schema-invalid", child.Type())
			}
		}
	}
	mdValidate(t, doc)
}

// Bold-wrapped inline code is an invalid ADF mark pair: the converter keeps
// code, drops the decorative mark, and reports a NON-lossy source-mapped
// downgrade — the text and its code mark survive verbatim, so the default
// strict mutation mode proceeds with the warning instead of aborting.
func TestFromMarkdownCodeMarkConflictSanitized(t *testing.T) {
	doc, warnings := mdConvert(t, "- **`Tree()`** walks\n")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly the mark-conflict warning", warnings)
	}
	w := warnings[0]
	if w.Lossy {
		t.Error("mark-conflict warning must be a non-lossy downgrade (decoration loss, not content loss)")
	}
	if !strings.Contains(w.Message, "strong") {
		t.Errorf("message = %q, want the dropped mark named", w.Message)
	}
	if !strings.Contains(w.Message, "line 1") || !strings.Contains(w.Message, "Tree()") {
		t.Errorf("message = %q, want source line and snippet", w.Message)
	}
	if w.Path == "" {
		t.Error("warning path must carry the source position")
	}

	// bulletList → listItem → paragraph → text
	code := mdChild(t, mdBlock(t, doc, 0), 0, 0, 0)
	marks := code.Marks()
	if len(marks) != 1 || marks[0].Type() != "code" {
		t.Fatalf("marks = %+v, want code only", marks)
	}
	mdValidate(t, doc)
}

// A link-wrapped code span is the one legal combination and stays intact.
func TestFromMarkdownCodeInsideLinkKeepsBothMarks(t *testing.T) {
	doc, warnings := mdConvert(t, "[`docs`](https://example.com)\n")
	mdAssertNoWarnings(t, warnings)

	node := mdChild(t, mdBlock(t, doc, 0), 0)
	types := mdMarkTypes(node)
	if !types["code"] || !types["link"] {
		t.Fatalf("marks = %v, want code + link", types)
	}
	mdValidate(t, doc)
}

// Dropped constructs carry the source position and offending snippet so an
// author can find the input line without decoding a JSON path.
func TestFromMarkdownDroppedConstructIsSourceMapped(t *testing.T) {
	_, warnings := mdConvert(t, "fine paragraph\n\n<div>raw</div>\n")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one raw-HTML drop", warnings)
	}
	w := warnings[0]
	if !strings.Contains(w.Message, "line 3") || !strings.Contains(w.Message, "<div>raw</div>") {
		t.Errorf("message = %q, want line number and snippet", w.Message)
	}
	if !strings.Contains(w.Path, "line 3") {
		t.Errorf("path = %q, want source position", w.Path)
	}
}
