package cli

import (
	"image/color"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/gechr/clib/theme"
	"github.com/gechr/clog/style"
	clogtheme "github.com/gechr/clog/theme"
	"github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	"github.com/gechr/x/palette"
	xslices "github.com/gechr/x/slices"
	"github.com/gechr/x/terminal"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/output"
)

// Pure value sinks for the human output surface (design doc: "The View
// (formatter/emitter split, gh-shaped)"). Each takes a value and returns
// a styled or sanitized string — no I/O, no writer, unit-testable in
// isolation. They live here, beside ResolvedTheme and StyleEnabled,
// rather than in internal/output where the View emitter lives: Status,
// Priority and Entity all need cli.ResolvedTheme(), which internal/output
// must not reach for: the dependency runs cli -> output (detector.go
// imports it for the machine side), so the reverse would be a cycle —
// which is also why output.Table takes a resolved theme as a parameter
// rather than resolving one itself. A renderer reaches both packages
// already, so the split costs it nothing.

// OrDash substitutes an em-dash placeholder for a sink result with no
// visible content, so "nobody is assigned" reads as an answer rather
// than as an omission — an empty cell says "this view did not fetch
// that", which is a different claim.
//
// Lives here beside the sinks rather than in the one command that first
// needed it: nothing about it is issue-shaped, and the next optional
// column would otherwise re-derive its one non-obvious rule (below) from
// scratch.
//
// Emptiness is output.IsBlank's call, not this function's. A value sink
// wraps even an EMPTY name in real ANSI, so a raw check never sees it —
// View.Fields owns that rule for deciding which rows to drop, and asking
// the same function keeps a dropped row and a dashed cell agreeing about
// the same value.
func OrDash(styled string) string {
	if output.IsBlank(styled) {
		return "-"
	}
	return styled
}

// Entity renders a repeated identity — project, assignee, reporter,
// component, label, sprint, environment — with a stable per-key color
// from OUR OWN entity palette (x/palette.md's reservation recipe),
// resolved once via entityPalette below rather than clib's bundled
// RenderEntity.
//
// Never call this for a status, priority, or any other semantic state:
// Entity HASHES the (canonicalized) name, and hashing semantic state
// would make its color arbitrary rather than meaningful — the opposite
// of what Status and Priority exist for.
//
// RESOLVED GAP: clib v0.7.16's built-in themes do not build their own
// EntityColors with x/palette's WithReserved against their own semantic
// slots — theme.Dark()'s Orange and EntityColors[0] were the identical
// ANSI-256 color (0xd0), confirmed by comparing RGBA values directly —
// and defaultEntityColors always returns x/palette's plain ANSI-256
// DefaultDark/DefaultLight set regardless of what a theme's own Red,
// Green, Yellow, Blue and Orange actually render with, so a true-color
// theme (catppuccin-mocha, say) still hashed entities into ANSI-256:
// inconsistent with Status/Priority AND liable to collide with them.
// entityPalette fixes both by building a SEPARATE palette with
// x/palette.md's own recipe — palette.Auto(WithDark(...), WithReserved(
// semanticColors(...)...)) — so entities land on the same true-color-or-
// ANSI-256 capability decision Status/Priority's own colors already
// made, and can never repeat a color one of them uses for something
// else.
//
// Custom themes supply their own ordered entity colors. Those colors receive
// the same semantic reservation, measured after ANSI-256 conversion when the
// terminal cannot render true color. An empty filtered palette renders plain
// text rather than substituting a reserved color.
//
// The lookup key is canonicalized (trim + lowercase, same fold Status
// and Priority use) before hashing, so "Agent DX", "agent dx" and
// " AGENT DX " all land on the same color; the ORIGINAL name is what
// gets rendered, per x/palette.md ("Canonicalise the identity once
// before color lookup ... Render the original display text").
func Entity(name string) string {
	return EntityAs(name, name)
}

// EntityAs is Entity's color-choosing path, decoupled from what gets
// rendered: the identity color is still hashed from key alone, but the
// styling is applied to text, which may be a larger string built AROUND
// that identity (a divider title combining an author and a timestamp,
// say). Entity(name) is exactly EntityAs(name, name) — ONE
// color-choosing path, not two — so a caller needing this shape never
// hand-rolls the canonicalize/palette lookup a second time.
//
// This exists because coloring only the identity substring and
// concatenating the rest is unsafe once the result is handed to a
// caller that wraps the WHOLE string in its own style (clog's Divider
// title, which unconditionally bolds what it is given): the identity's
// own self-closing ANSI span resets mid-title, and everything after it
// silently loses the outer style. Coloring the full text in one Render
// call emits exactly one reset, at the very end, so nothing can nest.
//
// key and text are sanitized independently — key exactly as Entity
// would sanitize a name before hashing it, text as this function's own
// display copy — so a hostile control byte in either cannot reach the
// terminal regardless of which string a caller happened to reuse for
// both.
func EntityAs(key, text string) string {
	key = sanitize(key)
	text = sanitize(text)
	if !StyleEnabled() {
		return text
	}
	c := entityPalette().Color(canonicalize(key))
	if c == nil {
		// Avoiding emptied the palette entirely (x/palette.md: "Avoiding
		// can remove every color ... Never force a semantic color as an
		// entity fallback") — render plain rather than reach for one of
		// the colors this palette exists to avoid.
		return text
	}
	return lipgloss.NewStyle().Foreground(c).Render(text)
}

// entityPalette resolves the entity color palette exactly once per
// process, mirroring theme.go's ResolvedTheme(): built from the SAME
// resolved theme, so entities land on the same dark/light and
// true-color-capability answer Status and Priority's own colors
// already carry, without a second terminal probe.
var entityPalette = sync.OnceValue(func() palette.Palette {
	return newEntityPalette(ResolvedTheme())
})

// newEntityPalette builds the entity palette from an explicit resolved
// theme. Split out from entityPalette's sync.OnceValue so a test can
// supply theme.Dark()/theme.Light() directly, rather than depending on
// (or trying to reset) the once-per-process cache.
func newEntityPalette(resolved *theme.Theme) palette.Palette {
	reserved := semanticColors(resolved)
	if resolved.String() == "custom" {
		entities := palette.Palette(resolved.EntityColors)
		if !terminal.SupportsTrueColor() {
			// x/palette v0.6.0 only exposes transformed reservation through
			// Auto. Convert both independent slices before using its public
			// Avoiding method so custom colors retain that same guarantee.
			entities = xslices.Map(entities, colorprofile.ANSI256.Convert)
			reserved = xslices.Map(reserved, colorprofile.ANSI256.Convert)
		}
		return entities.Avoiding(reserved...)
	}
	dark := BackgroundIsDark(resolved)
	return palette.Auto(
		palette.WithDark(dark),
		palette.WithReserved(reserved...),
	)
}

