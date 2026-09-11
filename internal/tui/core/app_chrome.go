// The App's chrome: tab bar, brand label, footer rule and
// hint line, and the modal help sheet.

package core

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/gechr/primer/helpsheet"
	pkey "github.com/gechr/primer/key"
	termansi "github.com/gechr/x/ansi"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/browser"
	"github.com/matcra587/jira-cli/internal/tui/components/activity"
	"github.com/matcra587/jira-cli/internal/tui/icons"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

// HintSegment renders one key/description hint. A single-letter key (with or
// without a modifier prefix) highlights its mnemonic inside the description —
// the "(t)ransition" style — so the eye reads the word and the key at once;
// anything else falls back to "key desc". Shared by the footer hint line and
// the sections' own hint rows so the two can never drift apart.
func HintSegment(styles Styles, hintKey, desc string) string {
	if seg, ok := pkey.Inline(hintKey, desc, styles.HintKey, styles.HintDesc); ok {
		return seg
	}
	return styles.HintKey.Render(hintKey) + " " + styles.HintDesc.Render(desc)
}

// newHelpDialog builds the full-keymap sheet as a stack dialog: shared
// navigation and chrome bindings plus the active section's contextual ones,
// deduplicated by key. The dialog Shell frames the sheet, so it renders
// without its own box.
func (a App) newHelpDialog() helpDialog {
	k := a.ctx.Keys
	// App-level chrome bindings lead so the first-occurrence dedupe can never
	// let a section binding shadow them in the sheet.
	bindings := []key.Binding{
		k.Up, k.Down, k.Top, k.Bottom, k.PageUp, k.PageDown,
		k.NextSection, k.PrevSection,
		k.GrowPreview, k.ShrinkPreview, k.Zoom, k.TogglePause,
	}
	if s := a.build(a.activeID()); s != nil {
		bindings = append(bindings, s.HelpBindings()...)
	}
	bindings = append(bindings, k.CommandPalette, k.CopyKey, k.CopyURL, k.OpLog, k.Help, k.Quit)

	seen := make(map[string]bool, len(bindings))
	pairs := make([]helpsheet.Pair, 0, len(bindings))
	for _, b := range bindings {
		hb := b.Help()
		if !b.Enabled() || hb.Key == "" || seen[hb.Key] {
			continue
		}
		seen[hb.Key] = true
		pairs = append(pairs, helpsheet.Pair{Key: hb.Key, Desc: hb.Desc})
	}
	return helpDialog{
		pairs:   pairs,
		dismiss: "press any key to close",
		styles: helpsheet.Styles{
			Key:     a.ctx.Styles.HintKey,
			Text:    a.ctx.Styles.Footer,
			Dismiss: a.ctx.Styles.HintDesc,
			// The sheet draws its own box in the shared overlay style, so the
			// help menu matches the other overlays. It must be self-drawn:
			// helpsheet sizes its columns from the box, so rendering it bare
			// narrows the layout and wraps the longer descriptions.
			Box: a.ctx.Styles.Overlay,
		},
	}
}

// tabBar renders the section titles as pills, the active one reverse-video,
// each with its loaded item count when the section reports one. The brand
// label (product + version) is right-aligned on the same row.
// Titles come from cached section instances, so no section is built per frame.
func (a App) tabBar() string {
	if len(a.order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(a.order))
	for i := range a.order {
		parts = append(parts, a.tabLabel(i))
	}
	row := strings.Join(parts, " ")
	if brand := a.brand(); brand != "" {
		if fill := a.ctx.ScreenWidth - lipgloss.Width(row) - lipgloss.Width(brand); fill >= 0 {
			row += strings.Repeat(" ", fill) + brand
		}
	}
	// Clamp to the screen: on a narrow terminal a wrapped tab row would push
	// the whole fixed-height chrome down a line. tabAt hit-tests only what
	// remains visible, which is exactly right.
	return termansi.Truncate(row, a.ctx.ScreenWidth, "…")
}

// tabLabel renders one tab cell (title, optional count, active styling) —
// the single source for both tabBar and the click hit-test, so their
// geometry can never drift.
func (a App) tabLabel(i int) string {
	id := a.order[i]
	label := string(id)
	if s := a.build(id); s != nil {
		label = s.Title()
		if c, ok := s.(Counter); ok {
			if n, has := c.Count(); has {
				label = fmt.Sprintf("%s (%d)", label, n)
			}
		}
	}
	if i == a.current {
		return a.ctx.Styles.TabActive.Render(label)
	}
	return a.ctx.Styles.TabInactive.Render(label)
}

// tabAt maps a click x in the tab row to a section index by walking the same
// rendered cells tabBar lays out (tabs joined by one space).
func (a App) tabAt(x int) (int, bool) {
	pos := 0
	for i := range a.order {
		w := lipgloss.Width(a.tabLabel(i))
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1
	}
	return 0, false
}

// brand is the right-aligned product label in the tab row: the app name plus
// the build version ("jira v1.2.3"). A bare numeric version gains a v prefix;
// non-release strings ("dev") render as-is. Empty Version hides the label.
func (a App) brand() string {
	v := a.ctx.Version
	if v == "" {
		return ""
	}
	if v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return a.ctx.Styles.Brand.Render("jira") + " " + a.ctx.Styles.FooterRule.Render(v) + " "
}

