package adf

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	xstrings "github.com/gechr/x/strings"
)

// This file normalizes Jira wiki markup into the GFM dialect the converter
// already understands. Users routinely paste content that originated in
// Jira itself — old descriptions, Server/DC exports, colleagues' comments —
// written in wiki markup, where h2. is a heading, {{text}} is code and
// ||cells|| open a table. Fed to a CommonMark parser untreated, all of that
// degrades to noise.
//
// The pass is gated by a dialect detector and runs ONLY when the input
// carries wiki signals and no CommonMark signals: pure Markdown input never
// enters the scanner, so its conversion stays byte-identical. Detection is
// all-or-nothing per document; there is no per-line mixing of dialects.

// mdDialect identifies which markup dialect a document is written in.
type mdDialect int

const (
	dialectCommonMark mdDialect = iota
	dialectWiki
)

// detectDialect classifies a document. CommonMark is both the default and
// the tie-breaker: any unambiguous CommonMark construct pins the document
// as CommonMark even when wiki signals are also present, because rewriting
// a Markdown document is worse than leaving wiki markup literal.
func detectDialect(input string) mdDialect {
	if hasCommonMarkSignal(input) {
		return dialectCommonMark
	}
	if hasWikiSignal(input) {
		return dialectWiki
	}
	return dialectCommonMark
}

// hasCommonMarkSignal reports whether the document contains a construct
// that only CommonMark spells this way: a fence, a multi-hash heading,
// **strong emphasis**, an inline link or image, a leading blockquote
// marker, or a GFM table separator row. A single leading # is NOT a signal
// — it is ambiguous with a wiki ordered-list item.
func hasCommonMarkSignal(input string) bool {
	for line := range strings.SplitSeq(input, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			return true
		case strings.HasPrefix(t, "##"):
			return true
		case strings.HasPrefix(t, "> "):
			return true
		case isTableSeparatorRow(t):
			return true
		}
		if xstrings.ContainsAny(t, "**", "](", "![") {
			return true
		}
	}
	return false
}

// hasWikiSignal reports whether the document contains a construct that only
// Jira wiki markup spells this way.
func hasWikiSignal(input string) bool {
	for line := range strings.SplitSeq(input, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case wikiHeadingLevel(t) > 0:
			return true
		case isWikiTableRow(t):
			return true
		case isWikiBlockOpen(t) != "":
			return true
		}
		if wikiInlineCodePattern.MatchString(t) || wikiLinkPattern.MatchString(t) {
			return true
		}
	}
	return false
}

// isTableSeparatorRow matches a GFM alignment row such as |---|:--:|.
func isTableSeparatorRow(t string) bool {
	if len(t) < 5 || t[0] != '|' || t[len(t)-1] != '|' {
		return false
	}
	return strings.Contains(t, "---")
}

// isWikiTableRow matches a wiki header row, ||a||b||. The length floor
// rules out the degenerate "||||" that carries no cell.
func isWikiTableRow(t string) bool {
	return strings.HasPrefix(t, "||") && strings.HasSuffix(t, "||") && len(t) >= 5
}

// wikiHeadingLevel returns 1-6 for an hN. heading line, 0 otherwise.
func wikiHeadingLevel(t string) int {
	if len(t) >= 4 && t[0] == 'h' && t[1] >= '1' && t[1] <= '6' && t[2] == '.' && t[3] == ' ' {
		return int(t[1] - '0')
	}
	return 0
}

// isWikiBlockOpen returns the fence to emit when t opens a {noformat} or
// {code} block, or "" when it does not. {code:java} carries its language
// onto the fence; {noformat} content is plain text.
func isWikiBlockOpen(t string) string {
	if t == "{noformat}" || strings.HasPrefix(t, "{noformat:") {
		return "```text"
	}
	if t == "{code}" {
		return "```"
	}
	if strings.HasPrefix(t, "{code:") && strings.HasSuffix(t, "}") {
		lang := strings.TrimSuffix(strings.TrimPrefix(t, "{code:"), "}")
		// {code:title=Foo.java|borderStyle=solid} style params: the first
		// segment before any | is the language when it has no key=value.
		lang, _, _ = strings.Cut(lang, "|")
		if strings.Contains(lang, "=") {
			lang = ""
		}
		return "```" + lang
	}
	return ""
}