// semanticColors collects a theme's real semantic foreground colors —
// the ones Status, Priority and Type actually render with (Red, Orange,
// Yellow, Blue, Green, Magenta) — for palette.WithReserved, so no entity
// color can be mistaken for one of them (x/palette.md: "Reserve the
// configured application's danger, warning, success and information
// foregrounds"). Built once, here, at the composition boundary —
// callers must not duplicate this extraction themselves.
//
// Magenta joined this list alongside Type below: Type colors an Epic
// issue type with it (the near-Jira purple), so an entity hash landing
// on the identical color would read as "this is an Epic" by accident.
// Dim is deliberately NOT here — Type also uses it, for Subtask — because
// Dim carries no hue of its own (clib's theme.Dark() defines it as
// Faint(true) alone, no Foreground color), so there is no COLOR for an
// entity to collide with in the first place; WithReserved's whole job is
// keeping two COLORS apart.
//
// jiraStatusColors' four category colors join the reservation here too,
// for the CURRENT dark/light resolution — and unlike Dim, every one of
// them genuinely needs it: each is a real, hard-coded hex hue (see
// jiraStatusColors' own doc comment for why they are literals rather
// than theme slots), so an entity landing on the identical RGB value as
// jiraBlue or jiraGreen would visually read as "this is a status pill,"
// the exact confusion Magenta's reservation above already guards Type's
// Epic color against. th.Background decides which half of each pair is
// live, the same dark/light question newEntityPalette already resolves
// from this same th — never a second probe.
//
// jiraPriorityRanks' five rung colors join for the same reason and on
// the same terms. They are the OTHER hard-coded Jira vocabulary in this
// file, rendered right beside an entity in a list row (the P column next
// to ASSIGNEE), so an assignee hashing onto the identical red as
// "Highest" reads as a severity signal the name never carried. Reserving
// status but not priority was an omission, not a decision — the two
// tables are peers, and every deliberate exclusion here says so in
// words (see Dim above).
//
// Reserving all five does cost the entity palette real colors, and the
// trade is deliberate: measured on a dark background it drops from 23
// usable hues to 20 (light is unchanged at 12). Only three of the five
// evict anything — "low" is already the same #4BCE97 as
// statusCategoryDone, and one more falls close enough to be de-duplicated
// by distance. Twenty hues still hash assignee names apart well past the
// point a list is readable, where a name rendering in exactly "Highest"
// red misreads as severity every time it appears.
//
// nil and lipgloss.NoColor are both excluded: NoColor's RGBA() still
// answers (0,0,0,0xffff) because the interface has to return something,
// so keeping it would reserve a color nothing is actually styled with.
func semanticColors(th *theme.Theme) []color.Color {
	var out []color.Color
	for _, s := range []*lipgloss.Style{th.Red, th.Orange, th.Yellow, th.Blue, th.Green, th.Magenta} {
		if s == nil {
			continue
		}
		if fg := s.GetForeground(); hasRealColor(fg) {
			out = append(out, fg)
		}
	}
	// Sorted keys, never a bare map range: iteration order is randomized
	// per process, and a reserved list whose ORDER varies run to run is a
	// byte-for-byte reproducibility hazard. (Avoiding happens to be
	// order-independent today — it Rejects from the palette's own fixed
	// order, testing each candidate against the whole reserved set — so
	// this is guarding against an upstream change, not a live bug.)
	//
	// Sorting the map's own keys rather than keeping a hand-written
	// ordered slice beside each table: a second container is a second
	// thing to forget. A rank present in the slice but not the map, or
	// the reverse, fails silently — its color simply stops being
	// reserved — so the slices needed a drift test each to stay honest,
	// where deriving the order from the one authoritative table cannot
	// disagree with itself. Sort order is not severity order, and
	// nothing here needs it to be: every member is reserved regardless.
	dark := BackgroundIsDark(th)
	for _, category := range slices.Sorted(maps.Keys(jiraStatusColors)) {
		out = append(out, statusCategoryColor(dark, category))
	}
	for _, rank := range slices.Sorted(maps.Keys(jiraPriorityRanks)) {
		out = append(out, jiraPriorityRanks[rank].resolve(dark))
	}
	return out
}

// hasRealColor reports whether c is an actually-configured color, not
// lipgloss's own "nothing set" sentinel. Style.GetForeground()/
// GetBackground() document returning lipgloss.NoColor{} — a concrete,
// NON-nil value satisfying color.Color — when that slot was never set
// (charm.land/lipgloss/v2's own get.go), so a plain `c != nil` check
// alone never catches the unset case; statusPill's own first version
// (before jiraStatusColors existed) missed exactly this and always took
// its "real color" branch, even for Dim, which the theme genuinely never
// gives a Foreground color at all — caught by eyeballing the actual
// rendered bytes (design brief's own "look at the bytes" instruction),
// not by any test, since color.Color interface equality made the bug
// invisible to a `!= nil` assertion either way. semanticColors is the
// remaining caller: jiraStatusColors' own values are lipgloss.Color hex
// literals, never NoColor, so statusPill no longer needs this
// distinction itself — but the theme's OWN semantic slots (Red, Orange,
// ...) still can be nil-foreground under a theme like Monochrome, and
// this is still the one place that filtering happens.
func hasRealColor(c color.Color) bool {
	if c == nil {
		return false
	}
	_, unset := c.(lipgloss.NoColor)
	return !unset
}

// StatusCategory is one of Jira's four status-category keys, as a
// defined type so it cannot be confused with the status NAME traveling
// beside it. Status and StatusPadded take (name, category) — two
// strings until this type existed, which meant swapping them compiled
// silently and produced an uncolored pill with a category for a label.
// Exported because the value crosses a package boundary:
// internal/cli/issue reads it off the wire and hands it back here.
//
// Unlike priorityRankKey, this really is a closed set — Jira computes
// the category, and a tenant cannot add a fifth. The empty value is
// still meaningful and deliberately not a constant: it is the "no
// category rode along with this status" signal Status documents, which
// falls back to coloring by name.
type StatusCategory string

// statusCategoryNew, statusCategoryIndeterminate, statusCategoryDone and
// statusCategoryUndefined are Jira's own StatusCategory.Key values — a
// closed, stable, language-independent enum verified live against a
// real tenant ({"colorName":"blue-gray","id":2,"key":"new","name":
// "To Do", ...}). Status keys off THIS rather than the wire's sibling
// colorName field because key is the one Jira documents as the stable
// identifier across locales and tenant renames; colorName is a
// presentation label from Jira's OWN palette that would only add a
// second closed set to keep in sync, for no extra information this CLI
// needs — every category rendered differently below already has its
// own key.
const (
	statusCategoryNew           StatusCategory = "new"
	statusCategoryIndeterminate StatusCategory = "indeterminate"
	statusCategoryDone          StatusCategory = "done"
	statusCategoryUndefined     StatusCategory = "undefined"
)

