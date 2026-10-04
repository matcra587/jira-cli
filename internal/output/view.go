package output

import (
	"io"
	"strings"

	"github.com/gechr/clog"
	"github.com/gechr/clog/field/hyperlink"
	"github.com/gechr/x/ansi"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
)

// View is the human stdout emitter half of the output-surface design's
// formatter/emitter split (gh-shaped: iostreams.ColorScheme's pure
// sinks vs. tableprinter's emitter). It wraps a clog Logger pinned to
// ONE writer — cmd.OutOrStdout() at the call site a later layer wires
// in — so a renderer draws its own layout without reaching for a bare
// io.Writer or fmt.Fprintf.
//
// View owns no color policy of its own: Fields and Line are thin layout
// helpers, not a place a renderer picks a color. That decision already
// happened at the pure value sinks (cli.Status, cli.Priority,
// cli.Entity, ...) before a string ever reaches here — View just lines
// values up.
//
// Table (table.go, a package-level function rather than a method — see
// its own doc comment for why) is the list-layout primitive, added
// alongside these once the first list command needed it (design doc,
// "Table (RESOLVED)"). Section (below) is the titled header the
// description, and each future section, sits under.
type View struct {
	logger *clog.Logger
}

// NewView builds a View writing to w with mode — the SAME clog.ColorMode
// cli.ResolvedColorMode published for this invocation, so the View's own
// color/TTY detection agrees with what the pure value sinks already
// decided for the same writer. Passing a mismatched mode is a caller
// bug, not something this constructor can detect from here.
func NewView(w io.Writer, mode clog.ColorMode) *View {
	logger := clog.New(clog.NewOutput(w, mode))
	// Message only. A View writes DATA, so none of a log line's furniture
	// belongs on it — no timestamp, no level, no symbol. Set on this
	// logger rather than through clog's package-level SetParts, which
	// root already uses for its own stderr logger: two writers with two
	// jobs, each configured where it is built.
	//
	// This is what lets Hint below reach clog's message path at all —
	// the path carrying backtick styling, wrapping, and the tree
	// connectors a parent/child render will want — without dragging a
	// logger's presentation onto a data stream. Line/Block/Fields/Section
	// deliberately do NOT go through it: View fits rows and titles to the
	// destination width, while Block preserves its caller's layout. Message
	// formatting would add another layout pass to those data surfaces.
	logger.SetParts(clog.PartMessage)
	// ADF link labels are document content, not abbreviations of a URL.
	// Preserve them when OSC 8 is disabled instead of using clog's URL fallback.
	logger.SetHyperlinkFallback(hyperlink.FallbackText)
	return &View{logger: logger}
}

// Logger returns the underlying clog.Logger, for a caller that needs a
// clog primitive View does not wrap itself (a tree, a divider, a
// spinner around a slow render step).
func (v *View) Logger() *clog.Logger {
	return v.logger
}

// Output returns the underlying clog.Output, for a caller that needs its
// width or TTY state directly.
func (v *View) Output() *clog.Output {
	return v.logger.Output()
}

// rowGutter separates a Field's label from its value, and is repeated as
// leading space on a wrapped value's continuation lines so they line up
// under the value column rather than the margin.
const rowGutter = "  "

// Field is one label/value pair in the aligned block Fields renders.
type Field struct {
	Label string
	Value string
}

// IsBlank reports whether a rendered value carries no visible content —
// the one place this package's "what counts as empty" rule lives, so a
// caller deciding whether to substitute a placeholder and Fields
// deciding whether to drop a row can never disagree about the same
// value.
//
// Stripped before trimming, not TrimSpace alone: a value sink wraps even
// an EMPTY name in real ANSI (lipgloss renders a style around ""), so
// the raw string is non-empty whenever styling is on.
func IsBlank(rendered string) bool {
	return xstrings.IsBlank(ansi.Strip(rendered))
}

// Fields renders fields as one aligned "Label: value" block: every label
// gets a trailing colon and is padded to the widest VISIBLE label in this
// call, so every value in the block starts in the same column — the
// shape gh and most CLIs use for a detail block, and a step up from a
// column that only lines up within one row.
//
// A field whose Value has no visible content is skipped — not
// strings.TrimSpace(Value) alone, but TrimSpace after x/ansi.Strip: a
// colored value (cli.Entity, cli.Status, ...) can wrap an EMPTY name in
// real ANSI codes (lipgloss.Style.Render does not special-case ""), so
// checking the raw styled string would see non-whitespace bytes and
// print a row of bare color codes instead of skipping it. Stripping
// first is what makes it safe for a caller to build every field the same
// way — sink already applied — and let Fields decide what's empty,
// rather than each caller re-deriving cli.Entity's own emptiness rule.
//
// A single-Field call still works: the block's shared width is just that
// one label's width, so it aligns to itself. When the terminal cannot fit
// the label column and a value's graphemes, the complete row wraps instead.
func (v *View) Fields(fields ...Field) {
	kept := make([]Field, 0, len(fields))
	width := 0
	for _, f := range fields {
		if IsBlank(f.Value) {
			continue
		}
		kept = append(kept, f)
		width = max(width, ansi.StringWidth(f.Label)+1) // +1 for the colon
	}

	for _, f := range kept {
		label := f.Label + ":"
		pad := width - ansi.StringWidth(label)
		v.writeAligned(label+strings.Repeat(" ", pad)+rowGutter, f.Value)
	}
}

