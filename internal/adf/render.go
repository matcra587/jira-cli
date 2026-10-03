package adf

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gechr/x/ansi"
)

// Styler is internal/adf's injection point for terminal styling. This
// package must stay pure — parse/validate/render, nothing more — so it
// must NOT import internal/cli or internal/output (doc.go); Render calls
// out to this interface instead of styling bytes itself. internal/cli
// implements it over the shared human-output sinks (cli.Text,
// cli.MarkupStrong, ...), and internal/cli/issue supplies the Link method
// from its View's own hyperlink support (clog.Output.Hyperlink already
// degrades to plain text when color is off, so Styler need not know
// about that itself).
type Styler interface {
	// Text sanitizes leaf text — every "text" node's content, and any
	// placeholder label Render builds for a node it does not draw, is
	// untrusted Jira content. Render calls this BEFORE Strong/Emphasis/
	// Code so marks are layered onto sanitized text, never onto raw
	// Jira bytes. Render also calls this on a link mark's href — equally
	// untrusted Jira content, and the raw input to Link's own OSC 8
	// escape — immediately before Link, so the sanitization runs exactly
	// once and stays here rather than duplicated by every implementation.
	Text(s string) string
	// Strong renders s with the "strong" mark's emphasis.
	Strong(s string) string
	// Emphasis renders s with the "em" mark's emphasis.
	Emphasis(s string) string
	// Code renders s with the "code" mark's (or a codeBlock line's)
	// emphasis.
	Code(s string) string
	// Link renders text as a hyperlink to url, with whatever plain-text
	// fallback the implementation owns for a writer where a real
	// terminal hyperlink is unavailable.
	Link(url, text string) string
}

// Render walks doc's content and returns terminal-ready text for the
// human `issue view` — the render half of the substrate/typed/render
// split doc.go describes. s styles and sanitizes every leaf; width
// soft-wraps block text to that many columns, with 0 disabling wrapping
// (x/ansi.WrapSoft's own no-op posture, which is what makes a non-TTY
// caller's width=0 "just work" with no extra branch here).
//
// A node type this walker does not know how to draw — table, panel,
// mention, media, expand, layout, cards, task/decision lists, and
// anything the schema adds after this was written — becomes a single
// labeled placeholder line rather than an error or a silent drop: the
// substrate's byte-preserving --output=json stays the lossless escape
// hatch for exactly this content, so a placeholder is correct, not a gap.
//
// Render never fails. A malformed document is ParseDocument's problem,
// not Render's — by the time a *Document exists, walking it can only
// ever produce text, in the worst case an empty string.
func Render(doc *Document, s Styler, width int) string {
	if doc == nil {
		return ""
	}
	w := &walker{styler: s, width: width}
	w.blocks(doc.Content(), "")
	return strings.TrimRight(w.buf.String(), "\n")
}

// walker carries one render pass's shared state: the styler every leaf
// goes through, the wrap width every block measures against, and the
// output built so far. blockquote and list rendering recurse through a
// FRESH walker sharing the same styler and width, so a nested render's
// blank-line spacing is built in isolation and only spliced into the
// parent afterward — see renderBlockquote and renderList.
type walker struct {
	styler Styler
	width  int
	buf    strings.Builder
}

// blockRenderer draws one block-level node at the given indent prefix —
// literal leading text (a blockquote's "> ", a list item's marker
// column) already accumulated by the caller. It must not emit a
// trailing blank line of its own; blocks inserts spacing between
// siblings.
type blockRenderer func(w *walker, n *Node, prefix string)

// blockDispatch is the node-type dispatch table: the bulk of a real
// description's content. A type absent here — every deferred node
// Render's doc comment names — falls through to renderPlaceholder.
//
// Built in init(), not a var literal: a literal map of these method
// expressions closes a false initialization cycle in the compiler's
// eyes (renderBlockquote calls blocks, blocks calls block, block reads
// blockDispatch) even though no VALUE actually depends on itself —
// init() runs after ordinary package initialization, which is outside
// that analysis.
var blockDispatch map[NodeType]blockRenderer

func init() {
	blockDispatch = map[NodeType]blockRenderer{
		NodeTypeParagraph:   (*walker).renderParagraph,
		NodeTypeHeading:     (*walker).renderHeading,
		NodeTypeBulletList:  (*walker).renderBulletList,
		NodeTypeOrderedList: (*walker).renderOrderedList,
		NodeTypeCodeBlock:   (*walker).renderCodeBlock,
		NodeTypeBlockquote:  (*walker).renderBlockquote,
		NodeTypeRule:        (*walker).renderRule,
	}
}

// blocks renders nodes as sibling blocks, one blank line apart — the
// spacing every blockRenderer is exempt from producing itself.
func (w *walker) blocks(nodes []Node, prefix string) {
	for i := range nodes {
		if i > 0 {
			w.buf.WriteByte('\n')
		}
		w.block(&nodes[i], prefix)
	}
}