// footer is two lines: a labeled border carrying the profile/project/board
// context on the left and the help affordance on the right, then a centered hint
// line of the active section's bindings. A non-blocking error replaces both.
func (a App) footer() string {
	if a.ctx.Err != nil {
		// Pad to the two rows chrome reserves so the footer stays bottom-anchored
		// in the error state too.
		return a.ctx.Styles.Error.Render("⚠ "+a.ctx.Err.Error()) + "\n"
	}
	w := a.ctx.ScreenWidth
	h := a.ctx.Keys.Help.Help()
	help := HintSegment(a.ctx.Styles, h.Key, h.Desc)
	// The mutation status slot sits left of the help affordance on the border
	// row; it is empty until the first write is recorded.
	right := help
	if status := a.activityStatus(); status != "" {
		right = status + a.ctx.Styles.FooterRule.Render("  ·  ") + help
	}
	return a.labeledBorder(w, a.contextLine(), right) + "\n" + a.hintLine(w)
}

// activityStatus renders the footer's mutation status slot from the activity
// registry: the most-recent operation's text (pending, resolved, or failed),
// prefixed with an [N] counter when more than one write is in flight, and with
// the affected issue key rendered as an OSC-8 hyperlink. Empty when nothing has
// been recorded, so an untouched session shows only context and help.
func (a App) activityStatus() string {
	reg := a.ctx.Activity
	if reg == nil {
		return ""
	}
	e, ok := reg.Recent()
	if !ok {
		return ""
	}
	var (
		text string
		st   lipgloss.Style
	)
	switch e.Status {
	case activity.StatusPending:
		text, st = e.Pending+"…", theme.StatusInProgress
	case activity.StatusDone:
		text, st = "✓ "+e.Done, theme.StatusDone
	case activity.StatusFailed:
		text, st = "✗ "+e.Pending+" failed", theme.StatusErr
	}
	if n := reg.InFlight(); n > 1 {
		text = fmt.Sprintf("[%d] %s", n, text)
	}
	return st.Render(linkIssueKey(text, e.IssueKey, a.ctx.BaseURL))
}

// linkIssueKey rewrites the issue key inside an activity entry's text as an
// OSC-8 hyperlink when the entry names one and the site root is known. The
// footer status slot and the activity log share it so their linking can't
// drift.
func linkIssueKey(text, key, baseURL string) string {
	url := browser.IssueURL(baseURL, key)
	if url == "" || !strings.Contains(text, key) {
		return text
	}
	return strings.Replace(text, key, adf.Hyperlink(url, key), 1)
}

// labeledBorder draws a horizontal rule with an optional label embedded at each
// end: ── <left> ─────────── <right> ──. Widths are measured for display, so
// styled labels keep the fill correct.
func (a App) labeledBorder(width int, left, right string) string {
	rule := a.ctx.Styles.FooterRule.Render("─")
	gap := a.ctx.Styles.FooterRule.Render(" ")
	// Keep the left label from overrunning the row (a long profile/board name
	// would otherwise wrap the border and break the fixed-height layout). Reserve
	// room for the right label plus the four rule glyphs and three gaps.
	if budget := width - lipgloss.Width(right) - 7; budget > 0 && lipgloss.Width(left) > budget {
		left = xstrings.Truncate(left, budget, "…")
	}
	var leftPart, rightPart string
	if left != "" {
		leftPart = rule + rule + gap + a.ctx.Styles.Footer.Render(left) + gap
	}
	if right != "" {
		rightPart = gap + right + gap + rule + rule
	}
	fillW := max(width-lipgloss.Width(leftPart)-lipgloss.Width(rightPart), 0)
	return leftPart + a.ctx.Styles.FooterRule.Render(strings.Repeat("─", fillW)) + rightPart
}

// hintLine renders the active section's key bindings left-to-right, centered,
// stopping with an ellipsis once they would overflow the width.
func (a App) hintLine(width int) string {
	sec := a.build(a.activeID())
	if sec == nil {
		return ""
	}
	const sep = "  "
	sepW := lipgloss.Width(sep)
	var parts []string
	used := 0
	for _, b := range sec.HelpBindings() {
		if !b.Enabled() {
			continue
		}
		hb := b.Help()
		if hb.Key == "" {
			continue
		}
		seg := HintSegment(a.ctx.Styles, hb.Key, hb.Desc)
		need := lipgloss.Width(seg)
		if len(parts) > 0 {
			need += sepW
		}
		if used+need > width {
			ell := a.ctx.Styles.HintDesc.Render("…")
			if used+sepW+lipgloss.Width(ell) <= width {
				parts = append(parts, ell)
			}
			break
		}
		parts = append(parts, seg)
		used += need
	}
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, strings.Join(parts, sep))
}

// contextLine joins the non-empty profile/project/board labels for the footer.
func (a App) contextLine() string {
	var parts []string
	if a.paused {
		// The paused heartbeat is invisible otherwise — surface it where the
		// user's eye already rests between refreshes.
		parts = append(parts, icons.Active().Paused+" paused")
	}
	for _, p := range []string{a.ctx.ProfileName, a.ctx.Project, a.ctx.Board} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "  ·  ")
}