// colorPair is one Jira-derived hue, resolved for the terminal's dark or
// light background — the same two-value shape ResolvedTheme's own
// Background enum distinguishes, kept as a struct rather than a second
// palette.Auto call because there is nothing PER-KEY here (unlike
// entityPalette, which hashes an open-ended set of names): a fixed pair
// per key, chosen once and never re-derived.
//
// Both Jira-derived vocabularies use it — the four status categories
// (jiraStatusColors) and the five priority ranks (jiraPriorityRanks,
// which embeds it) — so the name stays vocabulary-neutral rather than
// saying "status" to a reader who arrived here through priorityRank.
//
// Field order is (light, dark) to match lipgloss.LightDarkFunc's own
// parameter order, so resolve below can hand the pair straight over
// without transposing. Every literal names its fields, which is what
// makes that ordering safe to rely on.
type colorPair struct{ light, dark color.Color }

// resolve picks the half of the pair this terminal should draw, through
// lipgloss's own selector rather than a hand-written if-dark branch.
// One method for both vocabularies: the rule for choosing a half is the
// same rule wherever a pair appears, and when it was written out per
// caller the two copies were free to drift.
func (p colorPair) resolve(dark bool) color.Color {
	return lipgloss.LightDark(dark)(p.light, p.dark)
}

// jiraStatusColors are LITERAL hex approximations of Atlassian's own
// status-category swatches (statusCategory.colorName off the wire —
// "blue-gray", "blue", "green", "medium-gray"; statusCategoryNew's own
// doc comment above quotes the live tenant response these keys come
// from), keyed by the same statusCategory* constants Status already
// switches on.
//
// Hard-coded rather than a theme slot is the point, not a shortcut: a
// status PILL exists to look like Jira's own lozenge, a shape and hue a
// reader already recognizes from the browser, and clib's theme has
// exactly seven semantic colors (Red, Green, Yellow, Blue, Magenta,
// Orange, BoldGreen) with no neutral, gray-toned entry among them —
// there was never a theme slot this pill COULD have borrowed for "new"
// or "undefined" without also claiming a color some other sink already
// means something specific by (Green means Done everywhere in this
// package; reusing it for "undefined" would say two different things
// with one color). The old code reached for Dim instead — clib's
// theme.Dark() defines Dim as Faint(true) alone, no Foreground color at
// all — and statusPill fell back to SGR-7 reverse video for it, which is
// the solid inverted block a real terminal renders for "style requested,
// no color to paint it with." These four values exist specifically so
// that branch is never reached again: every category below carries a
// real, if approximate, RGB hue, so statusPill (below) always has a
// background to paint.
//
// Values are approximate on purpose — Atlassian's own design tokens
// shift release to release, and pinning this CLI to an exact hex would
// make a cosmetic upstream change a diff here. "Recognizably the same
// family of blue-gray/blue/green/gray a Jira Cloud user already knows"
// is the bar, not pixel parity.
var jiraStatusColors = map[StatusCategory]colorPair{
	// new ("To Do") — Jira's "blue-gray": muted enough to read as "not
	// started" without competing with indeterminate's own blue.
	statusCategoryNew: {dark: lipgloss.Color("#44546F"), light: lipgloss.Color("#8993A4")},
	// indeterminate ("In Progress" and its custom-workflow equivalents)
	// — Jira's "blue", the same family as Jira Cloud's own primary brand
	// blue (≈ #0052CC).
	statusCategoryIndeterminate: {dark: lipgloss.Color("#0C66E4"), light: lipgloss.Color("#0052CC")},
	// done — Jira's "green" (≈ #00875A).
	statusCategoryDone: {dark: lipgloss.Color("#4BCE97"), light: lipgloss.Color("#00875A")},
	// undefined — a status Jira itself could not categorize. Jira's own
	// "medium-gray", one step flatter than blue-gray: no hue lean at
	// all, which is the point — this is Jira's OWN fallback swatch, not
	// this CLI inventing a fifth category.
	statusCategoryUndefined: {dark: lipgloss.Color("#738496"), light: lipgloss.Color("#97A0AF")},
}

// statusCategoryColor resolves category to its Jira-derived pill
// background for the given dark/light background, or nil for an empty
// or unrecognized key — the same "try something else" signal the old
// statusCategoryStyle returned via a nil *lipgloss.Style. dark is
// resolved by the caller from ResolvedTheme().Background, exactly the
// question newEntityPalette already answers for entities — never a
// second, independent "is this terminal dark" probe.
func statusCategoryColor(dark bool, category StatusCategory) color.Color {
	pair, ok := jiraStatusColors[category]
	if !ok {
		return nil
	}
	return pair.resolve(dark)
}

// Status renders a Jira status as a filled PILL — background color plus
// one space of padding either side under GlyphSetDefault, or Powerline
// half-circle caps under GlyphSetNerd (statusPill below) — the same
// "badge" shape Jira Cloud's own UI renders a status in — colored from
// the status's CATEGORY (category is statusCategory.key off the wire;
// see the constants above) rather than its NAME.
//
// This replaces an earlier version that colored by switching on the
// canonicalized NAME alone, which only ever matched Jira Cloud's three
// English defaults ("to do", "in progress", "done") and silently
// rendered PLAIN for every custom workflow status a project renamed or
// added ("Blocked", "In QA", "Ready for Review"), and for a translated
// name on a non-English tenant. Jira itself still knows which of its
// four categories any of those belongs to, so coloring by category
// fixes every one of them at once, in the one place this CLI decides a
// status's color — a caller never has to teach this function a new
// workflow's own vocabulary.
//
// category == "" is the explicit, documented fallback: an older
// response, or a caller that narrowed --fields away from statusCategory
// (it rides the default field set, but is not guaranteed once a caller
// asks for less). An unrecognized category value — Jira adding a fifth
// category after this switch was written — falls back the same way:
// this is Jira's entire documented set today, but trying something
// beats coloring nothing. Both cases fall through to statusNameColor,
// Status's own original name-based map, now reusing the SAME
// jiraStatusColors set rather than a second color choice — the "to do"
// bucket that used to reach for Dim (and so reverse video) now resolves
// through statusCategoryNew instead, closing the bug for this path too.
func Status(name string, category StatusCategory) string {
	return StatusPadded(name, category, 0)
}

// StatusPadded is Status widened to a shared pill width: name is padded
// with trailing spaces to width VISIBLE cells before the pill is built,
// so the background extends past the text and every pill in a column
// renders the same size — "Done" as wide as "To Do" — rather than a
// ragged row of badges each hugging its own word.
//
// The padding goes INSIDE the pill deliberately: padding outside it
// would align the column while leaving the colored blocks themselves
// different widths, which is the exact raggedness this exists to remove.
//
// width is a floor, never a ceiling: a name already wider than width is
// returned at its own width rather than truncated, since a clipped
// status ("In Progr") reads as data loss, and the caller computing the
// width (issue list's own column builder) derives it from the widest
// name it is about to render anyway, so the truncating case does not
// arise there. width 0 — Status's own call above — pads nothing, which
// is what a single-status surface like issue view's glance line wants:
// there is no set to align against.
//
// Padding is applied ONLY when the pill will actually be styled. With
// styling off there is no background for the spaces to color, so they
// would be plain trailing whitespace on a human line — noise in a pipe,
// and exactly the kind of artifact the output audit's own captures
// (NO_COLOR, fixed width) assert against.
func StatusPadded(name string, category StatusCategory, width int) string {
	th := ResolvedTheme()
	dark := BackgroundIsDark(th)
	bg := statusCategoryColor(dark, category)
	if bg == nil {
		bg = statusNameColor(dark, name)
	}
	// sanitize here, once, regardless of which color (or none) resolved:
	// name is Jira-authored and every returned shape — pilled or the
	// fully plain bg==nil case inside statusPill — must render the
	// sanitized form. category is never itself rendered (it only ever
	// selects a color through an exact map lookup above), so it needs no
	// sanitizing of its own — a hostile category value simply matches no
	// key and falls through to the name fallback, the same as any other
	// unrecognized string.
	clean := sanitize(name)
	if bg != nil && StyleEnabled() {
		if pad := width - ansi.StringWidth(clean); pad > 0 {
			clean += strings.Repeat(" ", pad)
		}
	}
	return statusPill(bg, clean)
}

