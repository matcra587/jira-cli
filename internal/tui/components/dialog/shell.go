package dialog

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	lipgloss "charm.land/lipgloss/v2"

	pkey "github.com/gechr/primer/key"
	"github.com/gechr/primer/overlay"
	"github.com/gechr/primer/prompt"
	"github.com/gechr/primer/scrollbar"
)

// Styles are the Shell's render styles, injected by the owner so the package
// stays theme-agnostic.
type Styles struct {
	// Box draws the border and padding around the whole dialog; inject the
	// app's overlay style.
	Box lipgloss.Style
	// Title styles the heading line above the body.
	Title lipgloss.Style
	// HintKey and HintText style the key and description halves of the foot
	// row, matching the rest of the TUI's hint rendering.
	HintKey  lipgloss.Style
	HintText lipgloss.Style
	// Scrollbar styles the internal scrollbar shown when the body overflows
	// the height cap.
	Scrollbar scrollbar.Styles
}

// ShellConfig declares a Shell's chrome and sizing bounds.
type ShellConfig struct {
	Styles Styles
	// MaxWidth and MaxHeight cap the box's total size in columns and rows; 0
	// disables the cap. They keep a long prefill or a tall body from bleeding
	// past a large screen.
	MaxWidth  int
	MaxHeight int
	// WidthFraction and HeightFraction cap the box to a share of the screen in
	// (0,1]; 0 disables. They bind on small screens where the absolute caps do
	// not.
	WidthFraction  float64
	HeightFraction float64
	// Margin keeps this many columns and rows clear at the screen edges when
	// the screen, not a cap, is the binding constraint.
	Margin int
	// Scrollbar configures the internal scrollbar's glyphs and geometry.
	Scrollbar scrollbar.Config
}

// Shell frames a dialog: it caps the box to a fraction of the screen and to an
// absolute maximum, scrolls an overflowing body internally, and centers the
// result over a backdrop. It carries value semantics and is safe to copy — the
// Stack holds one by value.
type Shell struct {
	cfg      ShellConfig
	viewport viewport.Model
}

// NewShell builds a Shell from cfg. It constructs the viewport once so
// RenderScrollable reuses its default key mappings and configuration.
func NewShell(cfg ShellConfig) Shell {
	return Shell{cfg: cfg, viewport: viewport.New()}
}

// clampWidth is the maximum total box width for a screen.
func (s Shell) clampWidth(screenW int) int {
	return clampAxis(screenW, s.cfg.Margin, s.cfg.WidthFraction, s.cfg.MaxWidth)
}

// clampHeight is the maximum total box height for a screen. modal.Frame had no
// height cap; adding one is what lets an oversized body scroll inside the box
// instead of overrunning the screen.
func (s Shell) clampHeight(screenH int) int {
	return clampAxis(screenH, s.cfg.Margin, s.cfg.HeightFraction, s.cfg.MaxHeight)
}

// clampAxis is the maximum total box size along one axis: the smaller of the
// absolute cap, the fractional cap, and the screen minus the edge margin,
// floored at one so a tiny terminal can never push a negative size into
// lipgloss.
func clampAxis(screen, margin int, fraction float64, limit int) int {
	v := screen - margin
	if fraction > 0 {
		if f := int(float64(screen) * fraction); f < v {
			v = f
		}
	}
	if limit > 0 && limit < v {
		v = limit
	}
	return max(1, v)
}