// writeAligned writes one prefix+value line, soft-wrapping value to what
// remains of Output().Width() (x/ansi.WrapSoft, which already no-ops for
// non-TTY output per clog/layout.md) and indenting continuation lines
// under prefix's VISIBLE width (x/ansi.StringWidth — prefix or value may
// carry ANSI from a colored label or value, so measuring bytes or runes
// would misjudge the column).
//
// Split out from Fields rather than inlined there because the prefix
// width it wraps against is the BLOCK's, not the row's: Fields measures
// the widest label once and every row wraps to that same column.
// Inlined, the natural thing to reach for inside the per-row loop is the
// row's own label width, which would wrap a long-label row under a wider
// indent than a short-label row in the same block and break the
// alignment the block exists to give.
func (v *View) writeAligned(prefix, value string) {
	// SplitLinesRaw for the same reason Block uses it: a field value can
	// carry Jira-authored CRLF, and splitting on "\n" alone would leave a
	// stray "\r" at the end of every continuation line. Lossless, so an
	// intentional blank line inside a wrapped value still survives.
	lines := xstrings.SplitLinesRaw(value)
	prefixWidth := ansi.StringWidth(prefix)
	if width := v.Output().Width(); width > 0 {
		value = strings.Join(lines, "\n")
		remaining := width - prefixWidth
		if remaining <= 0 {
			v.Line(prefix + value)
			return
		}
		wrapped := xstrings.SplitLinesRaw(ansi.WrapSoft(value, remaining))
		// A grapheme wider than the remaining column cannot be split. Use
		// the whole terminal budget rather than indenting it past the edge.
		if xslices.AnyFunc(wrapped, func(line string) bool { return ansi.StringWidth(line) > remaining }) {
			v.Line(prefix + value)
			return
		}
		lines = wrapped
	}
	indent := strings.Repeat(" ", prefixWidth)
	for i, line := range lines {
		if i == 0 {
			v.Output().WriteLine(prefix + line + "\n")
			continue
		}
		v.Output().WriteLine(indent + line + "\n")
	}
}

// Block writes text verbatim, one physical line at a time — the escape
// hatch for content a caller has already laid out and wrapped itself
// (today, internal/adf's rendered issue description, wrapped to
// Output().Width() before Block ever sees it), where Fields/Line's own
// row layout does not apply. Unlike writeAligned, Block does not
// wrap or indent continuation lines: the caller already decided both.
func (v *View) Block(text string) {
	if text == "" {
		return
	}
	// TrimSuffixes drops ONE trailing line ending, so a
	// newline-terminated block does not write an extra blank line at the
	// end. x/strings' variadic form rather than strings.TrimSuffix
	// because it removes the FIRST matching suffix only: "\r\n" is
	// listed ahead of "\n" so a CRLF ending goes whole, instead of
	// losing its "\n" and stranding the "\r". internal/adf.Render
	// already trims its own trailing newline, so this is dormant for
	// that caller today — but Block is a general escape hatch, and a
	// future caller should not have to know to trim first.
	//
	// SplitLinesRaw, never x/strings.SplitLines: the "Raw" half is the
	// whole point. SplitLines delegates to SplitBy, which trims each
	// part and DROPS empty ones — on a rendered description that would
	// silently delete every blank line between paragraphs and every
	// leading indent, which is precisely the layout the caller already
	// decided and Block exists to pass through untouched. It also
	// normalizes any REMAINING CRLF, which a plain Split would leave as
	// a stray "\r" at the end of every line.
	for _, line := range xstrings.SplitLinesRaw(xstrings.TrimSuffixes(text, "\r\n", "\n")) {
		v.Output().WriteLine(line + "\n")
	}
}

// Section writes a titled section header — the title followed by a rule
// filling the width (clog's Divider: left-aligned title, default '─'), so
// the block below it reads as a distinct part of the view. It degrades to
// an 80-column rule off-TTY and carries no color of its own, so it stays
// clean under NO_COLOR and redirection. Separates the fields block from
// the description, and each future section (comments, subtasks, linked
// issues) from the next. Titles wider than the terminal wrap without
// truncation before the native divider applies its styling and layout.
func (v *View) Section(title string) {
	// Clog's Divider does not wrap a title wider than the terminal. Wrap
	// before its native layout and styling; width zero preserves pipe output.
	v.logger.Divider().Msg(ansi.WrapSoft(title, v.Output().Width()))
}

// Hint writes a line of guidance — "raise --limit for more", "N of ~1,200"
// — through clog's own message path, so text a caller wrapped in
// backticks renders in clog's inline-code style and the flag it names
// stands out from the prose around it.
//
// Through the message path rather than styled here and handed to Line:
// clog already owns that rendering, and re-invoking it by hand is the
// duplication the gechr-first rule exists to stop. With color off the
// path leaves the backticks as literal text, exactly as rendering it
// here did, so a pipe sees the same bytes it always saw.
//
// A hint is guidance, not data: it names what to type next. That is why
// it is the one prose surface here, and why a value still goes through
// its own sink before being interpolated into the text.
func (v *View) Hint(text string) {
	v.logger.Info().Msg(text)
}

// Line writes cells as one line, joined by the same gutter Fields uses
// between label and value, so a Line drawn alongside a Fields block reads
// as part of the same layout rather than a different one.
//
// Rows wider than the terminal soft-wrap with native ANSI, hyperlink and
// grapheme preservation. Fitting rows keep their gutters; width zero keeps
// redirected bytes unchanged. The message path would add prose formatting
// to already rendered values, so Line uses the shared output directly.
//
// WriteLine expects an already-terminated line (clog docs: "a fully
// formatted log line... ending in a newline") rather than adding one
// itself, so Line appends it here instead of relying on the writer to.
func (v *View) Line(cells ...string) {
	v.Output().WriteLine(ansi.WrapSoft(strings.Join(cells, rowGutter), v.Output().Width()) + "\n")
}