// statusNameColor is Status's ORIGINAL category-free map, kept as its
// own function rather than folded back into Status: the fallback for a
// category-less (or unrecognized-category) caller stays exactly the
// three buckets the old Status always used — "to do", "in progress",
// "done", anything else uncolored (nil) — routed through the SAME
// jiraStatusColors pairs statusCategoryColor uses, so a name-only caller
// and a category-aware caller agree on what "to do" looks like.
func statusNameColor(dark bool, name string) color.Color {
	switch canonicalize(name) {
	case "to do":
		return statusCategoryColor(dark, statusCategoryNew)
	case "in progress":
		return statusCategoryColor(dark, statusCategoryIndeterminate)
	case "done":
		return statusCategoryColor(dark, statusCategoryDone)
	default:
		return nil
	}
}

// pillCapLeft and pillCapRight are the Powerline Extra Symbols half-
// circles GlyphSetNerd caps a pill with instead of padding spaces — left
// half circle (thick) and right half circle (thick). Both codepoints sit
// in the Unicode Private Use Area with no standard name of their own;
// they are the same in Nerd Font v2 and v3 (the v2/v3 remapping touches
// icon ranges, not the older Powerline Extra Symbols block), so one
// value covers both — there is no "v2 nerd" vs "v3 nerd" to add later.
const (
	pillCapLeft  = "" // U+E0B6, left half circle (thick)
	pillCapRight = "" // U+E0B4, right half circle (thick)
)

// statusPill renders name as Status's own filled badge: bg as the
// BACKGROUND and a foreground chosen for contrast against that exact
// background (contrastForeground below).
//
// bg == nil or styling disabled falls back to name completely unstyled —
// no padding, no caps, no background, zero escape bytes (H4/NO_COLOR):
// bg == nil means neither the category nor the name matched anything
// Status knows a color for, and a pill nobody can see is worse than no
// pill. Every OTHER case is guaranteed a real color now — bg comes from
// jiraStatusColors, a map of literal hex values, never a theme slot that
// might be nil or hueless the way Dim was — so the reverse-video
// fallback the old statusPill needed for that gap has no case left that
// reaches it; SGR 7 is not used anywhere in this function anymore.
//
// GlyphSetDefault renders ONE lipgloss Style built and Rendered in a
// single call — Padding(0, 1) for the space either side — so the whole
// pill is one self-closing styled span (H5: never a foreground style
// nested inside a background one). GlyphSetNerd instead delegates to
// pillWithCaps, which needs a SECOND foreground (the cap glyphs render
// in bg's own color) that one Style cannot carry alongside the body's
// contrasting foreground — see pillWithCaps' own doc comment for why
// that is still H5-safe despite being three Render calls, not one.
func statusPill(bg color.Color, name string) string {
	if bg == nil || !StyleEnabled() {
		return name
	}
	fg := contrastForeground(bg)
	if ResolvedGlyphs() == config.GlyphSetNerd {
		return pillWithCaps(bg, fg, name)
	}
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Padding(0, 1).Render(name)
}

// pillWithCaps renders name capped with pillCapLeft/pillCapRight instead
// of padding spaces — GlyphSetNerd's own shape, reached only once
// statusPill has already confirmed styling is on and bg is a real color.
// The caps REPLACE the padding spaces; nothing here emits both.
//
// Three independent Render calls, deliberately, not one Style spanning
// the whole pill: lipgloss applies exactly one foreground per Render
// call, and the caps need a DIFFERENT foreground (bg's own hue, so each
// half-circle glyph reads as a rounded continuation of the pill against
// whatever the terminal's own background already is) from the body
// (fg against bg). Each of the three calls is independently
// self-closing — its own SGR set, then its own SGR reset — so
// concatenating them leaves nothing open across the join: by the time
// the next call's SET appears on the wire, the previous call's RESET has
// already run, and a caller (a table cell, a Divider title) that later
// wraps the WHOLE concatenated result in its own outer style is in
// exactly the same position it would be wrapping any other single-Render
// pill — no NEW stranded-reset risk this function introduces on its own.
// TestStatusPillCapsHaveNoStrandedReset proves this at the byte level
// (H5).
func pillWithCaps(bg, fg color.Color, name string) string {
	capStyle := lipgloss.NewStyle().Foreground(bg)
	body := lipgloss.NewStyle().Background(bg).Foreground(fg)
	return capStyle.Render(pillCapLeft) + body.Render(name) + capStyle.Render(pillCapRight)
}

// contrastForeground picks black or white text for a pill rendered
// against bg, using the standard broadcast-luma ("YIQ") approximation —
// (R*299 + G*587 + B*114)/1000, black text above the 128 midpoint,
// white at or below it — the same formula W3C's own accessibility
// guidance uses for this exact problem. No gechr helper covers this:
// x/palette's Semantic set (SemanticDark/SemanticLight) measures a
// FOREGROUND color against the TERMINAL's own background, the opposite
// problem from this one, where the background is one of the THEME's
// own semantic colors, chosen independently of whatever the terminal
// itself is set to.
func contrastForeground(bg color.Color) color.Color {
	r, g, b, _ := bg.RGBA() // premultiplied 16-bit channels, 0-65535
	yiq := ((r>>8)*299 + (g>>8)*587 + (b>>8)*114) / 1000
	if yiq >= 128 {
		return color.Black
	}
	return color.White
}

// priorityRankKey identifies one rung of Jira's priority ramp — the
// canonicalized (lowercased, trimmed) form of a Jira priority name, which
// is what canonicalize produces and what jiraPriorityRanks is keyed by.
//
// A defined type rather than a bare string so a rank key cannot be
// confused with the many other strings passing through this file (a
// status category, an entity name, a raw wire value): the five rungs
// below are the only valid values, and a lookup elsewhere has to say
// priorityRankKey(...) out loud to perform the conversion.
type priorityRankKey string

const (
	priorityHighest priorityRankKey = "highest"
	priorityHigh    priorityRankKey = "high"
	priorityMedium  priorityRankKey = "medium"
	priorityLow     priorityRankKey = "low"
	priorityLowest  priorityRankKey = "lowest"
)