// Frame renders d's title, body, and hints into a box, scrolls the composed
// content when it exceeds the height cap, and composites it centered over
// backdrop, which must already be screenW columns by screenH rows.
func (s Shell) Frame(backdrop string, d Dialog, screenW, screenH int) string {
	frameW := s.cfg.Styles.Box.GetHorizontalFrameSize()
	frameH := s.cfg.Styles.Box.GetVerticalFrameSize()

	maxInnerW := max(1, s.clampWidth(screenW)-frameW)
	viewportH := max(1, s.clampHeight(screenH)-frameH)

	// A footered dialog (a scrolling form) pins its foot row below the viewport,
	// so the body scrolls but the hint/confirm/submitting row is always visible.
	if f, ok := d.(Footered); ok {
		return s.frameFootered(backdrop, d, f, screenW, screenH, maxInnerW, viewportH, frameW)
	}

	title := s.renderTitle(d.Title())
	hints := s.renderHints(d.Hints())

	// Wrap the body to the full inner width first. At its widest wrap the body
	// spans the fewest rows, so a body that fits the height cap here needs no
	// scrollbar and keeps every column — content that fits is never wrapped a
	// column early. Only a body that still overflows surrenders the last inner
	// column to the scrollbar, and is re-wrapped one column narrower so the
	// viewport never clips its final column once the scrollbar appears.
	body := d.Content(maxInnerW)
	// A body wider than the box means the dialog ignored the offered width (a
	// fixed-measure form on a tiny terminal); boxing it anyway would wrap its
	// borders into unreadable interleaved fragments, so show the notice instead.
	if lipgloss.Width(body) > maxInnerW {
		return s.frameTooNarrow(backdrop, screenW, screenH, frameW)
	}
	inner := joinNonEmpty(title, body, hints)

	// RenderScrollable renders the body through Box.Width(BoxWidth), and lipgloss
	// counts the border and padding inside that width; BoxWidth is therefore the
	// box's total width, not its text width, so the frame has to be added back or
	// the text area comes up frameW columns short and wraps. (The predecessor's
	// bare viewW+1 omitted frameW, wrapping a bordered body a column early.)
	viewW := min(lipgloss.Width(inner), maxInnerW)
	boxW := viewW + frameW
	// lipgloss.Height counts \n-separated lines, matching RenderScrollable's own
	// overflow test, so this predicts exactly when it will show a scrollbar.
	if lipgloss.Height(inner) > viewportH {
		viewportW := max(1, maxInnerW-1)
		// Re-wrap only when the body actually uses the surrendered column; a
		// narrower body renders byte-identical, so the second Content call
		// would be pure waste.
		if lipgloss.Width(body) > viewportW {
			body = d.Content(viewportW)
			inner = joinNonEmpty(title, body, hints)
		}
		viewW = min(lipgloss.Width(inner), viewportW)
		boxW = viewW + frameW + 1 // + the reserved scrollbar column
	}

	// A scroll-hinting dialog (a tall form) asks the viewport to follow its
	// focus. The hint's top is relative to the body, so offset it past the
	// Shell-drawn title, then prime the viewport's bounds before setting the
	// offset — SetYOffset clamps against the viewport's current height and
	// content, so priming first is what keeps a valid offset from collapsing to
	// zero (RenderScrollable re-sets the same bounds, leaving the offset intact).
	if sh, ok := d.(ScrollHint); ok {
		if top, height, ok := sh.ScrollTo(); ok {
			if title != "" {
				top += lipgloss.Height(title)
			}
			s.viewport.SetWidth(max(1, viewW))
			s.viewport.SetHeight(viewportH)
			s.viewport.SetContent(inner)
			s.viewport.SetYOffset(scrollOffset(top, height, viewportH))
		}
	}

	scrollable := prompt.RenderScrollable(prompt.ScrollableModel{
		BoxStyle:        s.cfg.Styles.Box,
		BoxWidth:        boxW,
		Content:         inner,
		ViewportHeight:  viewportH,
		ViewWidth:       viewW,
		View:            s.viewport,
		ScrollbarConfig: s.cfg.Scrollbar,
		Styles:          prompt.Styles{Scrollbar: s.cfg.Styles.Scrollbar},
	})
	return overlay.Place(backdrop, scrollable, screenW, screenH, overlay.Center)
}