var (
	// wikiLinkPattern matches [text|url] — the wiki hyperlink spelling.
	wikiLinkPattern = regexp.MustCompile(`\[([^\[\]|]+)\|([^\[\]]+)\]`)
	// wikiInlineCodePattern matches {{monospaced}} runs.
	wikiInlineCodePattern = regexp.MustCompile(`\{\{([^{}\n]+?)\}\}`)
	// wikiBoldPattern matches *bold* runs. The detector guarantees a wiki
	// document contains no ** anywhere (that is a CommonMark signal), so a
	// plain single-star pair needs no adjacency guards here.
	wikiBoldPattern = regexp.MustCompile(`\*([^\s*][^*\n]*?)\*`)
)

// wikiNormalized is the result of one normalization pass.
type wikiNormalized struct {
	text string
	// constructs holds the distinct construct names that were rewritten,
	// sorted, for the informational warning.
	constructs []string
}

// normalizeWikiMarkup rewrites Jira wiki markup as GFM in a single pass
// over the document's lines. A small state machine tracks Markdown fences
// and open {noformat}/{code} blocks so their content is never rewritten;
// everything else flows through the block rewrites (headings, ordered
// lists, table rows) and then the inline rewrites on the code-free
// segments of the line.
func normalizeWikiMarkup(input string) wikiNormalized {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines)+4)
	seen := map[string]bool{}

	var inFence, inWikiBlock bool
	for _, line := range lines {
		t := strings.TrimSpace(line)

		if inFence {
			out = append(out, line)
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				inFence = false
			}
			continue
		}
		if inWikiBlock {
			if t == "{noformat}" || t == "{code}" {
				out = append(out, "```")
				inWikiBlock = false
			} else {
				out = append(out, line)
			}
			continue
		}

		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			out = append(out, line)
			inFence = true
			continue
		}
		if fence := isWikiBlockOpen(t); fence != "" {
			out = append(out, fence)
			inWikiBlock = true
			seen["code blocks"] = true
			continue
		}

		if level := wikiHeadingLevel(t); level > 0 {
			out = append(out, strings.Repeat("#", level)+" "+t[4:])
			seen["headings"] = true
			continue
		}
		if strings.HasPrefix(line, "# ") {
			// In wiki markup a leading # is an ordered-list item, never a
			// heading — the detector already ruled out CommonMark.
			seen["numbered lists"] = true
			out = append(out, rewriteInline("1. "+line[2:], seen))
			continue
		}
		if isWikiTableRow(t) {
			header := strings.ReplaceAll(t, "||", "|")
			out = append(out, rewriteInline(header, seen))
			cells := strings.Count(header, "|") - 1
			out = append(out, "|"+strings.Repeat("---|", cells))
			seen["tables"] = true
			continue
		}

		out = append(out, rewriteInline(line, seen))
	}

	return wikiNormalized{
		text:       strings.Join(out, "\n"),
		constructs: slices.Sorted(maps.Keys(seen)),
	}
}

// rewriteInline applies the inline wiki rewrites — {{code}}, [text|url]
// and *bold* — to the parts of line that are not inside backtick spans.
// {{code}} converts first so the backtick spans it produces shield their
// content from the link and bold rewrites, matching how a fence shields a
// block.
func rewriteInline(line string, seen map[string]bool) string {
	if wikiInlineCodePattern.MatchString(line) {
		line = wikiInlineCodePattern.ReplaceAllString(line, "`$1`")
		seen["monospace"] = true
	}
	segments := strings.Split(line, "`")
	for i := 0; i < len(segments); i += 2 { // even indexes are outside backticks
		s := segments[i]
		if wikiLinkPattern.MatchString(s) {
			s = wikiLinkPattern.ReplaceAllString(s, "[$1]($2)")
			seen["links"] = true
		}
		if wikiBoldPattern.MatchString(s) {
			s = wikiBoldPattern.ReplaceAllString(s, "**$1**")
			seen["bold"] = true
		}
		segments[i] = s
	}
	return strings.Join(segments, "`")
}

// wikiEmoji is one emoticon shortcut's ADF spelling: the shortName the
// schema wants, plus a unicode fallback for renderers that only read text.
type wikiEmoji struct {
	shortName string
	text      string
}