// priorityRank is everything one rung of Jira's priority ramp renders
// with: its color pair, and its glyph in each vocabulary. One entry per
// rank rather than a color map beside an icon map, because the two are
// the SAME vocabulary — five ranks, one key set — and split across two
// tables nothing makes them agree. A rank added to only one of them
// would degrade silently (PriorityIcon falls back to the bare word),
// which is the quiet half-broken state a single table cannot reach.
type priorityRank struct {
	colorPair
	iconDefault, iconNerd string
}

// jiraPriorityRanks is Jira's own priority spectrum, in the same
// dark/light pairs and for the same reason jiraStatusColors above is
// hard-coded rather than borrowed from a theme slot — and priority is
// where borrowing was actually caught looking wrong. Priority used to
// read th.Orange for "High"; in a real resolved theme that slot renders
// #F5E0DC, a near-white cream, so "High" arrived on screen as
// undifferentiated plain text. A theme's Orange is whatever that theme's
// designer meant by orange, which is not a promise about severity —
// while Jira's priority ramp IS a severity signal, and has to keep its
// ordering (red hot, green cool) whatever palette surrounds it.
//
// Approximate on purpose, exactly as jiraStatusColors is: the bar is
// "recognizably Jira's own red-to-green priority ramp", not pixel parity
// with tokens Atlassian reshuffles between releases.
var jiraPriorityRanks = map[priorityRankKey]priorityRank{
	priorityHighest: {colorPair{dark: lipgloss.Color("#E2483D"), light: lipgloss.Color("#C9372C")}, "↑↑", ""},
	priorityHigh:    {colorPair{dark: lipgloss.Color("#F87462"), light: lipgloss.Color("#E34935")}, "↑", ""},
	priorityMedium:  {colorPair{dark: lipgloss.Color("#E56910"), light: lipgloss.Color("#B65C02")}, "-", ""},
	priorityLow:     {colorPair{dark: lipgloss.Color("#4BCE97"), light: lipgloss.Color("#22A06B")}, "↓", ""},
	priorityLowest:  {colorPair{dark: lipgloss.Color("#7EE2B8"), light: lipgloss.Color("#2ABB7F")}, "↓↓", ""},
}

// labelGlyph marks a label as a label — the tag icon Jira draws beside
// one in the browser — so a run of them reads as tags rather than as a
// comma-separated sentence.
//
// Font Awesome's tag, for the same reason the priority chevrons are:
// Nerd Font v3 remapped much of the Material Design range, and nothing
// can ask a terminal which version its font is, so the glyph comes from
// a range whose codepoints did not move.
//
// Empty under GlyphSetDefault, deliberately: a terminal without a
// patched font would draw tofu, and a label needs no marker to read as
// a label once it carries its own color.
const labelGlyph = "\uf02b"

// Label renders one of an issue's labels: its own entity color, led by
// a tag glyph when the terminal's font can draw one.
//
// The glyph takes the label's OWN color rather than a neutral one, so a
// label and its marker read as one object — and, as with every sink
// here, the whole thing is one styled span, never a colored glyph
// concatenated onto a separately-colored word.
func Label(name string) string {
	if ResolvedGlyphs() != config.GlyphSetNerd {
		return Entity(name)
	}
	return EntityAs(name, labelGlyph+" "+sanitize(name))
}

// PriorityIcon renders a priority as its Jira-shaped glyph rather than
// its word — what a LIST wants, where a column of "Highest"/"Medium"
// spends horizontal room on text a reader already recognizes by shape,
// and where every extra column competes with the summary for width.
//
// Colored from the same ramp Priority uses, so icon and word agree
// wherever both appear. An unrecognized rank keeps its NAME rather than
// borrowing a glyph that would assert a severity this CLI cannot know —
// the same refusal Priority makes by leaving it uncolored.
func PriorityIcon(name string) string {
	name = sanitize(name)
	rank, ok := jiraPriorityRanks[priorityRankKey(canonicalize(name))]
	if !ok {
		return Priority(name)
	}
	glyph := rank.iconDefault
	if ResolvedGlyphs() == config.GlyphSetNerd {
		glyph = rank.iconNerd
	}
	if !StyleEnabled() {
		return glyph
	}
	// rank.color, not priorityColor(name): the rank is already in hand,
	// and going back through the name would canonicalize it a second time
	// — a fresh lowercase allocation per row, in a column that renders
	// once per issue.
	return lipgloss.NewStyle().Foreground(rank.color()).Render(glyph)
}

// PriorityWithIcon renders a priority as its glyph followed by its word
// — what a DETAIL view wants, where there is room for both and the pair
// reinforce each other: the glyph is the shape a reader recognizes from
// the browser, the word is what they would say out loud. A list uses
// PriorityIcon alone instead, because there the column has to be narrow.
//
// Glyph first, matching Jira's own ordering in the browser and the
// icon-only column in `issue list`, so the eye lands on the same mark in
// either surface.
//
// A rank the ramp does not cover has no glyph to lead with (see
// PriorityIcon's own refusal to invent one), so it renders as the bare
// word. An absent priority renders empty and the row is dropped by the
// caller's own field block, rather than showing a glyph with nothing
// beside it.
func PriorityWithIcon(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	if priorityColor(name) == nil {
		return Priority(name)
	}
	return PriorityIcon(name) + " " + Priority(name)
}

// priorityColor resolves a rank to its ramp color for the resolved
// background, or nil for a rank the ramp does not cover. Shared by
// Priority and PriorityIcon so the word and the glyph can never drift
// onto different colors.
func priorityColor(name string) color.Color {
	rank, ok := jiraPriorityRanks[priorityRankKey(canonicalize(name))]
	if !ok {
		return nil
	}
	return rank.color()
}

// color resolves this rank to the half of its pair the resolved
// background calls for. A method so a caller already holding the rank
// never has to go back through its name to get the color.
func (r priorityRank) color() color.Color {
	return r.resolve(BackgroundIsDark(ResolvedTheme()))
}

// Priority renders a Jira priority name in its rung of the severity
// ramp — the word alone, for a surface with room for it. A list wants
// PriorityIcon instead, and a detail view PriorityWithIcon; all three
// resolve their color through priorityColor, so the word and the glyph
// cannot drift apart.
//
// A tenant that renames or reorders its priority scheme falls through
// to plain text, for the same reason Status does: a wrong urgency color
// reads worse than none.
func Priority(name string) string {
	// See Status's sanitize call: same requirement, same reason — name
	// is Jira-authored and every branch, including the unstyled default,
	// must render the sanitized form.
	name = sanitize(name)
	c := priorityColor(name)
	if c == nil {
		// A tenant's own custom priority. Left plain rather than hashed
		// through Entity: priority is an ORDERED severity, and an
		// arbitrary hash color would imply a rank this CLI cannot know.
		return name
	}
	if !StyleEnabled() {
		return name
	}
	return lipgloss.NewStyle().Foreground(c).Render(name)
}

