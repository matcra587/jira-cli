package adf

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"slices"
	"strings"

	xstrings "github.com/gechr/x/strings"
	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdownParser parses GFM with the table, strikethrough, and task-list
// extensions enabled. Enabling the table extension means a pipe table
// is parsed as a real Table node that converts to an ADF table, rather
// than rendering as literal pipe-laden paragraph text; the task-list
// extension parses `- [ ]` / `- [x]` items as real checkboxes that
// convert to ADF taskList/taskItem nodes. Only the parser is taken — the
// converter walks the AST itself and no goldmark renderer is wired.
var markdownParser = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
	goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(statusDirectiveParser{}, 500))),
).Parser()

// newLocalID generates the localId attr ADF requires on every
// taskList/taskItem node. A package variable so tests can pin
// deterministic ids.
var newLocalID = func() string { return uuid.NewString() }

// codeBlockAttrs is deliberately NOT the generated CodeBlockAttrs: that
// struct tags Language `omitempty`, so an unhinted fence would serialize
// as an empty attrs object — and Jira renders an attr-less codeBlock with
// its default language (java), silently java-highlighting arbitrary
// output. The language key must be present even when empty.
type codeBlockAttrs struct {
	Language string `json:"language"`
}

// linkMarkAttrs carries a link mark's href. The generated typed layer
// covers nodes only; marks have capability specs but no attr structs, so
// the mark shapes this converter authors are spelled out here.
type linkMarkAttrs struct {
	Href  string `json:"href"`
	Title string `json:"title,omitempty"`
}

// FromMarkdownLossy converts GFM Markdown to an ADF document and reports
// every Markdown construct it could not represent faithfully in the
// supported ADF node set. Each unsupported construct yields one Warning
// (lossy=true) naming the construct and its source position so callers —
// and strict-mode abort gates — can act on the content loss instead of
// letting it slip through silently.
//
// It returns *Document to match ParseDocument and the rest of the
// substrate, whose methods are all pointer-receiver.
func FromMarkdownLossy(markdown string) (*Document, []Warning, error) {
	return fromMarkdownWithReferences(markdown, nil)
}

// Editing converts separate anchored blocks, but Markdown link references
// have document scope. Seed a fresh parser context for each block so references
// outside that block resolve without sharing mutable parser state.
func fromMarkdownWithReferences(markdown string, references []parser.Reference) (*Document, []Warning, error) {
	var dialectWarnings []Warning
	wikiEmoji := false
	if detectDialect(markdown) == dialectWiki {
		normalized := normalizeWikiMarkup(markdown)
		markdown = normalized.text
		wikiEmoji = true
		dialectWarnings = append(dialectWarnings, wikiNormalizationWarning(normalized.constructs))
	}

	source := []byte(markdown)
	reader := text.NewReader(source)
	pc := parser.NewContext()
	for _, reference := range references {
		pc.AddReference(reference)
	}
	root := markdownParser.Parse(reader, parser.WithContext(pc))

	conv := &mdConverter{source: source, wikiEmoji: wikiEmoji, warnings: dialectWarnings}
	var content []Node
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		if node, ok := conv.block(child); ok {
			content = append(content, node)
		}
		// Blocks hoisted out of forbidden nesting (tables in list items
		// or blockquotes) land right after the block that contained them.
		if len(conv.hoisted) > 0 {
			content = append(content, conv.hoisted...)
			conv.hoisted = nil
		}
	}
	if len(content) == 0 && !xstrings.IsBlank(markdown) && len(conv.warnings) == 0 && len(pc.References()) == 0 {
		content = append(content, conv.node(NodeTypeParagraph, nil, []Node{
			conv.text(strings.TrimSpace(markdown), nil),
		}))
	}
	if conv.err != nil {
		return nil, nil, conv.err
	}

	doc := NewDocument()
	// The stable schema requires content even when every input construct was
	// dropped. An explicit empty array keeps best-effort candidates valid.
	doc.SetContent(content)
	return doc, conv.warnings, nil
}

