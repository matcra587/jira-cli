package adf_test

import (
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// This file pins the Typed()/MarkTyped() dispatch mechanism itself — the
// generated type switches in nodes_gen.go — plus the AsX()/AsXMark()
// accessor CONTRACT with a representative sample across node categories
// (root, block with/without attrs, inline leaf, mark-bearing text) rather
// than one test per generated accessor.

// TestTypedDispatchAcrossCategories pins that Typed() routes real node
// kinds to the correct concrete view across categories: the document root,
// a block node with no typed attrs struct, a block node with attrs and
// content, and an inline leaf node with required attrs.
func TestTypedDispatchAcrossCategories(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantNodeType adf.NodeType
	}{
		{"doc root", `{"type":"doc","version":1,"content":[]}`, adf.NodeTypeDoc},
		{"block with no attrs struct", `{"type":"mediaGroup","content":[]}`, adf.NodeTypeMediaGroup},
		{"block with attrs and content", `{"type":"blockquote","content":[]}`, adf.NodeTypeBlockquote},
		{"inline leaf with required attrs", `{"type":"emoji","attrs":{"shortName":":smile:"}}`, adf.NodeTypeEmoji},
		{"mark-bearing inline text", `{"type":"text","text":"hi","marks":[{"type":"strong"}]}`, adf.NodeTypeText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := parseNode(t, tt.in)
			typed, err := adf.Typed(n)
			if err != nil {
				t.Fatalf("Typed: %v", err)
			}
			if typed.NodeType() != tt.wantNodeType {
				t.Errorf("NodeType() = %q, want %q", typed.NodeType(), tt.wantNodeType)
			}
			if typed.Node() != n {
				t.Errorf("view does not wrap the original node")
			}
		})
	}
}

// TestTypedRoutesToExactConcreteType pins the type switch's per-case
// routing (not just the NodeType() label it reports back).
func TestTypedRoutesToExactConcreteType(t *testing.T) {
	typed, err := adf.Typed(parseNode(t, `{"type":"emoji","attrs":{"shortName":":smile:"}}`))
	if err != nil {
		t.Fatalf("Typed: %v", err)
	}
	emoji, ok := typed.(*adf.Emoji)
	if !ok {
		t.Fatalf("Typed returned %T, want *adf.Emoji", typed)
	}
	if emoji.Attrs.ShortName != ":smile:" {
		t.Errorf("ShortName = %q", emoji.Attrs.ShortName)
	}
}

// TestAsXWrongTypeContract pins the accessor contract for the "wrong type"
// case across categories: a nil view and a non-nil error naming both the
// actual and wanted type, never a zero-value view with no error.
func TestAsXWrongTypeContract(t *testing.T) {
	textNode := parseNode(t, `{"type":"text","text":"x"}`)

	panel, err := adf.AsPanel(textNode)
	if panel != nil {
		t.Errorf("AsPanel(text node) view = %v, want nil", panel)
	}
	if err == nil {
		t.Fatal("AsPanel(text node) err = nil, want error")
	}
	wantMsg := `adf: node is "text", not "panel"`
	if err.Error() != wantMsg {
		t.Errorf("AsPanel err = %q, want %q", err.Error(), wantMsg)
	}

	doc, err := adf.AsDoc(textNode)
	if doc != nil || err == nil {
		t.Errorf("AsDoc(text node) = %v, %v; want nil, error", doc, err)
	}

	text, err := adf.AsText(parseNode(t, `{"type":"panel","attrs":{"panelType":"info"},"content":[]}`))
	if text != nil || err == nil {
		t.Errorf("AsText(panel node) = %v, %v; want nil, error", text, err)
	}
}

// TestAsXAttrsDecodeError pins the case a low-coverage pass would miss:
// attrs present but shaped wrong for the target struct (a JSON array where
// an object is expected). AsX distinguishes "wrong node type" from
// "right type, unparseable attrs" with a different error.
func TestAsXAttrsDecodeError(t *testing.T) {
	n := parseNode(t, `{"type":"panel","attrs":[1,2],"content":[]}`)
	panel, err := adf.AsPanel(n)
	if panel != nil {
		t.Errorf("AsPanel with array attrs view = %v, want nil", panel)
	}
	if err == nil {
		t.Fatal("AsPanel with array attrs err = nil, want error")
	}
	wantMsg := "adf: panel attrs: json: cannot unmarshal array into Go value of type adf.PanelAttrs"
	if err.Error() != wantMsg {
		t.Errorf("err = %q, want %q", err.Error(), wantMsg)
	}
}

// TestMarkTypedUnknownMark mirrors typed_test.go's TestTypedUnknownNode:
// MarkTyped's own ErrUnknownMarkType path had no pin at all.
func TestMarkTypedUnknownMark(t *testing.T) {
	n := parseNode(t, `{"type":"text","text":"x","marks":[{"type":"futureMark"}]}`)
	marks := n.Marks()
	_, err := adf.MarkTyped(&marks[0])
	if !errors.Is(err, adf.ErrUnknownMarkType) {
		t.Errorf("MarkTyped error = %v, want ErrUnknownMarkType", err)
	}
}

// TestMarkTypedDispatchAcrossCategories pins MarkTyped's routing across a
// mark with required attrs, a mark with no attrs at all, and a mark whose
// attrs are all optional.
func TestMarkTypedDispatchAcrossCategories(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantMarkType adf.MarkType
	}{
		{"mark with required attrs", `{"type":"alignment","attrs":{"align":"center"}}`, adf.MarkTypeAlignment},
		{"mark with no attrs struct", `{"type":"strong"}`, adf.MarkTypeStrong},
		{"mark with all-optional attrs", `{"type":"link","attrs":{"href":"https://example.com"}}`, adf.MarkTypeLink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := parseNode(t, `{"type":"text","text":"x","marks":[`+tt.in+`]}`)
			marks := n.Marks()
			typed, err := adf.MarkTyped(&marks[0])
			if err != nil {
				t.Fatalf("MarkTyped: %v", err)
			}
			if typed.MarkType() != tt.wantMarkType {
				t.Errorf("MarkType() = %q, want %q", typed.MarkType(), tt.wantMarkType)
			}
			if typed.Mark() != &marks[0] {
				t.Errorf("view does not wrap the original mark")
			}
		})
	}
}

// TestAsXMarkWrongTypeContract mirrors TestAsXWrongTypeContract for marks:
// nil view, non-nil error, never a zero-value view.
func TestAsXMarkWrongTypeContract(t *testing.T) {
	strongMark := parseNode(t, `{"type":"text","text":"x","marks":[{"type":"strong"}]}`).Marks()[0]

	link, err := adf.AsLinkMark(&strongMark)
	if link != nil {
		t.Errorf("AsLinkMark(strong mark) view = %v, want nil", link)
	}
	if err == nil {
		t.Fatal("AsLinkMark(strong mark) err = nil, want error")
	}
	wantMsg := `adf: mark is "strong", not "link"`
	if err.Error() != wantMsg {
		t.Errorf("err = %q, want %q", err.Error(), wantMsg)
	}
}

// TestAsXMarkAttrsDecodeError mirrors TestAsXAttrsDecodeError for marks.
func TestAsXMarkAttrsDecodeError(t *testing.T) {
	m := parseNode(t, `{"type":"text","text":"x","marks":[{"type":"link","attrs":[1,2]}]}`).Marks()[0]
	link, err := adf.AsLinkMark(&m)
	if link != nil {
		t.Errorf("AsLinkMark with array attrs view = %v, want nil", link)
	}
	if err == nil {
		t.Fatal("AsLinkMark with array attrs err = nil, want error")
	}
}