// issueTypeBug, issueTypeStory and issueTypeTask are the canonicalized
// issue-type names Type colors deliberately, and the hierarchy*
// constants below are Jira's own IssueTypeDetails.hierarchyLevel values
// (wire_gen.go).
//
// Both are OPEN sets, and naming them is not a claim otherwise — it is
// the opposite. An admin creates issue types freely, and the wire's
// Scope field means one can belong to a single project; Advanced
// Roadmaps adds hierarchy levels above epic. Even the built-ins vary by
// template: a service-desk project has Incident and Change and no Story
// at all.
//
// So these name the STANDARD set, and everything else keeps working —
// an unrecognized name falls through to Entity's hash (a stable
// per-name color, just not a meaningful one) and an unrecognized level
// falls through to the name switch. What they buy is that `case 1:` no
// longer reads as a magic number when it means "epic", and a reader can
// tell which literals are Jira's vocabulary rather than this file's
// invention.
//
// Plain string constants rather than a defined type like
// StatusCategory: these values never leave Type below — not a map key,
// not a field, not a parameter — so there is nowhere for one to be
// confused with another string, and a type would buy only a conversion
// at the switch.
const (
	issueTypeBug   = "bug"
	issueTypeStory = "story"
	issueTypeTask  = "task"
)

const (
	// hierarchySubtask and hierarchyEpic are the two levels that carry a
	// color of their own. Level 0 — the base level every standard type
	// sits at — deliberately has no constant: nothing switches on it,
	// because it is exactly the case the NAME switch below exists to
	// separate.
	hierarchySubtask int64 = -1
	hierarchyEpic    int64 = 1
)

// Type renders a Jira issue type name in a color chosen from the most
// reliable signal available, checked in order:
//
//  1. hierarchyLevel — IssueTypeDetails' own structural classification
//     (-1 subtask, 0 standard, 1 epic; verified live against a real
//     tenant), language-independent unlike the type's NAME, which a
//     tenant can rename freely (an Epic renamed "Initiative" still
//     carries hierarchyLevel 1). Checked FIRST, so it wins over the
//     name-based rule below whenever it applies — Epic and Subtask never
//     fall through to a name match at all, however a tenant has renamed
//     them.
//  2. the well-known standard names Jira Cloud ships by default at
//     hierarchyLevel 0 (Bug, Story, Task) — matched by name because
//     hierarchyLevel 0 alone cannot tell a Bug from a Story from a
//     tenant's own custom standard-level type.
//  3. anything else — a tenant-custom type this CLI has no brand color
//     for — falls back to Entity's hash-based per-name color, so it
//     still reads consistently wherever it recurs, just not
//     brand-matched to Jira's own palette.
//
// Foreground only, never a pill: unlike status, issue type carries no
// color of its own on the wire at all (verified against the vendored
// spec and live: IssueTypeDetails has name/hierarchyLevel/subtask/
// iconUrl/avatarId, nothing else), so there is no real signal to badge
// the way Status's own pill badges a genuine Jira category. A second
// filled badge sitting next to Status's own would also just be visual
// noise, not a second piece of information.
func Type(name string, hierarchyLevel int64) string {
	clean := sanitize(name)
	th := ResolvedTheme()
	// Hierarchy first: it is Jira's own structural answer and survives
	// renaming, so a team that calls its epics "Initiatives" still gets
	// the epic color. Only then the name, for the standard types that
	// share hierarchy level 0 and can be told apart no other way.
	switch hierarchyLevel {
	case hierarchyEpic:
		return styleOrPlain(th.Magenta, clean)
	case hierarchySubtask:
		return styleOrPlain(th.Dim, clean)
	}
	switch canonicalize(clean) {
	case issueTypeBug:
		return styleOrPlain(th.Red, clean)
	case issueTypeStory:
		return styleOrPlain(th.Green, clean)
	case issueTypeTask:
		return styleOrPlain(th.Blue, clean)
	default:
		return Entity(clean)
	}
}

// numberStyle resolves clog's own style for an int/float field value
// (style.Config.FieldNumber) straight from clog's package-level
// defaults — style.Default(), the exact Config clog.DefaultStyles()
// itself returns — rather than hard-coding a color or reserving a
// second entry in x/palette alongside Entity's own allocation.
//
// This app never calls clog.SetStyles (theme.go's applyTheme only ever
// calls clog.SetTheme, which — per its own doc comment — touches
// highlighting and gradients, never Config.FieldNumber), so
// style.Default() IS the Config clog is already rendering every other
// int/float field with. Reading it fresh here, rather than copying
// today's magenta into a literal, is what keeps Number's color tied to
// clog's own choice instead of a snapshot of it.
//
// Unlike backtickStyle, which switches BacktickFor on the resolved
// theme's background (clog ships separate dark/light inline-code
// stops), FieldNumber carries no background variant in clog's
// Default() — a single fixed magenta (ANSI color 5) either way — so
// there is no bg parameter to resolve here.
func numberStyle() *lipgloss.Style {
	return style.Default().FieldNumber
}

// Number renders a count this CLI itself computed — a "Showing N"
// line, a pagination total, an estimate — with clog's own numeric-field
// style (numberStyle above), so a number reads as a number at a glance
// instead of blending into the surrounding prose, and matches the exact
// color clog itself already uses whenever IT prints an int/float field.
//
// Formatting is human.FormatNumber(n, ","), the same thousands-grouping
// issue list's own count-estimate hint already used before this sink
// existed (nextPageHint, internal/cli/issue/list.go) — reused here
// rather than adding a second formatter, so "how a number looks" is
// decided in exactly one place; nextPageHint's own call now goes
// through this sink instead of formatting separately.
//
// n is an int: every call site (Pagination.Returned/Total, a --limit/
// --comments count, len(...)) already carries one, and FormatNumber's
// own int64 parameter is satisfied by a single explicit conversion here
// rather than asking every caller to widen its own count.
//
// Gated on StyleEnabled() like every other sink in this file (via
// styleOrPlain): styling off returns the plain formatted digits, with
// ZERO escape bytes (H4) — no different from Status/Priority/Entity
// falling back to plain text.
//
// Negative input renders as "-N", grouped the same way FormatNumber
// groups a positive one (its own documented behavior) — no call site
// today can produce a negative count, but silently clamping to 0 would
// be a worse surprise than a well-formed negative number.
func Number(n int) string {
	text := human.FormatNumber(int64(n), ",")
	// Resolve the style only when it will be used. styleOrPlain checks
	// StyleEnabled itself, but Go evaluates numberStyle() before the call,
	// and style.Default() rebuilds clog's ENTIRE default config — every
	// level, value map, gradient and per-format sub-config — to read one
	// field. Gating here keeps the unstyled path (NO_COLOR, a pipe, the
	// audit's own captures) from paying for a config it discards, the
	// same way Hint gates backtickStyle below.
	if !StyleEnabled() {
		return text
	}
	return styleOrPlain(numberStyle(), text)
}

