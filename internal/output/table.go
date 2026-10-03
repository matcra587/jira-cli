package output

import (
	"image/color"
	"slices"

	"github.com/gechr/clib/theme"
	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"
)

// Table renders items as an aligned, column-flexed table and writes the
// result to v — the list-layout primitive the design doc's "Table
// (RESOLVED: gechr/primer/table)" section commits to: a STATIC renderer
// (its Renderer.Render returns a string-producing value, never a
// bubbletea component) that imports nothing beyond stdlib, x/ansi and
// charmbracelet/x/ansi — verified against the pinned v0.1.4 via
// `go list -deps`, so adding it drags no TUI runtime into the binary.
//
// It sits beside Fields/Line/Block/Section rather than inside one of
// them because a table's column math — flex shrinking, per-column width,
// stable nil-last sort — is primer's job, not a hand-rolled
// strings.Repeat pad loop; the H1 guard exists specifically so a
// renderer reaches for a View primitive instead of writing that loop
// itself (tests/guardrails/human_view_primitives.go's own doc comment
// names config/list.go's pad() as the reviewed, temporary debt this
// primitive replaces once a caller migrates to it).
//
// Table is a package-level function, not a *View method, for one
// mechanical reason: Go does not allow a method to declare its own type
// parameters, and T — the row type — is different for every list command
// (an issue, a comment, whatever `jira issue list` and its successors
// render). A free function taking v as its first argument is the
// standard shape for "a generic operation over an existing non-generic
// value" this language leaves once methods are off the table.
//
// th is the resolved theme (cli.ResolvedTheme() at the call site) rather
// than something Table resolves itself: internal/output cannot import
// internal/cli — internal/cli already imports internal/output for the
// machine side, so the reverse direction would cycle — so the one piece
// of state Table cannot already know on its own (WHICH theme) has to
// come from the caller. Everything Table CAN already answer for
// itself — terminal width, TTY-ness, whether color is active — it reads
// from v.Output() instead of taking a second, possibly-disagreeing
// answer as a parameter: a table that disagreed with the rest of its own
// View about width or color would be exactly the inconsistency this
// whole output-surface layer exists to prevent.
//
// columns and items are primer's own generic column/row types verbatim —
// Table does not wrap them in a jira-cli-specific shape, because doing
// so would just be a second name for the same two slices. A column's
// own Render func is where cli.Entity/cli.Status/cli.Priority/cli.Text
// get called (see StyledCell's own doc comment for why that matters);
// Table never touches a value sink itself.
//
// Table's own WithTermWidth is appended AFTER opts — last wins,
// so nothing a caller passes can override the View's own resolved width. Only the options genuinely left to a caller's
// judgment (WithReverse, WithShowIndex, WithHeaderRenderer,
// WithGridOptions) are meant to be supplied here.
//
// An empty items slice writes nothing at all, not a placeholder
// "no results" line: primer's own Render returns an empty RenderedTable
// for zero rows (deliberately not even a bare header — see
// RenderHeaderOnly's own doc for the alternative primer offers, unused
// here), and Table passes that straight through Block, which already
// no-ops on "". A generic layout primitive has no business inventing a
// domain-specific empty-state message (renderCommentList's own "No
// comments." is exactly this decision, made by the caller who knows
// what an empty list of ITS rows means) — Table stays honest about not
// knowing.
func Table[T any](v *View, th *theme.Theme, columns []table.Column[T], items []T, opts ...table.Option) {
	out := v.Output()
	// Styling requires both a terminal and enabled colors. Primer's grid
	// padding is plain text; headers and hyperlinks use this shared gate.
	styled := out.IsTTY() && !out.ColorsDisabled()

	ctx := table.NewRenderContext(tableTheme{th: th, styled: styled}, ansi.New(ansi.WithTerminal(styled)))
	// Concat rather than a nested append: it puts the "caller's options
	// first, ours last" ordering the doc above depends on in one readable
	// expression, where two nested appends have to be unwound to see it.
	// Both forms copy into a fresh slice — the caller's own array was
	// never reachable either way.
	rendererOpts := slices.Concat(opts, []table.Option{
		table.WithTermWidth(out.Width()),
	})
	rendered := table.NewRenderer(columns, ctx, rendererOpts...).Render(items)
	v.Block(rendered.String())
}

// tableTheme adapts a clib theme.Theme into gechr/primer/table's own
// Theme interface (RenderBold/RenderDim/EntityColors) — the "Its Theme
// is an interface cli.ResolvedTheme() satisfies/adapts" the design doc
// promises, made literal: clib's Theme carries Bold/Dim as
// *lipgloss.Style fields and EntityColors as a plain []color.Color
// field, neither of which is a method, so something has to bridge them
// to primer's method-shaped interface rather than clib's Theme
// satisfying it by accident.
//
// styled is resolved once by Table's own caller (this file), not
// re-derived here: it mirrors cli.styleOrPlain's nil-style-or-disabled
// gate (internal/cli/sinks.go) so a Monochrome theme or a color-disabled
// invocation renders every header and index number in this table exactly
// as plain as StyleEnabled() already makes Status/Priority/Entity
// elsewhere — table headers must not be the one place color sneaks back
// in when everything else in the same View went plain.
type tableTheme struct {
	th     *theme.Theme
	styled bool
}

// RenderBold renders s in the theme's bold style — primer's own default
// header renderer calls this unconditionally for every column header
// (table.go's format/RenderHeaderOnly), so this must degrade to s
// unstyled rather than panic or ignore styled when th.Bold is nil
// (Monochrome's own documented meaning for a nil style slot).
func (t tableTheme) RenderBold(s string) string {
	if !t.styled || t.th.Bold == nil {
		return s
	}
	return t.th.Bold.Render(s)
}

// RenderDim renders s in the theme's dim style — used by primer's
// WithShowIndex row-number column (table.go's format). See RenderBold
// for why the nil/unstyled fallback is required, not optional.
func (t tableTheme) RenderDim(s string) string {
	if !t.styled || t.th.Dim == nil {
		return s
	}
	return t.th.Dim.Render(s)
}

// EntityColors exposes the theme's entity palette to primer's own
// AssignEntityColor helper, for a future column Render func that wants
// primer to pick a per-key color itself rather than pre-styling text
// with cli.Entity. Returns nil when unstyled, matching
// AssignEntityColor's own empty-palette handling (returns a nil color
// rather than indexing an empty slice) — an unstyled table must not
// hand out colors nobody asked it to use.
func (t tableTheme) EntityColors() []color.Color {
	if !t.styled {
		return nil
	}
	return t.th.EntityColors
}