func (w *walker) block(n *Node, prefix string) {
	fn, ok := blockDispatch[NodeType(n.Type())]
	if !ok {
		w.renderPlaceholder(n, prefix)
		return
	}
	fn(w, n, prefix)
}

// writeLine writes one physical line. A blank line never carries prefix
// bytes — otherwise a wrapped paragraph's blank continuation (a
// trailing hardBreak, an empty ADF spacer paragraph) would print
// trailing whitespace nothing else this renderer produces leaves
// behind.
func (w *walker) writeLine(prefix, line string) {
	if line == "" {
		w.buf.WriteByte('\n')
		return
	}
	w.buf.WriteString(prefix)
	w.buf.WriteString(line)
	w.buf.WriteByte('\n')
}

// writeBlock writes segments — already styled, one per hardBreak-
// separated run — as a wrapped block: firstPrefix on the first physical
// line, contPrefix on every line after. The two differ only for a list
// item's first block, where firstPrefix carries the marker and
// contPrefix its same-width blank column; every other caller passes the
// same string for both.
func (w *walker) writeBlock(firstPrefix, contPrefix string, segments []string) {
	if len(segments) == 0 {
		w.writeLine(firstPrefix, "")
		return
	}
	prefix := firstPrefix
	for _, seg := range segments {
		wrapped := ansi.WrapSoft(seg, w.width-ansi.StringWidth(contPrefix))
		for _, line := range strings.Split(wrapped, "\n") {
			w.writeLine(prefix, line)
			prefix = contPrefix
		}
	}
}

func (w *walker) renderParagraph(n *Node, prefix string) {
	w.writeBlock(prefix, prefix, w.inlineSegments(n.Content()))
}

// renderHeading has no dedicated Styler method — Strong is the only
// emphasis primitive available, so a heading is a "#"-prefixed line
// bolded as a whole, applied to every physical line the wrap produces
// (WrapSoft reapplies preserved styles at each break, per x/ansi).
func (w *walker) renderHeading(n *Node, prefix string) {
	segments := w.inlineSegments(n.Content())
	if len(segments) == 0 {
		segments = []string{""}
	}
	segments[0] = strings.Repeat("#", headingLevel(n)) + " " + segments[0]
	for i, seg := range segments {
		segments[i] = w.styler.Strong(seg)
	}
	w.writeBlock(prefix, prefix, segments)
}

// headingLevel reads the heading's level attr, clamped to ADF's 1–6
// range; a node whose attrs fail to decode (malformed, not schema-
// conformant) renders as a level-1 heading rather than losing its
// heading styling entirely.
func headingLevel(n *Node) int {
	h, err := AsHeading(n)
	if err != nil {
		return 1
	}
	switch level := int(h.Attrs.Level); {
	case level < 1:
		return 1
	case level > 6:
		return 6
	default:
		return level
	}
}

// renderCodeBlock preserves internal line breaks exactly, unlike every
// other leaf: a codeBlock's text content is legitimately multi-line
// (Jira encodes it as one "text" node with embedded newlines), so the
// RAW content is split into physical lines FIRST and only then run
// through styler.Text per line — sanitizing the whole blob up front
// would strip those newlines as the H4 single-line-row policy (design:
// "Text feeds single-line rows") requires it to, collapsing the block
// onto one line. Splitting first keeps that same control-character
// stripping (still real defense: a hostile ESC or BEL survives no
// better here) without paying for it with the block's own structure.
func (w *walker) renderCodeBlock(n *Node, prefix string) {
	var raw strings.Builder
	content := n.Content()
	for i := range content {
		c := &content[i]
		if NodeType(c.Type()) == NodeTypeText {
			raw.WriteString(c.Text())
		}
	}
	codePrefix := prefix + "    "
	for _, line := range strings.Split(raw.String(), "\n") {
		w.writeLine(codePrefix, w.styler.Code(w.styler.Text(line)))
	}
}

// renderBlockquote recurses through a fresh walker so the quoted body's
// own block spacing is built in isolation, then splices the result in
// whole — the same shape renderList uses, and for the same reason:
// blocks() would otherwise not know to prefix EVERY line, including
// ones a nested renderer writes many calls later.
func (w *walker) renderBlockquote(n *Node, prefix string) {
	sub := &walker{styler: w.styler, width: w.width}
	sub.blocks(n.Content(), prefix+"> ")
	w.buf.WriteString(sub.buf.String())
}

// renderRule draws a divider spanning the available width, falling back
// to clog's own off-TTY divider width (layout.md: "Dividers use
// terminal width and fall back to 80 columns off-TTY") rather than
// disappearing when width is 0.
func (w *walker) renderRule(_ *Node, prefix string) {
	width := w.width
	if width <= 0 {
		width = 80
	}
	width -= ansi.StringWidth(prefix)
	if width < 1 {
		width = 1
	}
	w.writeLine(prefix, strings.Repeat("─", width))
}

