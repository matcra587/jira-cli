package adf

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gechr/x/set"
)

// MarkdownProjection is a review artifact. Loss-free panels and status
// paragraphs are structurally checked against Markdown conversion; arbitrary
// documents are not round trips.
// LossyConstructs names node/mark types whose semantics, attributes or unknown
// fields have no equivalent in the projection. The source remains untouched.
type MarkdownProjection struct {
	Markdown        string
	LossyConstructs []string
}

// ToMarkdownLossy projects native ADF to GFM and reports approximated constructs.
// Unknown nodes retain their child content beside a labeled placeholder.
func ToMarkdownLossy(doc *Document) MarkdownProjection {
	p := markdownProjection{losses: set.New[string]()}
	if doc == nil {
		return MarkdownProjection{LossyConstructs: []string{}}
	}
	p.inspect(&doc.Node, nil)
	if version, err := doc.Version(); err != nil || version != 1 {
		p.losses.Add(doc.Type())
	}
	text := p.blocks(doc.Content(), true)
	if text != "" {
		text += "\n"
	}
	return MarkdownProjection{Markdown: text, LossyConstructs: set.Sorted(p.losses)}
}

type markdownProjection struct {
	losses set.Set[string]
}

func (p *markdownProjection) blocks(nodes []Node, topLevel bool) string {
	var blocks []string
	for i := range nodes {
		if text := p.node(&nodes[i], topLevel); text != "" {
			blocks = append(blocks, text)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (p *markdownProjection) inline(nodes []Node) string {
	var text strings.Builder
	for i := 0; i < len(nodes); {
		end := i + 1
		precedingStatus := false
		if NodeType(nodes[i].Type()) == NodeTypeText && len(nodes[i].Marks()) == 0 {
			// The whole plain run must hide URI delimiters, including ones in
			// an earlier sibling. The full paragraph proof catches merged runs.
			for end < len(nodes) && NodeType(nodes[end].Type()) == NodeTypeText && len(nodes[end].Marks()) == 0 {
				end++
			}
			precedingStatus = end < len(nodes) && NodeType(nodes[end].Type()) == NodeTypeStatus
		}
		for j := i; j < end; j++ {
			rendered := p.node(&nodes[j], false)
			if precedingStatus {
				rendered = statusPrecedingText(nodes[j].Text())
			}
			text.WriteString(rendered)
		}
		i = end
	}
	return text.String()
}

// inspect reports attributes and extension fields that are not represented.
// Unlike schema validation, this accepts future server nodes for review.
func (p *markdownProjection) inspect(n *Node, carried []string) map[string]json.RawMessage {
	attrs := map[string]json.RawMessage{}
	if raw := n.rawAttrs(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &attrs); err != nil {
			p.losses.Add(n.Type())
		}
	}
	for key := range attrs {
		if !slices.Contains(carried, key) {
			p.losses.Add(n.Type())
		}
	}
	for _, field := range n.fields {
		switch field.key {
		case "type", "text", "content", "attrs", "marks", "version":
		default:
			p.losses.Add(n.Type())
		}
	}
	return attrs
}

func (p *markdownProjection) node(n *Node, topLevel bool) string {
	var carried []string
	switch NodeType(n.Type()) {
	case NodeTypeHeading:
		carried = []string{"level"}
	case NodeTypeOrderedList:
		carried = []string{"order"}
	case NodeTypePanel:
		carried = []string{"panelType"}
	case NodeTypeCodeBlock:
		carried = []string{"language"}
	case NodeTypeStatus:
		carried = []string{"text", "color", "localId", "style"}
	}
	attrs := p.inspect(n, carried)
	if n.Type() != string(NodeTypeText) {
		for _, mark := range n.Marks() {
			p.losses.Add(mark.Type())
		}
	}
	switch NodeType(n.Type()) {
	case NodeTypeParagraph:
		if slices.ContainsFunc(n.Content(), func(child Node) bool { return NodeType(child.Type()) == NodeTypeStatus }) {
			children := markdownProjection{losses: set.New[string]()}
			text := children.inline(n.Content())
			if projectionBlockEqual(n, text) {
				return text
			}
			p.losses.Add(n.Type())
			maps.Copy(p.losses, children.losses)
			return text
		}
		text := p.inline(n.Content())
		if text == "" {
			p.losses.Add(n.Type()) // Markdown cannot encode an empty paragraph.
		}
		return text
	case NodeTypeText:
		return p.text(n)
	case NodeTypeHeading:
		level := 1
		if raw, ok := attrs["level"]; ok {
			if err := json.Unmarshal(raw, &level); err != nil || level < 1 || level > 6 {
				p.losses.Add(n.Type())
				level = 1
			}
		}
		return strings.Repeat("#", level) + " " + p.inline(n.Content())
	case NodeTypeHardBreak:
		return "  \n"
	case NodeTypeRule:
		return "---"
	case NodeTypeBulletList, NodeTypeOrderedList:
		start := 1
		if raw, ok := attrs["order"]; ok {
			if err := json.Unmarshal(raw, &start); err != nil || start < 1 {
				p.losses.Add(n.Type())
				start = 1
			}
		}
		var items []string
		for i, child := range n.Content() {
			marker := "- "
			if NodeType(n.Type()) == NodeTypeOrderedList {
				marker = strconv.Itoa(start+i) + ". "
			}
			text := p.node(&child, false)
			items = append(items, marker+strings.ReplaceAll(text, "\n", "\n"+strings.Repeat(" ", len(marker))))
		}
		return strings.Join(items, "\n")
	case NodeTypeListItem:
		return p.blocks(n.Content(), false)
	case NodeTypeBlockquote:
		return quoteMarkdown(p.blocks(n.Content(), false))
	case NodeTypeCodeBlock:
		var text strings.Builder
		for _, child := range n.Content() {
			p.inspect(&child, nil)
			if NodeType(child.Type()) != NodeTypeText || len(child.Marks()) != 0 {
				p.losses.Add(n.Type())
			}
			text.WriteString(child.Text())
		}
		language := projectionAttr(attrs, "language")
		if strings.ContainsAny(language, "`\r\n") {
			p.losses.Add(n.Type())
			language = ""
		}
		fence := strings.Repeat("`", max(3, longestBackticks(text.String())+1))
		return fence + language + "\n" + text.String() + "\n" + fence
	case NodeTypePanel:
		return p.panel(n, attrs, topLevel)
	case NodeTypeMention:
		p.losses.Add(n.Type())
		name := projectionAttr(attrs, "text")
		if name == "" {
			name = projectionAttr(attrs, "id")
		}
		return "@" + escapeMarkdown(strings.TrimPrefix(name, "@"))
	case NodeTypeEmoji:
		p.losses.Add(n.Type())
		text := projectionAttr(attrs, "text")
		if text == "" {
			text = projectionAttr(attrs, "shortName")
		}
		return escapeMarkdown(text)
	case NodeTypeStatus:
		return p.status(n, attrs, topLevel)
	case NodeTypeInlineCard:
		p.losses.Add(n.Type())
		return "[" + escapeMarkdown(projectionAttr(attrs, "url")) + "](" + markdownDestination(projectionAttr(attrs, "url")) + ")"
	case NodeTypeTaskList, NodeTypeDecisionList:
		p.losses.Add(n.Type())
		var items []string
		for _, child := range n.Content() {
			items = append(items, "- "+strings.ReplaceAll(p.node(&child, false), "\n", "\n  "))
		}
		return strings.Join(items, "\n")
	case NodeTypeTaskItem, NodeTypeBlockTaskItem:
		p.losses.Add(n.Type())
		box := "[ ] "
		if projectionAttr(attrs, "state") == "DONE" {
			box = "[x] "
		}
		if NodeType(n.Type()) == NodeTypeBlockTaskItem {
			return box + p.blocks(n.Content(), false)
		}
		return box + p.inline(n.Content())
	case NodeTypeDecisionItem:
		p.losses.Add(n.Type())
		return "\\<\\> " + p.inline(n.Content())
	case NodeTypeTable:
		p.losses.Add(n.Type())
		return p.table(n)
	case NodeTypeMediaSingle, NodeTypeMediaGroup:
		p.losses.Add(n.Type())
		return p.blocks(n.Content(), false)
	case NodeTypeMedia:
		p.losses.Add(n.Type())
		label := projectionAttr(attrs, "alt")
		if label == "" {
			label = projectionAttr(attrs, "id")
		}
		return "\\[attachment: " + escapeMarkdown(label) + "\\]"
	default:
		p.losses.Add(n.Type())
		return "\\[" + escapeMarkdown(n.Type()) + "\\]" + p.blocks(n.Content(), false)
	}
}

func (p *markdownProjection) text(n *Node) string {
	// Markdown parsing can trim boundary whitespace or normalize embedded
	// newlines and tabs. Retain readable text but report that approximation.
	boundarySpace := strings.TrimSpace(n.Text()) != n.Text()
	if boundarySpace || strings.ContainsAny(n.Text(), "\r\n\t") {
		p.losses.Add(n.Type())
	}
	text := escapeMarkdown(n.Text())
	marks := n.Marks()
	for _, mark := range marks {
		if MarkType(mark.Type()) == MarkTypeCode {
			text = markdownCode(n.Text())
			if strings.ContainsAny(n.Text(), "\r\n") {
				p.losses.Add(mark.Type())
			}
		}
	}
	for i := len(marks) - 1; i >= 0; i-- {
		mark := &marks[i]
		attrs := map[string]json.RawMessage{}
		if len(mark.attrs) > 0 {
			if err := json.Unmarshal(mark.attrs, &attrs); err != nil {
				p.losses.Add(mark.Type())
			}
		}
		for key := range attrs {
			if MarkType(mark.Type()) != MarkTypeLink || key != "href" {
				p.losses.Add(mark.Type())
			}
		}
		for _, field := range mark.fields {
			if field.key != "type" && field.key != "attrs" {
				p.losses.Add(mark.Type())
			}
		}
		switch MarkType(mark.Type()) {
		case MarkTypeStrong:
			if boundarySpace {
				p.losses.Add(mark.Type())
			}
			text = "**" + text + "**"
		case MarkTypeEm:
			if boundarySpace {
				p.losses.Add(mark.Type())
			}
			text = "*" + text + "*"
		case MarkTypeStrike:
			if boundarySpace {
				p.losses.Add(mark.Type())
			}
			text = "~~" + text + "~~"
		case MarkTypeCode:
		case MarkTypeLink:
			href := projectionAttr(attrs, "href")
			if href == "" || strings.ContainsAny(href, "\r\n") {
				p.losses.Add(mark.Type())
			}
			text = "[" + text + "](" + markdownDestination(href) + ")"
		default:
			p.losses.Add(mark.Type())
		}
	}
	return text
}

func (p *markdownProjection) table(n *Node) string {
	var rows [][]string
	columns := 0
	for _, row := range n.Content() {
		p.inspect(&row, nil)
		if NodeType(row.Type()) != NodeTypeTableRow {
			p.losses.Add(row.Type())
		}
		var cells []string
		for _, cell := range row.Content() {
			p.inspect(&cell, nil)
			text := p.blocks(cell.Content(), false)
			if strings.Contains(text, "\n") {
				p.losses.Add(cell.Type())
			}
			cells = append(cells, escapeTablePipes(strings.Join(strings.Fields(text), " ")))
		}
		columns = max(columns, len(cells))
		rows = append(rows, cells)
	}
	if columns == 0 {
		return ""
	}
	var lines []string
	for i, row := range rows {
		row = append(row, make([]string, columns-len(row))...)
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
		if i == 0 {
			lines = append(lines, "|"+strings.Repeat(" --- |", columns))
		}
	}
	return strings.Join(lines, "\n")
}

func projectionAttr(attrs map[string]json.RawMessage, name string) string {
	var value string
	if err := json.Unmarshal(attrs[name], &value); err != nil {
		return ""
	}
	return value
}

func quoteMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = ">"
		if line != "" {
			lines[i] += " " + line
		}
	}
	return strings.Join(lines, "\n")
}

func escapeMarkdown(text string) string {
	var escaped strings.Builder
	for offset, character := range text {
		// Literal native entity spellings must not become character references
		// when the projection is converted back to ADF.
		if character == '&' {
			escaped.WriteString("&amp;")
			continue
		}
		// Native literal text must not activate optional authoring directives.
		if strings.ContainsRune("\\`*_{}[]<>#+-.!|~", character) || character == ':' && strings.HasPrefix(text[offset:], ":status") {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

func longestBackticks(text string) int {
	longest, run := 0, 0
	for _, character := range text {
		if character == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

func markdownCode(text string) string {
	text = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text)
	fence := strings.Repeat("`", longestBackticks(text)+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") || strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.TrimSpace(text) != "" {
		text = " " + text + " "
	}
	return fence + text + fence
}

func markdownDestination(text string) string {
	return "<" + strings.NewReplacer("\\", "\\\\", "<", "\\<", ">", "\\>", "\r", "%0D", "\n", "%0A").Replace(text) + ">"
}

func escapeTablePipes(text string) string {
	var escaped strings.Builder
	backslashes := 0
	for _, character := range text {
		if character == '|' && backslashes%2 == 0 {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(character)
		if character == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	return escaped.String()
}
