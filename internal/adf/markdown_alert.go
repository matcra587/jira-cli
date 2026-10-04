package adf

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
)

// alert recognizes GitHub's five standalone top-level alert markers from
// source, not rendered text: escaped, linked and code spellings stay literal.
// GitHub forbids nested alerts, so existing quote/list conversion owns those.
// The parser owns this AST exclusively; removing the marker keeps the body's
// inline parsing and document-scoped references intact without reparsing it.
func (c *mdConverter) alert(quote ast.Node) (Node, bool) {
	if quote.Parent() == nil || quote.Parent().Kind() != ast.KindDocument {
		return Node{}, false
	}
	first := quote.FirstChild()
	if first == nil || first.Kind() != ast.KindParagraph || first.Lines().Len() == 0 {
		return Node{}, false
	}
	line := first.Lines().At(0)
	var kind string
	switch strings.TrimSpace(string(line.Value(c.source))) {
	case "[!NOTE]":
		kind = "info"
	case "[!TIP]":
		kind = "tip"
	case "[!IMPORTANT]":
		kind = "note"
	case "[!WARNING]":
		kind = "warning"
	case "[!CAUTION]":
		kind = "error"
	default:
		return Node{}, false
	}
	markerCount := 0
	for child := first.FirstChild(); child != nil; child = child.NextSibling() {
		if position := child.Pos(); position < line.Start || position >= line.Stop {
			break
		}
		if child.Kind() != ast.KindText {
			return Node{}, false
		}
		markerCount++
	}
	if markerCount == 0 {
		return Node{}, false
	}
	// Goldmark 1.8.6 assigns parsed inline positions even to childless
	// autolinks and HTML. Count only proven plain marker-line nodes before
	// changing the tree; a shortcut reference link remains an ordinary quote.
	for range markerCount {
		first.RemoveChild(first, first.FirstChild())
	}
	if !first.HasChildren() {
		quote.RemoveChild(quote, first)
	}
	var content []Node
	for child := quote.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case ast.KindBlockquote:
			c.warnDowngrade(child, "blockquote inside an alert", "quoted content flattened into the panel")
			content = append(content, c.blockquoteChildren(child)...)
		case extast.KindTable:
			c.warnDowngrade(child, "table inside an alert", "moved after the enclosing panel")
			if node, ok := c.block(child); ok {
				c.hoisted = append(c.hoisted, node)
			}
		default:
			if node, ok := c.block(child); ok {
				content = append(content, node)
			}
		}
	}
	// Stable panels require at least one block even for an empty alert or
	// a best-effort candidate whose entire body was explicitly dropped.
	if len(content) == 0 {
		content = []Node{c.node(NodeTypeParagraph, nil, nil)}
	}
	return c.node(NodeTypePanel, PanelAttrs{PanelType: kind}, content), true
}