// mdConverter carries the Markdown source plus the running warning list
// through the recursive goldmark walk. wikiEmoji is set when the input was
// normalized from Jira wiki markup, enabling emoticon-shortcut expansion in
// plain text runs — never inside code spans or code blocks, which take
// different paths through the walk.
type mdConverter struct {
	source    []byte
	warnings  []Warning
	wikiEmoji bool
	// hoisted collects block nodes that converted cleanly but sat in a
	// position ADF forbids (a table inside a list item or blockquote).
	// FromMarkdownLossy drains it after each top-level block, so the
	// node re-enters the document at the nearest valid position.
	hoisted []Node
	// inTaskItem is set while converting a task item's inline content,
	// where the leading GFM checkbox is consumed by the taskItem's state
	// attr. Outside a task item (a checkbox in a mixed list) the checkbox
	// degrades to literal text instead.
	inTaskItem bool
	// err holds the first attribute-encoding or text-decoding failure.
	// Attribute encoding only fails for values encoding/json cannot represent,
	// but swallowing either error could ship an incomplete document. Carry
	// the error to the exit instead of threading it through every recursive
	// construction and conversion site.
	err error
}

// node builds one substrate node. attrs is nil for a node that carries
// none; children are attached only when non-empty, because SetContent on
// an empty slice emits "content":[] and several ADF shapes (an empty
// codeBlock, the empty paragraph in a blank table cell) must carry no
// content key at all.
func (c *mdConverter) node(typ NodeType, attrs any, children []Node) Node {
	n := NewNode(string(typ))
	if attrs != nil {
		if err := n.SetAttrs(attrs); err != nil && c.err == nil {
			c.err = err
		}
	}
	if len(children) > 0 {
		n.SetContent(children)
	}
	return *n
}

// text builds a text node carrying marks. Marks are cloned so a caller's
// accumulating slice cannot alias into a finished node.
func (c *mdConverter) text(s string, marks []Mark) Node {
	n := NewNode(string(NodeTypeText))
	n.SetText(s)
	if len(marks) > 0 {
		n.SetMarks(cloneMarks(marks))
	}
	return *n
}

// mark builds one inline mark. attrs is nil for the bare decorative marks
// (strong, em, strike, code).
func (c *mdConverter) mark(typ MarkType, attrs any) Mark {
	m := NewMark(string(typ))
	if attrs != nil {
		if err := m.SetAttrs(attrs); err != nil && c.err == nil {
			c.err = err
		}
	}
	return *m
}

// warn records one lossy-conversion warning naming an unsupported
// Markdown construct, source-mapped to the offending input when n carries
// position information.
func (c *mdConverter) warn(n ast.Node, construct, detail string) {
	msg := fmt.Sprintf("Markdown %s is not supported and was dropped during ADF conversion", construct)
	if detail != "" {
		msg += ": " + detail
	}
	c.warnings = append(c.warnings, c.sourceMapped(n, Warning{
		Type:     "markdown_lossy_conversion",
		Message:  msg,
		NodeType: construct,
		Lossy:    true,
	}))
}

// warnDowngrade records a non-lossy downgrade notice: the content survives
// in a different shape, so strict mode does not abort on it.
func (c *mdConverter) warnDowngrade(n ast.Node, construct, resolution string) {
	c.warnings = append(c.warnings, c.sourceMapped(n, Warning{
		Type:     "markdown_lossy_conversion",
		Message:  fmt.Sprintf("Markdown %s was downgraded during ADF conversion: %s", construct, resolution),
		NodeType: construct,
		Lossy:    false,
	}))
}

// sourceMapped appends the 1-based source position and the offending line
// to w so a diagnostic points at the Markdown the author wrote, not a JSON
// path into a document they never saw. A node with no resolvable position
// (synthesized, or an empty document) leaves w unchanged.
func (c *mdConverter) sourceMapped(n ast.Node, w Warning) Warning {
	off := nodeOffset(n)
	if off < 0 || off > len(c.source) {
		return w
	}
	line := 1 + bytes.Count(c.source[:off], []byte("\n"))
	lineStart := bytes.LastIndexByte(c.source[:off], '\n') + 1
	col := off - lineStart + 1
	w.Path = fmt.Sprintf("line %d, col %d", line, col)
	if snippet := sourceLine(c.source, lineStart); snippet != "" {
		w.Message += fmt.Sprintf(" (line %d, col %d: %q)", line, col, snippet)
	} else {
		w.Message += fmt.Sprintf(" (line %d, col %d)", line, col)
	}
	return w
}

