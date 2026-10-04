package adf_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// fakeStyler is a Styler test double whose marks are visible ASCII
// bracketing instead of real terminal styling — cheap to assert on, and
// enough to prove Render calls the right method for the right mark
// without dragging cli/lipgloss into an internal/adf test (which would
// also defeat the point: this package must stay stylable by ANY
// implementation, not just the one jira-cli ships).
type fakeStyler struct{}

func (fakeStyler) Text(s string) string {
	// Mirrors cli.Text's control-character policy closely enough to
	// prove Render sanitizes leaves: strip C0/DEL, nothing else.
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func (fakeStyler) Strong(s string) string       { return "**" + s + "**" }
func (fakeStyler) Emphasis(s string) string     { return "_" + s + "_" }
func (fakeStyler) Code(s string) string         { return "`" + s + "`" }
func (fakeStyler) Link(url, text string) string { return "[" + text + "](" + url + ")" }

// mustParse is types_test.go's fixture helper, shared across this
// package's test files.

// docJSON marshals v (a document tree built from map[string]any) and
// parses it back through ParseDocument. Used only for fixtures carrying
// a hostile control byte: Go's \x escapes express one safely inside a
// normal string literal, where a hand-typed JSON \u escape would not
// survive authoring this file unmangled.
func docJSON(t *testing.T, v any) *adf.Document {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	doc, err := adf.ParseDocument(raw)
	if err != nil {
		t.Fatalf("ParseDocument(%s): %v", raw, err)
	}
	return doc
}

func TestRenderNilDocument(t *testing.T) {
	if got := adf.Render(nil, fakeStyler{}, 0); got != "" {
		t.Errorf("Render(nil) = %q, want empty", got)
	}
}

func TestRenderParagraph(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"hello world"}]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	if got != "hello world" {
		t.Errorf("Render = %q, want %q", got, "hello world")
	}
}

func TestRenderTwoParagraphsAreBlankLineSeparated(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"first"}]},
		{"type":"paragraph","content":[{"type":"text","text":"second"}]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "first\n\nsecond"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderMarks pins each mark's dispatch to its own Styler method,
// and that combined marks compose (code innermost, link outermost).
func TestRenderMarks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "strong",
			text: `{"type":"text","text":"bold","marks":[{"type":"strong"}]}`,
			want: "**bold**",
		},
		{
			name: "emphasis",
			text: `{"type":"text","text":"em","marks":[{"type":"em"}]}`,
			want: "_em_",
		},
		{
			name: "code",
			text: `{"type":"text","text":"code","marks":[{"type":"code"}]}`,
			want: "`code`",
		},
		{
			name: "link",
			text: `{"type":"text","text":"click","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}`,
			want: "[click](https://example.com)",
		},
		{
			name: "strong and link combine, code innermost",
			text: `{"type":"text","text":"x","marks":[{"type":"strong"},{"type":"code"},{"type":"link","attrs":{"href":"https://example.com"}}]}`,
			want: "[**`x`**](https://example.com)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[`+tt.text+`]}]}`)
			got := adf.Render(doc, fakeStyler{}, 0)
			if got != tt.want {
				t.Errorf("Render = %q, want %q", got, tt.want)
			}
		})
	}
}

// recordingLinkStyler wraps fakeStyler but records the exact url argument
// every Link call receives, so a test can inspect what actually reached
// Link rather than only the composed rendered string (fakeStyler.Link's
// own "[text](url)" bracketing would otherwise make a control byte in
// url visually indistinguishable from one in text once both are spliced
// into one returned string).
type recordingLinkStyler struct {
	fakeStyler
	gotURL string
}

func (s *recordingLinkStyler) Link(url, text string) string {
	s.gotURL = url
	return s.fakeStyler.Link(url, text)
}

// TestRenderSanitizesHrefBeforeLink pins that a link mark's href — Jira-
// authored exactly like a text node's own content, and (through
// descriptionStyler.Link in internal/cli/issue) the raw input to a real
// terminal's OSC 8 hyperlink escape one layer up — is sanitized through
// Styler.Text before Render ever calls Link with it. An ESC or BEL
// surviving in href could terminate an OSC 8 sequence early and inject a
// forged one; the link's visible text was already sanitized this way,
// only href was raw.
func TestRenderSanitizesHrefBeforeLink(t *testing.T) {
	hostileHref := "https://example.com/\x1b]8;;file:///etc/passwd\x07evil\x07"
	doc := docJSON(t, map[string]any{
		"type": "doc", "version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text", "text": "click",
						"marks": []any{
							map[string]any{"type": "link", "attrs": map[string]any{"href": hostileHref}},
						},
					},
				},
			},
		},
	})

	s := &recordingLinkStyler{}
	adf.Render(doc, s, 0)

	if s.gotURL == "" {
		t.Fatal("Link was never called — this test would prove nothing")
	}
	if strings.ContainsRune(s.gotURL, '\x1b') {
		t.Errorf("Link received href %q, an ESC byte from Jira reached the hyperlink escape", s.gotURL)
	}
	if strings.ContainsRune(s.gotURL, '\x07') {
		t.Errorf("Link received href %q, a BEL byte from Jira reached the hyperlink escape", s.gotURL)
	}
}

func TestRenderLinkWithoutHrefIsPlainText(t *testing.T) {
	// A link mark with no href (malformed, but the substrate does not
	// reject it) must not call Link with an empty target — the text
	// renders plain rather than as "[x]()".
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":""}}]}]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	if got != "x" {
		t.Errorf("Render = %q, want %q (no Link() call for an empty href)", got, "x")
	}
}