// wikiEmojiShortNames maps Jira wiki emoticon shortcuts to their ADF emoji
// attrs. The shortcut spellings and names come from Atlassian's
// symbols-and-emoticons documentation.
var wikiEmojiShortNames = map[string]wikiEmoji{
	"(/)": {shortName: ":check_mark:", text: "✅"},
	"(x)": {shortName: ":cross_mark:", text: "❌"},
	"(!)": {shortName: ":warning:", text: "⚠️"},
	"(i)": {shortName: ":information_source:", text: "ℹ️"},
	"(+)": {shortName: ":heavy_plus_sign:", text: "➕"},
	"(-)": {shortName: ":heavy_minus_sign:", text: "➖"},
	"(?)": {shortName: ":question:", text: "❓"},
	"(y)": {shortName: ":thumbsup:", text: "👍"},
	"(n)": {shortName: ":thumbsdown:", text: "👎"},
	"(*)": {shortName: ":star:", text: "⭐"},
	":)":  {shortName: ":slight_smile:", text: "🙂"},
	":(":  {shortName: ":slight_frown:", text: "🙁"},
	":D":  {shortName: ":grin:", text: "😀"},
	":P":  {shortName: ":stuck_out_tongue:", text: "😛"},
	";)":  {shortName: ":wink:", text: "😉"},
	"<3":  {shortName: ":heart:", text: "❤️"},
	"</3": {shortName: ":broken_heart:", text: "💔"},
}

// wikiEmojiPattern matches every shortcut in wikiEmojiShortNames. Longer
// alternatives sort first so </3 wins over <3.
var wikiEmojiPattern = func() *regexp.Regexp {
	shortcuts := slices.Collect(maps.Keys(wikiEmojiShortNames))
	slices.SortFunc(shortcuts, func(a, b string) int {
		if n := cmp.Compare(len(b), len(a)); n != 0 {
			return n
		}
		return cmp.Compare(a, b)
	})
	for i, s := range shortcuts {
		shortcuts[i] = regexp.QuoteMeta(s)
	}
	return regexp.MustCompile(strings.Join(shortcuts, "|"))
}()

// wikiEmojiNodes converts one plain text run into a sequence of text and
// emoji nodes, replacing every wiki emoticon shortcut with an ADF emoji
// node. Text fragments keep the surrounding marks; emoji nodes carry none
// (the schema forbids marks like code on emoji, and decorating an emoji is
// meaningless). Returns nil when the input was not wiki markup or carries
// no shortcut, so the caller keeps its original single-node path.
func (c *mdConverter) wikiEmojiNodes(s string, marks []Mark) []Node {
	if !c.wikiEmoji {
		return nil
	}
	locs := wikiEmojiPattern.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return nil
	}
	nodes := make([]Node, 0, len(locs)*2+1)
	prev := 0
	for _, loc := range locs {
		if loc[0] > prev {
			nodes = append(nodes, c.text(s[prev:loc[0]], marks))
		}
		e := wikiEmojiShortNames[s[loc[0]:loc[1]]]
		nodes = append(nodes, c.node(NodeTypeEmoji, EmojiAttrs{ShortName: e.shortName, Text: e.text}, nil))
		prev = loc[1]
	}
	if prev < len(s) {
		nodes = append(nodes, c.text(s[prev:], marks))
	}
	return nodes
}

// wikiNormalizationWarning describes a completed normalization pass. It is
// informational (Lossy=false): the content survives, so strict mode must
// not abort on it — but the rewrite is loud in the envelope, and it flags
// that line/col positions in any subsequent warnings refer to the
// normalized Markdown rather than the wiki-markup original.
func wikiNormalizationWarning(constructs []string) Warning {
	msg := "input detected as Jira wiki markup and normalized to Markdown before ADF conversion"
	if len(constructs) > 0 {
		msg += fmt.Sprintf(" (converted: %s)", strings.Join(constructs, ", "))
	}
	msg += "; positions in any further warnings refer to the normalized text"
	return Warning{
		Type:     "markdown_dialect_normalized",
		Message:  msg,
		NodeType: "wiki_markup",
		Lossy:    false,
	}
}