// nodeOffset resolves the byte offset of n's first piece of source text:
// block nodes carry line segments, text nodes carry their own segment, and
// containers defer to their first positioned child. -1 means no position.
func nodeOffset(n ast.Node) int {
	if n == nil {
		return -1
	}
	// Lines() is only defined for block nodes; goldmark's inline base
	// panics on it.
	if n.Type() == ast.TypeBlock {
		if lines := n.Lines(); lines != nil && lines.Len() > 0 {
			return lines.At(0).Start
		}
	}
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Start
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if off := nodeOffset(child); off >= 0 {
			return off
		}
	}
	// Childless inline nodes such as autolinks and raw HTML still carry a
	// parser-assigned position in Goldmark 1.8.6. Preserve that source mapping
	// when no line or child text segment can establish one.
	return n.Pos()
}

// sourceLine returns the trimmed source line starting at lineStart, capped
// so a pathological input cannot balloon a warning message.
func sourceLine(source []byte, lineStart int) string {
	end := bytes.IndexByte(source[lineStart:], '\n')
	if end < 0 {
		end = len(source) - lineStart
	}
	const maxSnippet = 80
	line := strings.TrimSpace(string(source[lineStart : lineStart+end]))
	if len(line) > maxSnippet {
		line = line[:maxSnippet] + "…"
	}
	return line
}

// block converts one block-level goldmark node to an ADF block node.
// Returns ok=false when the node produced nothing (e.g. an unsupported
// construct, which is recorded as a warning instead).
func (c *mdConverter) block(n ast.Node) (Node, bool) {
	switch n.Kind() {
	case ast.KindParagraph, ast.KindTextBlock:
		return c.node(NodeTypeParagraph, nil, c.inlineChildren(n)), true
	case ast.KindHeading:
		h, ok := n.(*ast.Heading)
		if !ok {
			return Node{}, false
		}
		attrs := HeadingAttrs{Level: float64(h.Level)}
		return c.node(NodeTypeHeading, attrs, c.inlineChildren(n)), true
	case ast.KindList:
		list, ok := n.(*ast.List)
		if !ok {
			return Node{}, false
		}
		return c.list(list)
	case ast.KindCodeBlock, ast.KindFencedCodeBlock:
		return c.codeBlock(n), true
	case ast.KindBlockquote:
		if panel, ok := c.alert(n); ok {
			return panel, true
		}
		return c.node(NodeTypeBlockquote, nil, c.blockquoteChildren(n)), true
	case ast.KindThematicBreak:
		return c.node(NodeTypeRule, nil, nil), true
	case ast.KindHTMLBlock:
		c.warn(n, "raw HTML block", "")
		return Node{}, false
	case ast.KindLinkReferenceDefinition:
		// Goldmark surfaces reference definitions as block nodes. They are
		// parser metadata; resolved links already carry the href.
		return Node{}, false
	case extast.KindTable:
		return c.table(n), true
	default:
		c.warn(n, "block "+n.Kind().String(), "")
		return Node{}, false
	}
}

// blockquoteChildren converts a blockquote's children. The schema
// restricts blockquote children to paragraphs, lists, and code blocks, so
// other quoted constructs degrade: a nested quote is flattened into its
// parent, and a quoted heading becomes a paragraph. Both keep the content
// and report a non-lossy downgrade.
func (c *mdConverter) blockquoteChildren(n ast.Node) []Node {
	var out []Node
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case ast.KindBlockquote:
			c.warnDowngrade(child, "nested blockquote", "flattened into its parent blockquote")
			out = append(out, c.blockquoteChildren(child)...)
		case extast.KindTable:
			// ADF forbids a table inside a blockquote; the table itself
			// converts cleanly, so it moves after the enclosing block.
			c.warnDowngrade(child, "table inside a blockquote", "moved after the enclosing block")
			if node, ok := c.block(child); ok {
				c.hoisted = append(c.hoisted, node)
			}
		case ast.KindHeading:
			c.warnDowngrade(child, "heading inside a blockquote", "converted to a paragraph")
			out = append(out, c.node(NodeTypeParagraph, nil, c.inlineChildren(child)))
		default:
			if node, ok := c.block(child); ok {
				out = append(out, node)
			}
		}
	}
	return out
}