// Bytes renders a byte count as a human-readable size — "1.4 MiB" — in
// the same numeric style Number uses, because a file size IS a number
// and a reader scanning a column of them should not have to work out
// why these digits are colored differently from those.
//
// IEC units (KiB/MiB), not SI (kB/MB). Jira reports an attachment's
// `size` as a raw byte count with no unit convention of its own, so the
// choice is ours, and the binary units are the ones that cannot be
// misread: "1 MB" is 1,000,000 bytes to a disk vendor and 1,048,576 to
// almost every file manager a person has ever used, while "1 MiB" is
// only ever the latter. The envelope publishes the integer regardless
// (envelope.AttachmentView.Size), so nothing a machine reads depends on
// this.
//
// Negative input is passed to the formatter as given rather than
// clamped, matching Number's own reasoning: no caller can produce one
// today, and a surprising number beats a silently invented zero.
//
// Gated on StyleEnabled before resolving the style, exactly as Number
// is and for the same cost reason — see its doc comment.
func Bytes(n int64) string {
	text := human.FormatIECBytes(float64(n))
	if !StyleEnabled() {
		return text
	}
	return styleOrPlain(numberStyle(), text)
}

// canonicalize folds a Jira name to the form the lookup tables above key
// on: trimmed and case-folded, so "In Progress", "in progress" and
// " In Progress " all match the same bucket.
func canonicalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// styleOrPlain renders text through style when styling is both possible
// and wanted, and returns text unchanged otherwise:
//
//   - possible — the resolved theme actually configured that slot.
//     Theme.Init documents nil as "not configured", and Monochrome
//     leaves every semantic color nil by design; lipgloss.Style.Render
//     has a VALUE receiver, so calling it through a nil *lipgloss.Style
//     would panic rather than fall back.
//   - wanted — StyleEnabled: NO_COLOR, --color=never, and a non-TTY
//     writer must all still produce byte-for-byte plain text, and a
//     pure sink has no writer of its own to check, so it asks the
//     resolved, once-per-invocation answer instead.
func styleOrPlain(style *lipgloss.Style, text string) string {
	if style == nil || !StyleEnabled() {
		return text
	}
	return style.Render(text)
}

// jiraTimeLayout is Jira Cloud's timestamp format: RFC 3339-like, but
// with a COLONLESS numeric offset ("-0400", not "-04:00") and three
// fractional digits. time.RFC3339 rejects the colonless offset, so
// parsing a real Jira timestamp against RFC3339 alone fails — which is
// exactly what shipped once: comment and date values rendered as raw ISO
// instead of "3 hours ago", because Jira is the only source of these and
// it never uses RFC3339's colon-offset form. x/human formats a time.Time
// but does not parse a wire shape, so the layout is ours to own.
const jiraTimeLayout = "2006-01-02T15:04:05.999-0700"

// Time renders a Jira timestamp as a humanized relative string —
// "3 hours ago" — against the current instant.
func Time(iso string) string {
	return TimeFrom(iso, time.Now())
}

// TimeFrom is Time with an injected reference instant, mirroring
// x/human's own FormatTimeAgo/FormatTimeAgoFrom pairing (x/primitives.md
// "Use their From(t, now) variants in tests..."): a test gets a
// deterministic answer, and several times rendered in one pass can share
// one instant so a list humanizes consistently even if drawing the list
// itself takes a moment.
//
// Parsing itself is ParseTime's job (shared, not duplicated here — see
// its own doc comment). A value that fails to parse renders AS GIVEN
// rather than disappearing silently or panicking — the caller has
// already sanitized it (cli.Text before cli.Time), so an unparsable value
// is safe to print rather than swallow.
func TimeFrom(iso string, now time.Time) string {
	t, ok := ParseTime(iso)
	if !ok {
		return iso
	}
	return human.FormatTimeAgoFrom(t, now)
}

