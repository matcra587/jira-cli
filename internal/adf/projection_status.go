package adf

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (p *markdownProjection) status(n *Node, attrs map[string]json.RawMessage, topLevel bool) string {
	if !topLevel {
		var directive strings.Builder
		directive.WriteString(":status[" + statusDirectiveText(projectionAttr(attrs, "text")) + "]{color=\"" + statusDirectiveText(projectionAttr(attrs, "color")) + "\"")
		for _, name := range []string{"localId", "style"} {
			if _, present := attrs[name]; present {
				directive.WriteString(" " + name + "=\"" + statusDirectiveText(projectionAttr(attrs, name)) + "\"")
			}
		}
		directive.WriteByte('}')
		paragraph := NewNode(string(NodeTypeParagraph))
		paragraph.SetContent([]Node{*n})
		if projectionBlockEqual(paragraph, directive.String()) {
			return directive.String()
		}
	}
	p.losses.Add(n.Type())
	return markdownCode(projectionAttr(attrs, "text"))
}

// statusDirectiveText keeps label and quoted attribute values on one source
// line. Escape native entities before adding references for syntax characters.
func statusDirectiveText(text string) string {
	return strings.NewReplacer("\"", "&#34;", "\r", "&#13;", "\n", "&#10;", "\t", "&#9;").Replace(escapeMarkdown(text))
}

// statusPrecedingText escapes physical URI delimiters and the last letter or
// number so plain native text cannot suppress the following status parser.
// These references are Markdown syntax; gechr/x has no directive encoder.
func statusPrecedingText(text string) string {
	last, width := utf8.DecodeLastRuneInString(text)
	lastStart := len(text) - width
	var escaped strings.Builder
	start := 0
	for offset, character := range text {
		if character == ':' || character == '/' || offset == lastStart && (unicode.IsLetter(last) || unicode.IsNumber(last)) {
			escaped.WriteString(escapeMarkdown(text[start:offset]))
			escaped.WriteString("&#" + strconv.Itoa(int(character)) + ";")
			start = offset + utf8.RuneLen(character)
		}
	}
	return escaped.String() + escapeMarkdown(text[start:])
}