// list converts a bullet or ordered list, recursing into list items. An
// unordered list whose every item leads with a GFM task checkbox becomes an
// ADF taskList; any other list with checkboxes keeps its shape and the
// checkboxes degrade to literal text (ADF forbids taskItem outside a pure
// taskList), reported as a non-lossy downgrade.
func (c *mdConverter) list(list *ast.List) (Node, bool) {
	pure, hasCheckboxes := taskListShape(list)
	if pure {
		return c.taskList(list), true
	}
	if hasCheckboxes {
		c.warnDowngrade(list, "task items in a mixed or ordered list", "checkboxes rendered as literal text")
	}
	// The items of a non-task list convert outside any surrounding task
	// context: their checkboxes (if any) must degrade to text, not vanish.
	prev := c.inTaskItem
	c.inTaskItem = false
	defer func() { c.inTaskItem = prev }()

	nodeType := NodeTypeBulletList
	var attrs any
	if list.IsOrdered() {
		nodeType = NodeTypeOrderedList
		if list.Start > 1 {
			order := float64(list.Start)
			attrs = OrderedListAttrs{Order: &order}
		}
	}
	var items []Node
	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() != ast.KindListItem {
			continue
		}
		items = append(items, c.node(NodeTypeListItem, nil, c.listItemChildren(child)))
	}
	return c.node(nodeType, attrs, items), true
}

// taskListShape reports whether a list is a pure task list (every item
// leads with a GFM checkbox — convertible to ADF taskList) and whether any
// item carries a checkbox at all. Ordered lists are never task lists: ADF
// task lists are inherently unordered.
func taskListShape(list *ast.List) (pure, hasCheckboxes bool) {
	if list.FirstChild() == nil {
		return false, false
	}
	pure = !list.IsOrdered()
	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() != ast.KindListItem {
			continue
		}
		if taskCheckbox(child) != nil {
			hasCheckboxes = true
		} else {
			pure = false
		}
	}
	return pure && hasCheckboxes, hasCheckboxes
}

// taskCheckbox returns the GFM checkbox leading a list item's first
// paragraph, or nil when the item is not a task item.
func taskCheckbox(item ast.Node) *extast.TaskCheckBox {
	first := item.FirstChild()
	if first == nil || (first.Kind() != ast.KindTextBlock && first.Kind() != ast.KindParagraph) {
		return nil
	}
	if cb, ok := first.FirstChild().(*extast.TaskCheckBox); ok {
		return cb
	}
	return nil
}

// taskList converts a pure GFM task list to the ADF taskList shape:
// taskItem children carrying the checkbox state, with nested task lists
// as taskList siblings (ADF nests them inside the parent taskList, not
// inside the inline-only taskItem).
func (c *mdConverter) taskList(list *ast.List) Node {
	var items []Node
	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() != ast.KindListItem {
			continue
		}
		items = append(items, c.taskItemNodes(child)...)
	}
	return c.node(NodeTypeTaskList, TaskListAttrs{LocalID: newLocalID()}, items)
}

// taskItemNodes converts one GFM task item into an ADF taskItem plus any
// sibling nodes that must live beside it in the parent taskList. The
// taskItem is inline-only: extra paragraphs join its inline run after a
// hardBreak, a nested pure task list becomes a taskList sibling, and any
// other block content is hoisted after the enclosing list with a
// downgrade warning — the content survives, only its nesting moves.
func (c *mdConverter) taskItemNodes(item ast.Node) []Node {
	state := "TODO"
	if cb := taskCheckbox(item); cb != nil && cb.IsChecked {
		state = "DONE"
	}
	var inline, siblings []Node
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case ast.KindTextBlock, ast.KindParagraph:
			run := c.taskItemInline(child)
			if len(inline) > 0 && len(run) > 0 {
				inline = append(inline, c.node(NodeTypeHardBreak, nil, nil))
			}
			inline = append(inline, run...)
		case ast.KindList:
			nested, ok := child.(*ast.List)
			if !ok {
				continue
			}
			if pure, _ := taskListShape(nested); pure {
				siblings = append(siblings, c.taskList(nested))
				continue
			}
			// ADF taskList children are task nodes only; a non-task nested
			// list converts cleanly, so it moves after the enclosing list.
			c.warnDowngrade(child, "list nested under a task item", "moved after the enclosing task list")
			if node, ok := c.list(nested); ok {
				c.hoisted = append(c.hoisted, node)
			}
		default:
			c.warnDowngrade(child, "block content inside a task item", "moved after the enclosing task list")
			if node, ok := c.block(child); ok {
				c.hoisted = append(c.hoisted, node)
			}
		}
	}
	attrs := TaskItemAttrs{LocalID: newLocalID(), State: state}
	return append([]Node{c.node(NodeTypeTaskItem, attrs, inline)}, siblings...)
}