func (w *walker) renderBulletList(n *Node, prefix string) {
	w.renderList(n.Content(), prefix, func(int) string { return "• " })
}

// renderOrderedList numbers from the list's "order" attr when the
// author set a starting number, matching Jira's own restart-numbering
// support, and from 1 otherwise.
func (w *walker) renderOrderedList(n *Node, prefix string) {
	start := 1
	if ol, err := AsOrderedList(n); err == nil && ol.Attrs.Order != nil {
		start = int(*ol.Attrs.Order)
	}
	w.renderList(n.Content(), prefix, func(i int) string {
		return strconv.Itoa(start+i) + ". "
	})
}

// renderList draws bulletList/orderedList's shared shape: each listItem
// child rendered through the normal block machinery at a uniform
// contPrefix indent — so a nested list, code block or extra paragraph
// inside one item indents exactly like a top-level block would, just
// deeper — with the item's own marker OVERLAID onto the first physical
// line afterward. contPrefix and firstPrefix (prefix+marker) are always
// the same visible width, so the overlay is a prefix swap, not a
// re-wrap: the item's own content never needs to know a marker exists.
func (w *walker) renderList(items []Node, prefix string, marker func(i int) string) {
	for i := range items {
		m := marker(i)
		contPrefix := prefix + strings.Repeat(" ", ansi.StringWidth(m))
		sub := &walker{styler: w.styler, width: w.width}
		sub.blocks(items[i].Content(), contPrefix)
		w.buf.WriteString(overlayMarker(sub.buf.String(), contPrefix, prefix+m))
	}
}

// overlayMarker replaces contPrefix's leading bytes on text's first
// line with newPrefix — the marker swap renderList relies on, and
// always newline-terminated so consecutive items never run together.
// TrimPrefix is a no-op when the first line is blank (writeLine's own
// blank-line exception left no contPrefix bytes to find), so an empty
// item still gets its marker rather than losing it to a failed match.
func overlayMarker(text, contPrefix, newPrefix string) string {
	line, rest, found := strings.Cut(text, "\n")
	line = strings.TrimPrefix(line, contPrefix)
	if !found {
		return newPrefix + line + "\n"
	}
	return newPrefix + line + "\n" + rest
}

// inlineSegments renders nodes — a paragraph or heading's inline
// content — into one styled string per hardBreak-separated run, so a
// caller can wrap and place each physical line independently.
func (w *walker) inlineSegments(nodes []Node) []string {
	var segments []string
	var cur strings.Builder
	for i := range nodes {
		n := &nodes[i]
		if NodeType(n.Type()) == NodeTypeHardBreak {
			segments = append(segments, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteString(w.inline(n))
	}
	return append(segments, cur.String())
}

// inline styles one inline node: a text node's sanitized content with
// its marks applied, or a placeholder for anything else the walker has
// no dedicated view for (mention, emoji, date, inlineCard, status,
// mediaInline, ...). Code applies innermost, Link outermost — a link
// wraps whatever styling its own text already carries, matching how
// Jira itself lets a link's text carry other marks.
func (w *walker) inline(n *Node) string {
	if NodeType(n.Type()) != NodeTypeText {
		return w.placeholder(n)
	}
	s := w.styler.Text(n.Text())
	var strong, em, code bool
	var href string
	marks := n.Marks()
	for i := range marks {
		m := marks[i]
		switch MarkType(m.Type()) {
		case MarkTypeStrong:
			strong = true
		case MarkTypeEm:
			em = true
		case MarkTypeCode:
			code = true
		case MarkTypeLink:
			if lm, err := AsLinkMark(&m); err == nil && lm.Attrs.Href != "" {
				href = lm.Attrs.Href
			}
		}
	}
	if code {
		s = w.styler.Code(s)
	}
	if strong {
		s = w.styler.Strong(s)
	}
	if em {
		s = w.styler.Emphasis(s)
	}
	if href != "" {
		// href is Jira-authored (AsLinkMark's Attrs.Href) exactly like a
		// text node's own content, and Link forwards it to a real OSC 8
		// hyperlink escape one layer up — a raw ESC or BEL in href would
		// terminate that sequence early and inject. Route it through the
		// same Styler.Text sanitization the visible text s already went
		// through above, so the untrusted half of the link is clean
		// before Link ever sees it.
		href = w.styler.Text(href)
		s = w.styler.Link(href, s)
	}
	return s
}

func (w *walker) renderPlaceholder(n *Node, prefix string) {
	w.writeLine(prefix, w.placeholder(n))
}

func (w *walker) placeholder(n *Node) string {
	return w.styler.Text(fmt.Sprintf("[%s — see --output=json]", n.Type()))
}
