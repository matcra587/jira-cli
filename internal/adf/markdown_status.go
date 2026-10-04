package adf

import (
	"bytes"
	"net/url"
	"strings"
	"unicode"

	xstrings "github.com/gechr/x/strings"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var kindStatusDirective = ast.NewNodeKind("StatusDirective")

// Labels use ordinary inline Markdown to detect formatting that the leaf ADF
// status cannot carry. This parser has no directive extension, so labels cannot
// recursively author status nodes or share a mutable parse context.
var statusLabelParser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough)).Parser()

type statusDirective struct {
	ast.BaseInline
	label      []byte
	literal    []byte
	attributes []statusAttribute
	references []parser.Reference
}

func (n *statusDirective) Kind() ast.NodeKind { return kindStatusDirective }

func (n *statusDirective) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type statusDirectiveParser struct{}

func (statusDirectiveParser) Trigger() []byte { return []byte{':'} }

func (statusDirectiveParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	const prefix = ":status"
	if !bytes.HasPrefix(line, []byte(prefix)) || !statusBoundary(parent, reader, segment.Start) {
		return nil
	}
	if len(line) > len(prefix) {
		next := line[len(prefix)]
		if statusNameByte(next) || next == ':' {
			return nil
		}
	}
	savedLine, savedPosition := reader.Position()
	reader.Advance(len(prefix))
	n := &statusDirective{references: pc.References()}
	if reader.Peek() == '[' {
		reader.Advance(1)
		labelLine, labelPosition := reader.Position()
		label, ok := reader.FindClosure('[', ']', text.FindClosureOptions{CodeSpan: true, Nesting: true, Newline: true, Advance: true})
		if !ok {
			// Goldmark's closure scanner treats an unmatched backtick as an
			// open code span. Retry as literal text; ordinary label parsing
			// still rejects any actual formatting rather than losing it.
			reader.SetPosition(labelLine, labelPosition)
			label, ok = reader.FindClosure('[', ']', text.FindClosureOptions{Nesting: true, Newline: true, Advance: true})
		}
		if !ok {
			reader.SetPosition(savedLine, savedPosition)
			return nil
		}
		n.label = label.Value(reader.Source())
	}
	if reader.Peek() == '{' {
		var ok bool
		n.attributes, ok = readStatusAttributes(reader)
		if !ok {
			reader.SetPosition(savedLine, savedPosition)
			return nil
		}
	}
	_, end := reader.Position()
	n.literal = reader.Source()[segment.Start:end.Start]
	return n
}

// Restrict this optional spelling to text boundaries. In particular, a colon
// within a URL token must remain part of the URL rather than authoring a node.
func statusBoundary(parent ast.Node, reader text.Reader, offset int) bool {
	before := reader.PrecendingCharacter()
	if before == ':' || before == '/' || unicode.IsLetter(before) || unicode.IsNumber(before) {
		return false
	}
	source := reader.Source()
	if !statusURLPrefix(source[:offset]) {
		return true
	}
	// Emphasis and strike delimiters resolve after inline parsing. Probe a
	// detached prefix with Goldmark's own resolver; never consume the live
	// delimiter stack, which can still acquire closers after this directive.
	prefix := parent
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if _, delimiter := child.(*parser.Delimiter); delimiter {
			prefix = statusResolvedPrefix(parent)
			break
		}
	}
	return statusPrefixBoundary(prefix, source, offset)
}

func statusPrefixBoundary(prefix ast.Node, source []byte, offset int) bool {
	start := offset
	if first := prefix.FirstChild(); first != nil {
		start = statusSourceStart(first)
	}
	for child := prefix.FirstChild(); child != nil; child = child.NextSibling() {
		childStart := statusSourceStart(child)
		if childStart >= offset {
			break
		}
		if _, text := child.(*ast.Text); text {
			continue
		}
		// A completed inline node is a boundary unless its opener was already
		// inside a URL token. Formatting within a bare URL cannot activate a
		// directive in the remainder of that URL.
		if !statusURLPrefix(source[start:childStart]) {
			start = offset
			if next := child.NextSibling(); next != nil {
				start = statusSourceStart(next)
			}
		}
	}
	return !statusURLPrefix(source[start:offset])
}