// taskItemInline converts a task item paragraph's inline children with the
// leading checkbox consumed (it became the taskItem's state attr).
func (c *mdConverter) taskItemInline(n ast.Node) []Node {
	prev := c.inTaskItem
	c.inTaskItem = true
	defer func() { c.inTaskItem = prev }()
	return c.inlineChildren(n)
}

// listItemChildren converts a list item's children. A list item's
// content must be block nodes (paragraph, nested list, codeBlock); a
// bare text run is wrapped in a paragraph.
func (c *mdConverter) listItemChildren(item ast.Node) []Node {
	var out []Node
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case ast.KindTextBlock, ast.KindParagraph:
			out = append(out, c.node(NodeTypeParagraph, nil, c.inlineChildren(child)))
		case ast.KindList:
			nested, ok := child.(*ast.List)
			if !ok {
				continue
			}
			if node, ok := c.list(nested); ok {
				out = append(out, node)
			}
		case ast.KindCodeBlock, ast.KindFencedCodeBlock:
			out = append(out, c.codeBlock(child))
		case ast.KindBlockquote:
			// ADF list items cannot contain blockquotes. Hoist the quoted
			// blocks into the list item so the content survives — this is
			// exactly the "- >50 keys" shape, where Markdown parses the
			// remainder of a list item as a nested quote.
			c.warnDowngrade(child, "blockquote inside a list item", "quoted content hoisted into the list item")
			out = append(out, c.blockquoteChildren(child)...)
		case extast.KindTable:
			// ADF forbids a table inside a list item — a common Markdown
			// shape (a status bullet followed by its indented table). The
			// table converts cleanly, so it moves after the enclosing list.
			c.warnDowngrade(child, "table inside a list item", "moved after the enclosing list")
			if node, ok := c.block(child); ok {
				c.hoisted = append(c.hoisted, node)
			}
		default:
			if node, ok := c.block(child); ok {
				out = append(out, node)
			}
		}
	}
	return out
}

// codeBlock converts an indented or fenced code block. The language attr is
// always present — empty string when the fence has no hint — because Jira
// renders an attr-less codeBlock with its default language (java) instead of
// plain text. An empty block carries no content array at all: the ADF schema
// requires text nodes to be non-empty, so an empty text child would be
// rejected. Trailing newlines are trimmed — the fence's closing newline is
// Markdown syntax, not code, and Jira renders it as a spurious blank line.
func (c *mdConverter) codeBlock(n ast.Node) Node {
	lang := ""
	if fenced, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(fenced.Language(c.source))
	}
	var children []Node
	if body := strings.TrimRight(codeBlockText(n, c.source), "\n"); body != "" {
		children = []Node{c.text(body, nil)}
	}
	return c.node(NodeTypeCodeBlock, codeBlockAttrs{Language: lang}, children)
}

// table converts a GFM table to the ADF table shape: a tableRow of
// tableHeader cells, then tableCell rows, with the standard table attrs.
// Every cell wraps its inline run in a paragraph — tableCell/tableHeader
// require at least one block child, and an empty GFM cell becomes an
// empty paragraph, which the schema permits.
func (c *mdConverter) table(n ast.Node) Node {
	var rows []Node
	// In goldmark, TableHeader directly contains TableCell children (no
	// wrapping TableRow), while TableRow does contain TableCells.
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case extast.KindTableHeader:
			rows = append(rows, c.tableRow(child, NodeTypeTableHeader))
		case extast.KindTableRow:
			rows = append(rows, c.tableRow(child, NodeTypeTableCell))
		}
	}
	numberColumn := false
	attrs := TableAttrs{IsNumberColumnEnabled: &numberColumn, Layout: "default"}
	return c.node(NodeTypeTable, attrs, rows)
}

// tableRow converts one goldmark header or body row into an ADF tableRow
// whose cells carry cellType ("tableHeader" or "tableCell").
func (c *mdConverter) tableRow(row ast.Node, cellType NodeType) Node {
	var cells []Node
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if cell.Kind() != extast.KindTableCell {
			continue
		}
		paragraph := c.node(NodeTypeParagraph, nil, c.inlineChildren(cell))
		cells = append(cells, c.node(cellType, nil, []Node{paragraph}))
	}
	return c.node(NodeTypeTableRow, nil, cells)
}