func TestRenderHeadingLevels(t *testing.T) {
	tests := []struct {
		level float64
		want  string
	}{
		{level: 1, want: "**# Title**"},
		{level: 3, want: "**### Title**"},
		{level: 6, want: "**###### Title**"},
		{level: 9, want: "**###### Title**"}, // clamped
		{level: 0, want: "**# Title**"},      // clamped
	}
	for _, tt := range tests {
		doc := mustParse(t, fmtHeading(tt.level))
		got := adf.Render(doc, fakeStyler{}, 0)
		if got != tt.want {
			t.Errorf("heading level %v: Render = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func fmtHeading(level float64) string {
	return `{"type":"doc","version":1,"content":[
		{"type":"heading","attrs":{"level":` + strconv.FormatFloat(level, 'f', -1, 64) + `},"content":[{"type":"text","text":"Title"}]}
	]}`
}

func TestRenderHardBreakSplitsPhysicalLines(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[
			{"type":"text","text":"line one"},
			{"type":"hardBreak"},
			{"type":"text","text":"line two"}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "line one\nline two"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderBulletList(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"bulletList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "• first\n• second"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderOrderedListNumbersFromOrderAttr(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"orderedList","attrs":{"order":5},"content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "5. a\n6. b"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderNestedList pins that a list nested inside a listItem indents
// one level deeper than its parent's marker column, and — like any two
// sibling blocks rendered by blocks() — IS separated from the item's own
// paragraph by a blank line ("outer\n\n  • inner"); it still renders
// under the item's own marker column, not promoted to a sibling bullet
// of its own, so a nested list stays part of the same item structurally
// even though it reads as its own block.
func TestRenderNestedList(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"bulletList","content":[
			{"type":"listItem","content":[
				{"type":"paragraph","content":[{"type":"text","text":"outer"}]},
				{"type":"bulletList","content":[
					{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"inner"}]}]}
				]}
			]}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "• outer\n\n  • inner"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderCodeBlockPreservesInternalNewlines(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"codeBlock","content":[{"type":"text","text":"line1\nline2"}]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "    `line1`\n    `line2`"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderCodeBlockSanitizesEachLineIndependently proves a hostile
// control byte embedded IN a code block is still stripped, without the
// legitimate newlines around it being swept away too -- the reason
// renderCodeBlock splits before sanitizing rather than after.
func TestRenderCodeBlockSanitizesEachLineIndependently(t *testing.T) {
	hostile := "safe\x07line\nnext" // \x07 = BEL
	doc := docJSON(t, map[string]any{
		"type": "doc", "version": 1,
		"content": []any{
			map[string]any{
				"type": "codeBlock",
				"content": []any{
					map[string]any{"type": "text", "text": hostile},
				},
			},
		},
	})
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "    `safeline`\n    `next`"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderBlockquoteIndentsEveryLine(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"blockquote","content":[
			{"type":"paragraph","content":[{"type":"text","text":"first"}]},
			{"type":"paragraph","content":[{"type":"text","text":"second"}]}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "> first\n\n> second"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderRuleUsesFallbackWidthOffTTY(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"rule"}]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	if n := len([]rune(strings.TrimRight(got, "\n"))); n != 80 {
		t.Errorf("Render = %q (%d runes), want an 80-column rule off-TTY", got, n)
	}
}

// TestRenderDeferredNodeTypeIsPlaceholder pins the placeholder contract
// for a node type outside the dispatch table — the escape hatch that
// keeps --output=json the lossless surface for content this walker
// draws imperfectly.
func TestRenderDeferredNodeTypeIsPlaceholder(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"table","content":[]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "[table — see --output=json]"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderDeferredInlineNodeTypeIsPlaceholder is the same contract for
// an unknown inline node embedded inside a paragraph.
func TestRenderDeferredInlineNodeTypeIsPlaceholder(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[
			{"type":"text","text":"hi "},
			{"type":"futureInline","attrs":{"id":"abc"}}
		]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "hi [futureInline — see --output=json]"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderSanitizesHostileTextBeforeStyling proves Render runs a leaf
// through Styler.Text -- and therefore whatever sanitization policy the
// real cli.Text implements -- BEFORE any mark styling touches it, per
// design constraint #4.
func TestRenderSanitizesHostileTextBeforeStyling(t *testing.T) {
	hostile := "click\x1b[31mhere\x07" // ESC ... BEL
	doc := docJSON(t, map[string]any{
		"type": "doc", "version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text", "text": hostile,
						"marks": []any{map[string]any{"type": "strong"}},
					},
				},
			},
		},
	})
	got := adf.Render(doc, fakeStyler{}, 0)
	// ESC and BEL stripped; "[31m" is plain printable text that survives.
	want := "**click[31mhere**"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("Render left a control byte in %q", got)
	}
}

func TestRenderWidthWrapsParagraphText(t *testing.T) {
	text := strings.Repeat("word ", 20) + "end"
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"`+text+`"}]}
	]}`)

	wrapped := adf.Render(doc, fakeStyler{}, 20)
	if lines := strings.Count(wrapped, "\n") + 1; lines < 2 {
		t.Errorf("Render at width 20 produced %d line(s), want wrapping across several", lines)
	}

	unwrapped := adf.Render(doc, fakeStyler{}, 0)
	if strings.Contains(unwrapped, "\n") {
		t.Errorf("Render at width 0 wrapped: %q, want exactly one line (non-TTY posture)", unwrapped)
	}
}

func TestRenderEmptyParagraphIsBlankLine(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[]},
		{"type":"paragraph","content":[{"type":"text","text":"after"}]}
	]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	want := "\n\nafter"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}