func statusSourceStart(node ast.Node) int {
	if text, ok := node.(*ast.Text); ok {
		return text.Segment.Start
	}
	return node.Pos()
}

func statusResolvedPrefix(parent ast.Node) ast.Node {
	probe := ast.NewParagraph()
	pc := parser.NewContext()
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		// Goldmark 1.8.6 exposes its pending link-label node only through
		// this AST kind name. Resolve each copied delimiter scope separately;
		// an opener outside a pending label cannot close inside that label.
		if child.Kind().String() == "LinkLabelState" {
			parser.ProcessDelimiters(nil, pc)
			pc = parser.NewContext()
		}
		var copy ast.Node
		switch child := child.(type) {
		case *ast.Text:
			copy = ast.NewTextSegment(child.Segment)
		case *parser.Delimiter:
			d := parser.NewDelimiter(child.CanOpen, child.CanClose, child.OriginalLength, child.Char, child.Processor)
			d.Length, d.Segment = child.Length, child.Segment
			pc.PushDelimiter(d)
			copy = d
		default:
			// Delimiter matching does not inspect completed nodes' contents.
			copy = ast.NewCodeSpan()
		}
		copy.SetPos(child.Pos())
		probe.AppendChild(probe, copy)
	}
	parser.ProcessDelimiters(nil, pc)
	return probe
}

func statusURLPrefix(prefix []byte) bool {
	start := len(prefix)
	for start > 0 && prefix[start-1] != ' ' && prefix[start-1] != '\t' && prefix[start-1] != '\n' && prefix[start-1] != '\r' {
		start--
	}
	prefix = prefix[start:]
	if bytes.Contains(prefix, []byte("://")) {
		return true
	}
	// URI references also include opaque schemes and protocol-relative hosts.
	// Opening prose delimiters do not turn a wrapped URL into directive text.
	candidate, err := url.Parse(strings.TrimLeft(string(prefix), "([{<\"'*_~"))
	return err == nil && !xstrings.AllEmpty(candidate.Scheme, candidate.Host)
}

func statusNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

type statusAttribute struct {
	name     string
	value    []byte
	hasValue bool
}

// Goldmark's ParseAttributes uses a different value grammar: it rejects
// unquoted #RRGGBB and single quotes and interprets booleans/numbers. This
// scanner follows text-directive HTML attribute spellings instead, retaining
// duplicate and unknown attributes so conversion can explicitly refuse them.
func readStatusAttributes(reader text.Reader) ([]statusAttribute, bool) {
	reader.Advance(1)
	var attributes []statusAttribute
	for {
		reader.SkipSpaces()
		if reader.Peek() == '}' {
			reader.Advance(1)
			return attributes, true
		}
		line, _ := reader.PeekLine()
		if len(line) == 0 {
			return nil, false
		}
		shortcut := line[0] == '#' || line[0] == '.'
		start := 0
		if shortcut {
			start = 1
		}
		i := start
		for i < len(line) && (statusNameByte(line[i]) || line[i] == ':' || line[i] == '.') {
			i++
		}
		if i == start || !shortcut && (line[0] == '-' || line[0] >= '0' && line[0] <= '9') {
			return nil, false
		}
		attribute := statusAttribute{name: string(line[:i])}
		reader.Advance(i)
		if !shortcut {
			nameLine, namePosition := reader.Position()
			reader.SkipSpaces()
			if reader.Peek() == '=' {
				attribute.hasValue = true
				reader.Advance(1)
				reader.SkipSpaces()
				var ok bool
				attribute.value, ok = readStatusAttributeValue(reader)
				if !ok {
					return nil, false
				}
			} else {
				reader.SetPosition(nameLine, namePosition)
			}
		}
		attributes = append(attributes, attribute)
		if next := reader.Peek(); next != '}' && next != ' ' && next != '\t' && next != '\n' && next != '\r' && next != text.EOF {
			return nil, false
		}
	}
}