// inlineChildren converts the inline children of a block node to ADF
// inline nodes, threading marks down onto leaf text nodes.
func (c *mdConverter) inlineChildren(n ast.Node) []Node {
	return c.inlineRun(n, nil)
}

// inline converts one inline goldmark node, applying the accumulated
// marks to every leaf text node it produces.
func (c *mdConverter) inline(n ast.Node, marks []Mark) []Node {
	switch n.Kind() {
	case kindStatusDirective:
		return c.statusDirective(n.(*statusDirective), marks)
	case ast.KindText:
		return c.inlineText(n, marks)
	case ast.KindString:
		s, ok := n.(*ast.String)
		if !ok {
			return nil
		}
		return []Node{c.text(string(s.Value), marks)}
	case ast.KindEmphasis:
		em, ok := n.(*ast.Emphasis)
		if !ok {
			return nil
		}
		markType := MarkTypeEm
		if em.Level == 2 {
			markType = MarkTypeStrong
		}
		return c.inlineRun(n, append(cloneMarks(marks), c.mark(markType, nil)))
	case ast.KindLink:
		link, ok := n.(*ast.Link)
		if !ok {
			return nil
		}
		mark := c.mark(MarkTypeLink, linkMarkAttrs{Href: string(link.Destination), Title: string(link.Title)})
		return c.inlineRun(n, append(cloneMarks(marks), mark))
	case ast.KindCodeSpan:
		return c.codeSpan(n, marks)
	case ast.KindAutoLink:
		al, ok := n.(*ast.AutoLink)
		if !ok {
			return nil
		}
		url := string(al.URL(c.source))
		mark := c.mark(MarkTypeLink, linkMarkAttrs{Href: url})
		return []Node{c.text(url, append(cloneMarks(marks), mark))}
	case extast.KindStrikethrough:
		return c.inlineRun(n, append(cloneMarks(marks), c.mark(MarkTypeStrike, nil)))
	case extast.KindTaskCheckBox:
		return c.checkbox(n, marks)
	case ast.KindImage:
		img, ok := n.(*ast.Image)
		if !ok {
			return nil
		}
		return c.image(img, marks)
	case ast.KindRawHTML:
		c.warn(n, "inline raw HTML", "")
		return nil
	default:
		c.warn(n, "inline "+n.Kind().String(), "")
		return nil
	}
}

// inlineText converts a plain text run, including the two line-break
// shapes goldmark hangs off it.
func (c *mdConverter) inlineText(n ast.Node, marks []Mark) []Node {
	t, ok := n.(*ast.Text)
	if !ok {
		return nil
	}
	nodes := []Node{}
	seg, err := markdownText(t.Segment.Value(c.source))
	if err != nil && c.err == nil {
		c.err = err
	}
	if seg != "" {
		if split := c.wikiEmojiNodes(seg, marks); split != nil {
			nodes = append(nodes, split...)
		} else {
			nodes = append(nodes, c.text(seg, marks))
		}
	}
	// A trailing hard break inside a paragraph is a real ADF node.
	if t.HardLineBreak() {
		return append(nodes, c.node(NodeTypeHardBreak, nil, nil))
	}
	if t.SoftLineBreak() {
		// Soft breaks render as a space in ADF inline flow. When the run
		// ends on an emoji node the space needs its own text node — an
		// emoji carries no top-level text.
		if last := len(nodes) - 1; last >= 0 {
			if nodes[last].Type() == string(NodeTypeText) {
				nodes[last].SetText(nodes[last].Text() + " ")
			} else {
				nodes = append(nodes, c.text(" ", nil))
			}
		}
	}
	return nodes
}

// markdownText uses the selected parser's text writer to resolve CommonMark
// escapes and character references in one pass. Its result contains only
// HTML-escaped text, so removing that output escaping once recovers the literal
// ADF text. Composing UnescapePunctuations and ResolveEntityNames would wrongly
// decode escaped ampersands and references introduced by an earlier decode.
// Code spans and blocks retain their separate raw-text paths.
func markdownText(source []byte) (string, error) {
	var escaped strings.Builder
	writer := bufio.NewWriter(&escaped)
	goldmarkhtml.DefaultWriter.Write(writer, source)
	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("adf: decode Markdown text: %w", err)
	}
	return html.UnescapeString(escaped.String()), nil
}