// ParseTime parses a Jira timestamp into a time.Time: Jira's own
// colonless-offset layout (jiraTimeLayout) first, then strict RFC 3339 as
// a fallback for any already-normalized value. ok is false when iso
// matched neither — TimeFrom's own "render as given" fallback for that
// case is a rendering decision, so it stays in TimeFrom rather than here.
//
// This is the one place either layout is tried, so a caller that needs
// the parsed time.Time itself — rather than TimeFrom's humanized string —
// shares this exact parsing instead of re-declaring jiraTimeLayout a
// second time (issue view's own comment-ordering sort is exactly that
// caller: see sortCommentsDescending in internal/cli/issue/view.go).
func ParseTime(iso string) (time.Time, bool) {
	if t, err := time.Parse(jiraTimeLayout, iso); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// MarkupStrong renders s with the "strong" ADF mark's emphasis — bold
// weight, the same visual prominence Status/Priority reserve for
// semantic state but used here for plain authored emphasis, never for
// state. Gated on StyleEnabled() like every other sink in this file, so
// NO_COLOR/--color=never/non-TTY output falls back to s unchanged.
func MarkupStrong(s string) string {
	return styleOrPlain(ResolvedTheme().Bold, s)
}

// markupEmphasisStyle renders the "em" ADF mark's emphasis (italic).
// Built directly rather than through the theme: italic is a text
// decoration, not a color choice a theme reserves a palette slot for
// (compare Entity's KNOWN GAP above, which is exactly that problem for
// a COLOR), so there is nothing for a theme preset to configure or
// leave nil here.
var markupEmphasisStyle = lipgloss.NewStyle().Italic(true)

// MarkupEmphasis renders s with the "em" ADF mark's emphasis.
func MarkupEmphasis(s string) string {
	if !StyleEnabled() {
		return s
	}
	return markupEmphasisStyle.Render(s)
}

// MarkupCode renders s with the "code" ADF mark's (or a codeBlock line's)
// emphasis, using the theme's own Markdown code style so a description's
// inline code and any future terminal-Markdown surface share one palette
// choice rather than inventing a second.
func MarkupCode(s string) string {
	return styleOrPlain(ResolvedTheme().MarkdownCode, s)
}

// backtickStyle resolves clog's own inline-code style (style.BacktickFor)
// for the CURRENTLY resolved theme's background — the same visual
// marker clib's help renderer already reserves for a backtick-quoted
// command mention in a flag description (theme.Theme's own
// HelpDescBacktick), reused here rather than reinvented, so a hint
// line's flag mention and a --help description's own mention read as
// the same kind of thing.
//
// clib and clog theme independently — applyThemeName (theme.go) hands
// the SAME name to both libraries, but each resolves its own Theme with
// its own Background enum, and the two are not interchangeable even
// though both only ever hold light or dark. This is the one place that
// bridges cli's own ResolvedTheme() (clib) onto the clog Background
// style.BacktickFor expects, rather than passing one through where the
// other is wanted.
func backtickStyle() *lipgloss.Style {
	bg := clogtheme.BackgroundLight
	if BackgroundIsDark(ResolvedTheme()) {
		bg = clogtheme.BackgroundDark
	}
	return style.BacktickFor(bg)
}

// Hint styles a CLI-authored hint line: any `backtick`-delimited flag
// or command mention in s renders with clog's own inline-code style
// (backtickStyle above), the surrounding prose plain — so a hint reads
// as guidance pointing at a specific flag, rather than as an
// undifferentiated sentence. The same distinction Status/Priority/
// Entity draw for a VALUE, applied here to a static sentence built
// around one.
//
// Gated on StyleEnabled() like every other sink in this file: with
// styling off, code is nil, and style.BacktickMode.Render's own
// documented non-color fallback returns s completely unchanged —
// backticks left in, literally, as plain ASCII — never converted to a
// raw ANSI byte. That is exactly what NO_COLOR/--color=never/non-TTY
// output needs (H4: zero escape bytes), and it is why Hint never has to
// sanitize s itself: there is nothing here for sanitize to strip.
//
// s must be built from CLI-authored text only. A value sink (cli.Text,
// cli.Entity, ...) still owns sanitizing anything Jira-sourced BEFORE
// it is interpolated into s — Hint only styles the backtick spans a
// caller's own static prose already carries, exactly once, and never
// nests a second Render call around its own result (H5).
func Hint(s string) string {
	var code *lipgloss.Style
	if StyleEnabled() {
		code = backtickStyle()
	}
	return style.BacktickStrip.Render(s, nil, code)
}

// Text sanitizes Jira-sourced text for a terminal — the H4 guard's
// per-sink policy (design doc: "Jira text IS sanitized"). See sanitize
// for what it strips.
//
// This is unconditional, unlike Status/Priority/Entity's OWN styling:
// sanitization is a safety property, not a styling choice, so it does
// not gate on StyleEnabled — a plain terminal needs the control
// characters removed exactly as much as a color one does. Status,
// Priority and Entity call sanitize directly (not through Text) for the
// same unconditional reason, since Text's own job is single-line rows,
// not styled names.
//
// Every embedded control character is dropped, newlines and tabs
// included: Text feeds single-line rows (View.Row, View.Line), where an
// embedded newline would break the row's layout, not just its safety.
func Text(s string) string {
	return sanitize(s)
}

// hyperlinkWriter is the shared x/ansi writer IssueKey emits OSC 8
// hyperlinks through. WithTerminal(true) is fixed rather than resolved
// per call: IssueKey only ever reaches Hyperlink after StyleEnabled()
// has already answered "yes, a real terminal, style it" (see IssueKey
// below), so there is no second TTY question left for this value to
// settle, and — just as important — the plain HyperlinkFallback path
// (HyperlinkFallbackExpanded's "text (url)") must never be reachable
// from here: that fallback is for a WithTerminal(false) writer, and
// piped/non-TTY output must see the bare key IssueKey already returns
// for that case, never "key (url)". A shared package-level value rather
// than one built per call: x/ansi.New does no I/O and holds only this
// fixed config, so there is nothing per-invocation for a fresh one to
// capture.
var hyperlinkWriter = ansi.New(ansi.WithTerminal(true))

// issueKeyPattern matches the shape Jira actually issues: a project key
// (one uppercase letter, then any run of uppercase letters/digits), a
// dash, then the issue number. This is deliberately narrower than "any
// sanitized string" — see IssueBrowseURL's own doc comment for why a key
// that survives sanitize() but does not match this shape still gets no
// link.
var issueKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// IssueKey renders a Jira issue key as a clickable OSC 8 terminal
// hyperlink to its browse URL (site + "/browse/" + key) when styling is
// genuinely on, and as the plain sanitized key in every other case — the
// same "link is a TTY affordance, exactly like color" rule Status/
// Priority/Entity already follow via StyleEnabled, reused here rather
// than a second notion of "is this a terminal". Callers that need the
// key rendered inside a SENTENCE — a "jira issue comment list KEY" hint,
// a "Showing N of TOTAL" banner — must use Text(key), never this: linking
// a key mentioned in prose is a deliberate, separate follow-up (out of
// scope here), and interpolating a hyperlink's own escape bytes into a
// hint line would make that line unsafe to copy-paste as a shell command.
//
// key is sanitized FIRST (H4), unconditionally — this is Jira-sourced
// text like any other sink here — and every returned value, styled or
// not, is built from that sanitized copy. site is trusted config/auth
// state, not Jira-sourced, so it is validated rather than sanitized (see
// IssueBrowseURL): a missing or unparsable site degrades to the plain
// key, never a partial or malformed URL — not every invocation is
// authenticated, and IssueKey must still be safe to call when one is
// not.
//
// The emitted hyperlink carries no color of its own — every current
// call site (issue view's glance line, issue list's KEY column) renders
// the key unstyled otherwise, so there is nothing for the OSC 8 span to
// nest against. A future caller that DOES want the key colored must
// compose deliberately (color the whole IssueKey() result the way
// EntityAs colors a whole assembled string, never re-open a style
// INSIDE the hyperlink's own open/close pair) and prove the byte shape
// holds, per H5 — see sinks_test.go's own byte-level proof of the shape
// this function emits today.
func IssueKey(site, key string) string {
	key = sanitize(key)
	if !StyleEnabled() {
		return key
	}
	link, ok := IssueBrowseURL(site, key)
	if !ok {
		return key
	}
	return hyperlinkWriter.Hyperlink(link, key)
}

// IssueBrowseURL builds the browse URL for key at site, or reports ok
// false when either is unusable — the single gate IssueKey trusts to
// decide "link or plain". Exported so a caller building a URL for its
// OWN purposes — `issue view`/`issue comment list`'s own --web target —
// reuses this exact construction rather than a second one: there is
// exactly one recipe for "an issue's browse URL" in this CLI.
//
// key must match issueKeyPattern: sanitize() already strips the escape/
// control-byte classes H4 exists to catch, but it does NOT reject a
// space or other printable character sanitize has no reason to touch —
// "a key carrying escapes or spaces must not be interpolated into a
// URL" needs a stronger gate than sanitization alone, so this checks the
// SHAPE a real Jira key always has before key ever reaches a URL. A
// value that fails this — hostile or merely malformed — gets no link,
// not an escaped-but-broken one: a 404 from a key nobody could have
// typed is a more honest failure than a URL built from a guess.
//
// site is parsed rather than trusted verbatim: a caller reaching this
// without an already-validated site (config.ValidateBaseURL runs at
// load time; adapters/jira.New checks again at construction) must still
// degrade safely rather than build a URL from a scheme-less or
// userinfo-carrying string. This is belt and braces, not the primary
// guard — BaseURL() on a real client is already clean by the time it
// reaches here.
func IssueBrowseURL(site, key string) (string, bool) {
	if site == "" || !issueKeyPattern.MatchString(key) {
		return "", false
	}
	base, err := url.Parse(site)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil {
		return "", false
	}
	return base.JoinPath("browse", key).String(), true
}

// sanitize strips ANSI escape sequences (x/ansi.Strip handles CSI/OSC/
// etc.) and then drops any remaining C0/DEL/C1 control byte an escape
// sequence did not already consume — a lone ESC, BEL, or CR a hostile or
// malformed Jira field could use to move the cursor or overwrite a line,
// which stripping recognized ANSI sequences alone would not catch.
//
// The shared implementation behind Text and every sink that renders a
// Jira-authored name (Status, Priority, Entity) — any string that
// reaches the terminal from Jira must go through this exactly once,
// before whatever styling that sink applies on top.
func sanitize(s string) string {
	stripped := ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		default:
			return r
		}
	}, stripped)
}