// frameFootered frames a Footered dialog: the foot row is pinned at the bottom
// of the box and the body scrolls in the space above it, so the body follows
// focus (via ScrollHint) while the hint/confirm row never scrolls off. It
// composes the viewport and scrollbar directly rather than through
// RenderScrollable, which owns its own box and could not host a pinned row
// inside it.
func (s Shell) frameFootered(backdrop string, d Dialog, f Footered, screenW, screenH, maxInnerW, viewportH, frameW int) string {
	footer := f.Footer()
	// The body gets whatever height the foot row leaves; at least one row, so a
	// tiny terminal still shows a sliver of body rather than none.
	bodyH := max(1, viewportH-lipgloss.Height(footer))

	// The scrolling part carries the same title/body/hints assembly as Frame —
	// implementing Footered moves only where the foot row is pinned, it never
	// voids the base Dialog contract's other parts.
	title := s.renderTitle(d.Title())
	hints := s.renderHints(d.Hints())

	// Wrap the body to the full inner width; only when the assembly still
	// overflows bodyH does it surrender the last column to the scrollbar and
	// re-wrap — the same fits-first rule Frame uses, so a body that fits keeps
	// every column. The trailing newline is trimmed so joining the footer adds
	// exactly one row between them, not a blank line.
	body := strings.TrimSuffix(d.Content(maxInnerW), "\n")
	// Same guard as Frame: a body or foot row wider than the box cannot be
	// boxed legibly, so the notice stands in until the terminal widens.
	if lipgloss.Width(body) > maxInnerW || lipgloss.Width(footer) > maxInnerW {
		return s.frameTooNarrow(backdrop, screenW, screenH, frameW)
	}
	inner := joinNonEmpty(title, body, hints)
	viewW := min(lipgloss.Width(inner), maxInnerW)
	scrolls := lipgloss.Height(inner) > bodyH
	if scrolls {
		vw := max(1, maxInnerW-1)
		// Same guard as Frame: a body narrower than the surrendered column
		// re-renders byte-identical, so skip the second Content call.
		if lipgloss.Width(body) > vw {
			body = strings.TrimSuffix(d.Content(vw), "\n")
			inner = joinNonEmpty(title, body, hints)
		}
		viewW = min(lipgloss.Width(inner), vw)
	}

	bodyView := inner
	if scrolls {
		off := 0
		if sh, ok := d.(ScrollHint); ok {
			if top, height, ok := sh.ScrollTo(); ok {
				// The hint is body-relative; offset it past the Shell-drawn title,
				// exactly as Frame does.
				if title != "" {
					top += lipgloss.Height(title)
				}
				off = scrollOffset(top, height, bodyH)
			}
		}
		vp := s.viewport
		vp.SetWidth(max(1, viewW))
		vp.SetHeight(bodyH)
		vp.SetContent(inner)
		vp.SetYOffset(off)
		bar := scrollbar.Model{
			Config:     s.cfg.Scrollbar,
			Height:     bodyH,
			TotalLines: vp.TotalLineCount(),
			Percent:    vp.ScrollPercent(),
			Styles:     s.cfg.Styles.Scrollbar,
		}.Render()
		bodyView = lipgloss.JoinHorizontal(lipgloss.Top, vp.View(), bar)
	}

	outer := lipgloss.JoinVertical(lipgloss.Left, bodyView, footer)
	boxW := lipgloss.Width(outer) + frameW
	boxed := s.cfg.Styles.Box.Width(boxW).Render(outer)
	return overlay.Place(backdrop, boxed, screenW, screenH, overlay.Center)
}

// frameTooNarrow is the fallback frame when a dialog's rendering is wider than
// the clamped box can hold. Only the rendering is replaced: the dialog stays
// open and keyed, so esc still cancels it and widening the terminal restores
// the real content on the next frame.
func (s Shell) frameTooNarrow(backdrop string, screenW, screenH, frameW int) string {
	notice := s.cfg.Styles.HintText.Render("terminal too narrow")
	boxW := min(lipgloss.Width(notice)+frameW, screenW)
	boxed := s.cfg.Styles.Box.Width(boxW).Render(notice)
	return overlay.Place(backdrop, boxed, screenW, screenH, overlay.Center)
}

// scrollOffset is the viewport's top line for keeping [top, top+height)
// visible in a window of viewportH lines: zero while the region already fits
// above the fold, otherwise just far enough to reveal its bottom — but never
// past its top, so a region taller than the window still shows its start rather
// than scrolling clean through it. The viewport clamps the result to its own
// content bounds.
func scrollOffset(top, height, viewportH int) int {
	if height < 1 {
		height = 1
	}
	off := 0
	if top+height > viewportH {
		off = top + height - viewportH
	}
	if off > top {
		off = top
	}
	return off
}

// renderTitle styles the heading, or returns "" when the dialog has none.
func (s Shell) renderTitle(title string) string {
	if title == "" {
		return ""
	}
	return s.cfg.Styles.Title.Render(title)
}

// renderHints renders the foot row with the Shell's injected styles.
func (s Shell) renderHints(hints []pkey.Hint) string {
	return RenderHints(s.cfg.Styles.HintKey, s.cfg.Styles.HintText, hints)
}

// RenderHints renders a hint row through primer's inline key renderer, the
// same "(y)es" styling the rest of the TUI uses; no hints means no row. Shared
// so a Footered dialog pinning its own hint row renders it identically to the
// Shell's.
func RenderHints(key, text lipgloss.Style, hints []pkey.Hint) string {
	if len(hints) == 0 {
		return ""
	}
	prefix := " "
	return pkey.Renderer{
		Gap:    "   ",
		Styles: pkey.Styles{Key: key, Text: text},
		Prefix: &prefix,
		Inline: true,
	}.Render(hints)
}

// joinNonEmpty stacks the parts that carry content, so an absent title or hint
// row leaves no blank line behind.
func joinNonEmpty(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, kept...)
}