// codeSpan converts an inline code run. The ADF spec allows the code mark
// to combine only with link. Resolve the conflict here, where the Markdown
// source is still at hand: keep code (and any link) and drop the
// decorative marks. The text and its code mark survive verbatim — like an
// image degrading to its alt-text link, this loses decoration, not
// content — so the warning is a non-lossy downgrade and the default strict
// mutation mode proceeds with it.
func (c *mdConverter) codeSpan(n ast.Node, marks []Mark) []Node {
	kept, dropped := sanitizeCodeMarks(cloneMarks(marks))
	if len(dropped) > 0 {
		c.warnings = append(c.warnings, c.sourceMapped(n, Warning{
			Type:     "markdown_lossy_conversion",
			Message:  fmt.Sprintf("Markdown formatting cannot combine with inline code in ADF (code combines only with link); kept the code mark and dropped %s", strings.Join(dropped, ", ")),
			NodeType: "codeSpan",
			MarkType: dropped[0],
			Lossy:    false,
		}))
	}
	return []Node{c.text(codeSpanText(n, c.source), append(kept, c.mark(MarkTypeCode, nil)))}
}

// checkbox handles a GFM task checkbox. Inside a task item the checkbox
// became the taskItem state attr; in a mixed list it degrades to the
// literal GFM text (the list carries one downgrade warning).
func (c *mdConverter) checkbox(n ast.Node, marks []Mark) []Node {
	if c.inTaskItem {
		return nil
	}
	cb, ok := n.(*extast.TaskCheckBox)
	if !ok {
		return nil
	}
	// The literal carries the separator space: goldmark strips the space
	// between "]" and the item text from the following segment.
	box := "[ ] "
	if cb.IsChecked {
		box = "[x] "
	}
	return []Node{c.text(box, marks)}
}

// sanitizeCodeMarks applies the ADF mark-combination rule at conversion
// time: the code mark combines only with link, so every other accumulated
// mark is dropped. Returns the surviving marks and the dropped mark types.
func sanitizeCodeMarks(marks []Mark) ([]Mark, []string) {
	var kept []Mark
	var dropped []string
	for _, m := range marks {
		if m.Type() == string(MarkTypeLink) {
			kept = append(kept, m)
			continue
		}
		dropped = append(dropped, m.Type())
	}
	return kept, dropped
}

// image degrades a Markdown image to its alt text (the URL when the alt is
// empty) carrying a link mark to the image URL. ADF cannot embed external
// images by URL — media nodes need attachment IDs — so the clickable
// reference is the faithful fallback. The downgrade is reported with
// Lossy=false: the reference survives in full, so a strict mutation is not
// aborted (only silently-dropped content trips the strict gate).
func (c *mdConverter) image(img *ast.Image, marks []Mark) []Node {
	url := string(img.Destination)
	if url == "" {
		c.warn(img, "image", "image with no URL dropped")
		return nil
	}
	alt := imageAltText(img, c.source)
	if alt == "" {
		alt = url
	}
	c.warnDowngrade(img, "image", "rendered as a link to "+url)
	mark := c.mark(MarkTypeLink, linkMarkAttrs{Href: url})
	return []Node{c.text(alt, append(cloneMarks(marks), mark))}
}

// imageAltText collects the image's child text runs (the [alt] part).
func imageAltText(img *ast.Image, source []byte) string {
	var b strings.Builder
	for child := img.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
		}
	}
	return strings.TrimSpace(b.String())
}

// inlineRun converts the children of an inline container (emphasis,
// link) and threads marks onto every produced leaf.
func (c *mdConverter) inlineRun(n ast.Node, marks []Mark) []Node {
	var out []Node
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		// Colon trigger splits. Goldmark segment/break rules.
		if run, ok := child.(*ast.Text); ok {
			for next := child.NextSibling(); next != nil && run.Merge(next, c.source); next = child.NextSibling() {
				n.RemoveChild(n, next)
			}
		}
		out = append(out, c.inline(child, marks)...)
	}
	return out
}

func cloneMarks(marks []Mark) []Mark {
	if len(marks) == 0 {
		return nil
	}
	return slices.Clone(marks)
}

func codeBlockText(n ast.Node, source []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := range lines.Len() {
		segment := lines.At(i)
		b.Write(segment.Value(source))
	}
	return b.String()
}

func codeSpanText(n ast.Node, source []byte) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
		}
	}
	return b.String()
}