func readStatusAttributeValue(reader text.Reader) ([]byte, bool) {
	quote := reader.Peek()
	if quote == '\'' || quote == '"' {
		reader.Advance(1)
		value, ok := reader.FindClosure(quote, quote, text.FindClosureOptions{Newline: true, Advance: true})
		if !ok {
			return nil, false
		}
		return value.Value(reader.Source()), true
	}
	line, _ := reader.PeekLine()
	i := 0
	for i < len(line) && line[i] != '}' && line[i] != ' ' && line[i] != '\t' && line[i] != '\n' && line[i] != '\r' {
		if strings.ContainsRune("\"'`<=>{", rune(line[i])) {
			return nil, false
		}
		i++
	}
	if i == 0 {
		return nil, false
	}
	reader.Advance(i)
	return line[:i], true
}

func (c *mdConverter) statusDirective(n *statusDirective, marks []Mark) []Node {
	fallback := func(reason string) []Node {
		c.warnings = append(c.warnings, c.sourceMapped(n, Warning{
			Type: "markdown_lossy_conversion", NodeType: "status directive", Lossy: true,
			Message: "Markdown status directive cannot be represented in ADF; kept its literal spelling: " + reason,
		}))
		return []Node{c.text(string(n.literal), marks)}
	}
	if len(marks) != 0 {
		return fallback("status nodes cannot carry inherited formatting")
	}
	label, plain, err := statusLabel(n.label, n.references)
	if err != nil {
		if c.err == nil {
			c.err = err
		}
		return nil
	}
	if !plain {
		return fallback("the label must contain plain text")
	}
	// Generated optional string fields omit explicit empty values. Keep the
	// supplied keys so native localId/style presence survives conversion.
	attrs := map[string]string{"text": label}
	for _, attribute := range n.attributes {
		switch attribute.name {
		case "color", "localId", "style":
		default:
			return fallback("only color, localId and style attributes are supported")
		}
		if _, exists := attrs[attribute.name]; exists {
			return fallback("attributes must not be repeated")
		}
		if !attribute.hasValue {
			return fallback("attributes require explicit string values")
		}
		value, err := markdownText(attribute.value)
		if err != nil {
			if c.err == nil {
				c.err = err
			}
			return nil
		}
		attrs[attribute.name] = value
	}
	if _, exists := attrs["color"]; !exists {
		attrs["color"] = "neutral"
	}
	node := c.node(NodeTypeStatus, attrs, nil)
	probe := NewDocument()
	probe.SetContent([]Node{c.node(NodeTypeParagraph, nil, []Node{node})})
	if err := probe.Validate(); err != nil {
		return fallback("the label or color violates the pinned status schema")
	}
	return []Node{node}
}

func statusLabel(label []byte, references []parser.Reference) (string, bool, error) {
	// Plain sentinels force inline parsing, preserving literal leading/trailing
	// spaces and preventing a label beginning with '#' or '-' becoming a block.
	source := []byte("a " + string(label) + " b")
	pc := parser.NewContext()
	for _, reference := range references {
		pc.AddReference(reference)
	}
	root := statusLabelParser.Parse(text.NewReader(source), parser.WithContext(pc))
	paragraph := root.FirstChild()
	if paragraph == nil || paragraph.Kind() != ast.KindParagraph || paragraph.NextSibling() != nil {
		return "", false, nil
	}
	var result strings.Builder
	for child := paragraph.FirstChild(); child != nil; child = child.NextSibling() {
		t, ok := child.(*ast.Text)
		if !ok || t.HardLineBreak() {
			return "", false, nil
		}
		value, err := markdownText(t.Segment.Value(source))
		if err != nil {
			return "", false, err
		}
		result.WriteString(value)
		if t.SoftLineBreak() {
			result.WriteByte(' ')
		}
	}
	return strings.TrimSuffix(strings.TrimPrefix(result.String(), "a "), " b"), true, nil
}
