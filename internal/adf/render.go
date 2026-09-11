package adf

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Segment is a run of rendered text tagged with the ADF node or mark type it
// came from, so a style-aware renderer can color each run. See ToFormatted.
type Segment struct {
	Text string
	Kind string
}

// ToPlain renders an ADF document to plain text: block content joined by
// spaces, with attr-only inline nodes (mentions, emoji, media, tasks) flattened
// to a readable stand-in so nothing renderable is silently dropped.
func ToPlain(doc Document) string {
	var parts []string
	for _, node := range doc.Content {
		collectPlain(&parts, node)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ToMarkdown renders an ADF document to GitHub-flavored Markdown — blocks
// separated by blank lines, one trailing newline. A node with no Markdown
// equivalent (panel, media) degrades to a labeled placeholder rather than
// vanishing, so the reader stays aware the content existed.
func ToMarkdown(doc Document) string {
	var lines []string
	for _, node := range doc.Content {
		if rendered := markdownBlock(node); rendered != "" {
			lines = append(lines, rendered)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n")) + "\n"
}

// ToFormatted renders an ADF document to a flat sequence of Segments, each text
// run tagged with its node or mark type, for a terminal renderer that styles by
// kind. A marked run is emitted once per mark, so a bold link yields a segment
// for each applicable style.
func ToFormatted(doc Document) []Segment {
	var segments []Segment
	for _, node := range doc.Content {
		collectFormatted(&segments, node, node.Type)
	}
	return segments
}

func collectPlain(parts *[]string, node Node) {
	// Attr-only inline nodes carry their content in attrs, not Text; flatten
	// them the same way ToMarkdown does so the plain path never silently
	// drops what the markdown path (and its lossy detector) considers
	// renderable.
	switch node.Type {
	case "mention":
		if name := attrStr(node.Attrs, "text", attrStr(node.Attrs, "id", "")); name != "" {
			*parts = append(*parts, "@"+strings.TrimPrefix(name, "@"))
		}
	case "emoji":
		if e := attrStr(node.Attrs, "text", attrStr(node.Attrs, "shortName", "")); e != "" {
			*parts = append(*parts, e)
		}
	case "status":
		if txt := attrStr(node.Attrs, "text", ""); txt != "" {
			*parts = append(*parts, txt)
		}
	case "inlineCard":
		if url := attrStr(node.Attrs, "url", ""); url != "" {
			*parts = append(*parts, url)
		}
	case "media":
		*parts = append(*parts, "[attachment: "+attrStr(node.Attrs, "alt", attrStr(node.Attrs, "id", "file"))+"]")
	case "taskItem", "blockTaskItem":
		if attrStr(node.Attrs, "state", "TODO") == "DONE" {
			*parts = append(*parts, "[x]")
		} else {
			*parts = append(*parts, "[ ]")
		}
	case "decisionItem":
		*parts = append(*parts, "<>")
	}
	if node.Text != "" {
		*parts = append(*parts, node.Text)
	}
	for _, child := range node.Content {
		collectPlain(parts, child)
	}
}

func markdownBlock(node Node) string {
	switch node.Type {
	case "paragraph":
		return markdownChildren(node)
	case "text":
		return markdownText(node)
	case "heading":
		level := attrInt(node.Attrs, "level", 1)
		return strings.Repeat("#", level) + " " + markdownChildren(node)
	case "bulletList":
		return markdownList(node, "-")
	case "orderedList":
		return markdownList(node, "1.")
	case "listItem":
		return markdownChildren(node)
	case "codeBlock":
		return "```\n" + strings.TrimSuffix(markdownChildren(node), "\n") + "\n```"
	case "blockquote":
		return quoteLines(joinBlocks(node.Content))
	case "panel":
		// Panels have no Markdown equivalent: render as a quote with the
		// panel type as a bold label so the intent survives.
		return quoteLines("**" + capitalize(attrStr(node.Attrs, "panelType", "note")) + "**\n\n" + joinBlocks(node.Content))
	case "rule":
		return "---"
	case "hardBreak":
		return "  \n"
	case "mention":
		name := attrStr(node.Attrs, "text", attrStr(node.Attrs, "id", ""))
		if name == "" {
			return ""
		}
		return "@" + strings.TrimPrefix(name, "@")
	case "emoji":
		return attrStr(node.Attrs, "text", attrStr(node.Attrs, "shortName", ""))
	case "status":
		if txt := attrStr(node.Attrs, "text", ""); txt != "" {
			return "`" + txt + "`"
		}
		return ""
	case "inlineCard":
		if url := attrStr(node.Attrs, "url", ""); url != "" {
			return "<" + url + ">"
		}
		return ""
	case "table":
		return markdownTable(node)
	case "taskList":
		return markdownTaskList(node)
	case "taskItem", "blockTaskItem":
		return markdownTaskLine(node)
	case "decisionList":
		return markdownDecisionList(node)
	case "decisionItem":
		return "<> " + markdownChildren(node)
	case "mediaSingle", "mediaGroup":
		return joinBlocks(node.Content)
	case "media":
		// Media can't be shown in a terminal: a labeled placeholder keeps
		// the reader aware an attachment exists.
		return "[attachment: " + attrStr(node.Attrs, "alt", attrStr(node.Attrs, "id", "file")) + "]"
	default:
		return markdownChildren(node)
	}
}

// joinBlocks renders child blocks separated by blank lines, the same shape
// ToMarkdown produces at the top level.
func joinBlocks(nodes []Node) string {
	var blocks []string
	for _, child := range nodes {
		if rendered := markdownBlock(child); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// quoteLines prefixes every line with the Markdown quote marker (bare ">"
// on blank lines, which GFM requires to keep one quote block together).
func quoteLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// markdownTaskList renders an ADF taskList as GFM task-list items. A
// nested taskList child renders indented two spaces so GFM re-nests it
// under the preceding item on the way back in.
func markdownTaskList(node Node) string {
	var lines []string
	for _, child := range node.Content {
		switch child.Type {
		case "taskItem", "blockTaskItem":
			lines = append(lines, "- "+markdownTaskLine(child))
		case "taskList":
			lines = append(lines, indentLines(markdownTaskList(child), "  "))
		}
	}
	return strings.Join(lines, "\n")
}

// markdownTaskLine renders one task item as its GFM checkbox plus content.
// blockTaskItem holds block content, which flattens to one line — a GFM
// list item's continuation lines would need indentation guarantees the
// surrounding join does not provide.
func markdownTaskLine(node Node) string {
	box := "[ ] "
	if attrStr(node.Attrs, "state", "TODO") == "DONE" {
		box = "[x] "
	}
	if node.Type == "blockTaskItem" {
		return box + strings.Join(strings.Fields(joinBlocks(node.Content)), " ")
	}
	return box + markdownChildren(node)
}

// markdownDecisionList renders an ADF decisionList as a bullet list whose
// items lead with the "<>" decision marker — the shortcut Atlassian's own
// editor uses to author a decision.
func markdownDecisionList(node Node) string {
	var lines []string
	for _, child := range node.Content {
		if child.Type != "decisionItem" {
			continue
		}
		lines = append(lines, "- "+markdownBlock(child))
	}
	return strings.Join(lines, "\n")
}

// indentLines prefixes every non-empty line with prefix.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// markdownTable renders an ADF table as GFM. The first row supplies the
// header (Jira tables normally lead with tableHeader cells); GFM has no
// headerless tables, so a leading data row is promoted rather than dropped.
func markdownTable(node Node) string {
	var rows [][]string
	cols := 0
	for _, row := range node.Content {
		if row.Type != "tableRow" {
			continue
		}
		var cells []string
		for _, cell := range row.Content {
			// Cells hold block nodes; join them like blocks, then flatten to
			// one line (GFM cells can't span lines).
			text := strings.TrimSpace(joinBlocks(cell.Content))
			text = strings.Join(strings.Fields(text), " ")
			cells = append(cells, strings.ReplaceAll(text, "|", `\|`))
		}
		if len(cells) > cols {
			cols = len(cells)
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 || cols == 0 {
		return ""
	}
	// Pad ragged rows so the separator matches every row — GFM renderers
	// reject tables whose rows disagree on column count.
	for i, r := range rows {
		for len(r) < cols {
			r = append(r, "")
		}
		rows[i] = r
	}
	line := func(cells []string) string { return "| " + strings.Join(cells, " | ") + " |" }
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	out := []string{line(rows[0]), line(sep)}
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func attrStr(attrs map[string]any, key, fallback string) string {
	if s, ok := attrs[key].(string); ok && s != "" {
		return s
	}
	return fallback
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

func attrInt(attrs map[string]any, key string, fallback int) int {
	switch raw := attrs[key].(type) {
	case int:
		if raw > 0 {
			return raw
		}
	case float64:
		if raw > 0 {
			return int(raw)
		}
	}
	return fallback
}

func markdownChildren(node Node) string {
	var parts []string
	for _, child := range node.Content {
		if child.Type == "text" {
			parts = append(parts, markdownText(child))
			continue
		}
		parts = append(parts, markdownBlock(child))
	}
	return strings.Join(parts, "")
}

func markdownText(node Node) string {
	text := node.Text
	for _, mark := range slices.Backward(node.Marks) {
		switch mark.Type {
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "*" + text + "*"
		case "code":
			text = "`" + text + "`"
		case "link":
			if href, ok := mark.Attrs["href"].(string); ok && href != "" {
				text = "[" + text + "](" + href + ")"
			}
		case "strike":
			text = "~~" + text + "~~"
		}
	}
	return text
}

func markdownList(node Node, marker string) string {
	lines := make([]string, 0, len(node.Content))
	for i, child := range node.Content {
		prefix := marker
		if node.Type == "orderedList" {
			prefix = strconv.Itoa(i+1) + "."
		}
		item := strings.TrimSpace(markdownBlock(child))
		lines = append(lines, prefix+" "+item)
	}
	return strings.Join(lines, "\n")
}

func collectFormatted(segments *[]Segment, node Node, inheritedKind string) {
	kind := inheritedKind
	if node.Type != "" && node.Type != "text" {
		kind = node.Type
	}
	if node.Type != "" && node.Type != "text" && len(node.Content) > 0 {
		plain := strings.TrimSpace(markdownChildren(node))
		if plain != "" {
			*segments = append(*segments, Segment{Text: plain, Kind: node.Type})
		}
	}
	if node.Text != "" {
		textKind := kind
		if textKind == "" {
			textKind = "text"
		}
		*segments = append(*segments, Segment{Text: node.Text, Kind: textKind})
		for _, mark := range node.Marks {
			*segments = append(*segments, Segment{Text: node.Text, Kind: mark.Type})
		}
	}
	for _, child := range node.Content {
		collectFormatted(segments, child, kind)
	}
}
