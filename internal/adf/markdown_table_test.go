package adf_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// A GFM table must convert to the canonical ADF table shape: table →
// tableRow → tableHeader/tableCell → paragraph → text, with the standard
// table attrs.
func TestFromMarkdownTableShape(t *testing.T) {
	doc, warnings := mdConvert(t, "| Key | Value |\n|---|---|\n| alpha | 1 |\n| beta | 2 |\n")
	mdAssertNoWarnings(t, warnings)

	if kinds := mdKinds(doc); len(kinds) != 1 || kinds[0] != "table" {
		t.Fatalf("top-level nodes = %v, want a single table", kinds)
	}
	tableNode := mdBlock(t, doc, 0)
	table, err := adf.AsTable(tableNode)
	if err != nil {
		t.Fatalf("AsTable: %v", err)
	}
	if table.Attrs.IsNumberColumnEnabled == nil || *table.Attrs.IsNumberColumnEnabled {
		t.Errorf("attrs.isNumberColumnEnabled = %v, want an explicit false", table.Attrs.IsNumberColumnEnabled)
	}
	if table.Attrs.Layout != "default" {
		t.Errorf("attrs.layout = %q, want default", table.Attrs.Layout)
	}

	rows := tableNode.Content()
	if len(rows) != 3 {
		t.Fatalf("table has %d rows, want 3 (header + 2 body)", len(rows))
	}
	header := rows[0]
	if header.Type() != "tableRow" {
		t.Fatalf("first child type = %q, want tableRow", header.Type())
	}
	for _, cell := range header.Content() {
		if cell.Type() != "tableHeader" {
			t.Errorf("header row cell type = %q, want tableHeader", cell.Type())
		}
	}

	body := rows[1]
	if len(body.Content()) != 2 {
		t.Fatalf("body row has %d cells, want 2", len(body.Content()))
	}
	cell := mdChild(t, &body, 0)
	if cell.Type() != "tableCell" {
		t.Fatalf("body cell type = %q, want tableCell", cell.Type())
	}
	if content := cell.Content(); len(content) != 1 || content[0].Type() != "paragraph" {
		t.Fatalf("cell content = %+v, want a single paragraph", content)
	}
	if got := mdChild(t, cell, 0, 0).Text(); got != "alpha" {
		t.Fatalf("cell text = %q, want alpha", got)
	}
}

// Inline marks survive inside cells, and an empty cell still satisfies the
// schema's one-block-child minimum via an empty paragraph.
func TestFromMarkdownTableCellInlinesAndEmptyCells(t *testing.T) {
	doc, _ := mdConvert(t, "| a | b |\n|---|---|\n| **bold** | |\n")

	body := mdChild(t, mdBlock(t, doc, 0), 1)
	bold := mdChild(t, body, 0, 0, 0)
	marks := bold.Marks()
	if len(marks) != 1 || marks[0].Type() != "strong" {
		t.Errorf("cell text marks = %+v, want strong", marks)
	}

	empty := mdChild(t, body, 1)
	if content := empty.Content(); len(content) != 1 || content[0].Type() != "paragraph" {
		t.Fatalf("empty cell content = %+v, want one (empty) paragraph", content)
	}
	if content := mdChild(t, empty, 0).Content(); len(content) != 0 {
		t.Errorf("empty cell paragraph content = %+v, want none", content)
	}
}

// Converted tables must pass schema validation — header rows, body rows,
// empty cells and all.
func TestFromMarkdownTablePassesValidation(t *testing.T) {
	for name, md := range map[string]string{
		"basic":      "| Key | Value |\n|---|---|\n| alpha | 1 |\n",
		"empty-cell": "| a | b |\n|---|---|\n| x | |\n",
		"marks":      "| a |\n|---|\n| `code` and [link](https://example.com) |\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := mdConvert(t, md)
			mdValidate(t, doc)
		})
	}
}
