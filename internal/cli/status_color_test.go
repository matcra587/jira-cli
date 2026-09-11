package cli

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/pill"

	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
	clibtheme "github.com/gechr/clib/theme"
)

// statusPill renders a status as a badge colored by its workflow category,
// mapping each of the three category keys to a distinct fixed truecolor fill
// and falling back to a per-name hash fill for an unknown/absent category.
// The badge always sets a background fill, so its rendered output differs
// from the bare text and from the other categories.
func TestStatusPillColorsByCategory(t *testing.T) {
	theme := clibtheme.Dark()
	cfg := plainConfig{tty: true, theme: theme}
	const sample = "Some Status"

	// Each category is pinned to its fixed fill (as a 24-bit SGR fragment),
	// so an accidental palette swap fails loudly, not just distinctness.
	fills := map[string]string{
		"done":          "48;2;31;132;90",  // #1f845a
		"indeterminate": "48;2;226;178;3",  // #e2b203
		"new":           "48;2;29;122;252", // #1d7afc
	}
	rendered := map[string]string{}
	for _, category := range []string{"done", "indeterminate", "new"} {
		out := statusPill(cfg, sample, category, "").Render(sample)
		if out == sample || !strings.Contains(out, "\x1b[") {
			t.Errorf("category %q produced no styling: %q", category, out)
		}
		if !strings.Contains(out, fills[category]) {
			t.Errorf("category %q missing its pinned fill %q: %q", category, fills[category], out)
		}
		rendered[category] = out
	}
	if rendered["done"] == rendered["indeterminate"] || rendered["done"] == rendered["new"] ||
		rendered["indeterminate"] == rendered["new"] {
		t.Errorf("category colors collapsed together: %+v", rendered)
	}

	// Unknown category falls back to a per-name hash color: still a pill,
	// distinct from the category colors and stable for a given status name.
	fallback := statusPill(cfg, "Custom", "", "").Render("Custom")
	if fallback == "Custom" || !strings.Contains(fallback, "\x1b[") {
		t.Errorf("unknown category should still render a pill, got %q", fallback)
	}
	if again := statusPill(cfg, "Custom", "weird-key", "").Render("Custom"); again != fallback {
		t.Errorf("hash fallback not stable for the same name: %q vs %q", again, fallback)
	}

	// Non-TTY output carries no styling.
	if got := statusPill(plainConfig{tty: false, theme: theme}, "Done", "done", "green").Render("Done"); got != "Done" {
		t.Errorf("non-TTY pill should be unstyled, got %q", got)
	}

	// A TTY with no theme produces no styling: a nil theme means unstyled
	// output was requested, even though the fill itself no longer reads it.
	if got := statusPill(plainConfig{tty: true, theme: nil}, "Done", "done", "green").Render("Done"); got != "Done" {
		t.Errorf("nil-theme pill should be unstyled, got %q", got)
	}
}

// TestStatusPillPrefersAPIColorName checks that Jira's own colorName
// designation wins over the category key, so the badge matches the Jira UI,
// and that medium-gray resolves to the fixed grey fill rather than a
// category color.
func TestStatusPillPrefersAPIColorName(t *testing.T) {
	theme := clibtheme.Dark()
	cfg := plainConfig{tty: true, theme: theme}
	const s = "Whatever"

	// colorName overrides a mismatched category: "green" on a "new" status
	// renders the same as the done-category pill, not the new (blue) one.
	greenByColor := statusPill(cfg, s, "new", "green").Render(s)
	greenByCategory := statusPill(cfg, s, "done", "").Render(s)
	if greenByColor != greenByCategory {
		t.Errorf("colorName should override category: %q vs %q", greenByColor, greenByCategory)
	}
	if blue := statusPill(cfg, s, "new", "").Render(s); greenByColor == blue {
		t.Errorf("colorName green should not match the new-category pill")
	}

	// medium-gray maps to the fixed grey fill: a real pill, distinct from the
	// three category colors.
	grey := statusPill(cfg, s, "", "medium-gray").Render(s)
	if grey == s || !strings.Contains(grey, "\x1b[") {
		t.Errorf("medium-gray should render a styled pill, got %q", grey)
	}
	for _, cat := range []string{"done", "indeterminate", "new"} {
		if grey == statusPill(cfg, s, cat, "").Render(s) {
			t.Errorf("medium-gray should differ from the %q category color", cat)
		}
	}
}

// TestStatusPillCellGating checks the cell wrapper: a styled cell whose plain
// (measured) text is space-padded on a color TTY; bare text off-TTY; and an
// empty cell for an empty status.
func TestStatusPillCellGating(t *testing.T) {
	theme := clibtheme.Dark()

	styled := statusPillCell(plainConfig{tty: true, theme: theme}, "Done", "done", "green")
	if !strings.Contains(styled.Text, "\x1b[") {
		t.Errorf("TTY pill cell should be styled, got %q", styled.Text)
	}
	// The measured (plain) text carries a space either side so the column is
	// sized to the rendered fill, not the bare label.
	if styled.Plain != " Done " {
		t.Errorf("pill cell plain text = %q, want %q", styled.Plain, " Done ")
	}

	plain := statusPillCell(plainConfig{tty: false, theme: theme}, "Done", "done", "green")
	if plain.Text != "Done" || plain.Plain != "Done" {
		t.Errorf("non-TTY pill cell should be bare text, got Text=%q Plain=%q", plain.Text, plain.Plain)
	}

	empty := statusPillCell(plainConfig{tty: true, theme: theme}, "", "done", "green")
	if empty.Text != "" {
		t.Errorf("empty status should render an empty cell, got %q", empty.Text)
	}
}

// TestStatusPillsPadToWidestLabel checks that every pill in a table pads its
// label to the widest status, so the filled backgrounds form a uniform block:
// the padding lives inside the measured text (and thus inside the fill), not
// in the grid's gap.
func TestStatusPillsPadToWidestLabel(t *testing.T) {
	theme := clibtheme.Dark()
	rows := []issueTableRow{
		{Status: "In Progress"},
		{Status: "Done"},
		{Status: ""}, // no pill; must not shrink the target width
	}

	width := widestStatusLabel(rows)
	if want := len(" In Progress "); width != want {
		t.Fatalf("widestStatusLabel = %d, want %d", width, want)
	}

	cfg := plainConfig{tty: true, theme: theme, statusPillWidth: width}
	short := statusPillCell(cfg, "Done", "done", "green")
	if want := " Done" + strings.Repeat(" ", width-len(" Done")); short.Plain != want {
		t.Errorf("short pill plain text = %q, want %q", short.Plain, want)
	}
	if !strings.Contains(short.Text, short.Plain) {
		t.Errorf("padding should sit inside the styled render, got %q", short.Text)
	}

	widest := statusPillCell(cfg, "In Progress", "indeterminate", "yellow")
	if widest.Plain != " In Progress " {
		t.Errorf("widest pill plain text = %q, want %q", widest.Plain, " In Progress ")
	}

	// Zero width (single renders, or width not precomputed) leaves the label
	// unpadded.
	unpadded := statusPillCell(plainConfig{tty: true, theme: theme}, "Done", "done", "green")
	if unpadded.Plain != " Done " {
		t.Errorf("unpadded pill plain text = %q, want %q", unpadded.Plain, " Done ")
	}
}

// TestPriorityStyleFollowsJiraScale pins the priority colors to Jira's scale:
// red for high and highest (highest bold), orange for medium, blue for low and
// lowest.
func TestPriorityStyleFollowsJiraScale(t *testing.T) {
	theme := clibtheme.Dark()
	cfg := plainConfig{tty: true, theme: theme}
	const s = "x"
	cases := map[string]lipgloss.Style{
		"Highest": foregroundStyle(theme.Red).Bold(true),
		"High":    foregroundStyle(theme.Red),
		"Medium":  foregroundStyle(theme.Orange),
		"Low":     foregroundStyle(theme.Blue),
		"Lowest":  foregroundStyle(theme.Blue),
	}
	for name, want := range cases {
		if got := priorityStyle(cfg, name).Render(s); got != want.Render(s) {
			t.Errorf("priorityStyle(%q) = %q, want %q", name, got, want.Render(s))
		}
	}
	// Non-TTY output carries no styling.
	if got := priorityStyle(plainConfig{tty: false, theme: theme}, "Highest").Render(s); got != s {
		t.Errorf("non-TTY priority should be unstyled, got %q", got)
	}
}

// TestPillForegroundContrast pins the luma rule: a light fill takes dark text,
// a dark fill takes light text, so the label stays legible on any background.
func TestPillForegroundContrast(t *testing.T) {
	const (
		dark  = "#1c1c1c"
		light = "#f5f5f5"
	)
	cases := map[string]string{
		"#ffffff": dark,  // white fill
		"#000000": light, // black fill
		"#ffee00": dark,  // bright yellow
		"#00008b": light, // dark navy
	}
	for fill, want := range cases {
		gotR, gotG, gotB, _ := pill.Foreground(lipgloss.Color(fill)).RGBA()
		wantR, wantG, wantB, _ := lipgloss.Color(want).RGBA()
		if gotR != wantR || gotG != wantG || gotB != wantB {
			t.Errorf("pill.Foreground(%s) = %s", fill, want)
		}
	}
}

// TestStatusFillNeverUsesPaletteSlots pins the remap-proofing decision: every
// fill statusFill can produce — colorName-mapped, category-mapped, or the
// per-name hash fallback — is a fixed truecolor value, never a basic ANSI
// palette slot a terminal could remap out from under the contrast computation.
func TestStatusFillNeverUsesPaletteSlots(t *testing.T) {
	inputs := []struct{ status, category, colorName string }{
		{"Done", "", "green"},
		{"In Progress", "", "yellow"},
		{"To Do", "", "blue-gray"},
		{"Blocked", "", "medium-gray"},
		{"Done", "done", ""},
		{"In Progress", "indeterminate", ""},
		{"To Do", "new", ""},
		{"Weird Custom Status", "", ""},
		{"Another Custom", "unrecognized", ""},
	}
	for _, in := range inputs {
		fill := pill.Fill(in.status, in.category, in.colorName)
		if fill == nil {
			t.Errorf("pill.Fill(%q,%q,%q) = nil; every status must get a fill", in.status, in.category, in.colorName)
			continue
		}
		if _, isBasic := fill.(ansi.BasicColor); isBasic {
			t.Errorf("pill.Fill(%q,%q,%q) resolved to remappable basic ANSI slot %v", in.status, in.category, in.colorName, fill)
		}
	}

	// The rendered pill therefore carries 24-bit SGR for both fill and text —
	// deterministic on-screen colors regardless of the terminal palette.
	out := statusPill(plainConfig{tty: true, theme: clibtheme.Dark()}, "To Do", "new", "blue-gray").Render("To Do")
	if !strings.Contains(out, "48;2;") || !strings.Contains(out, "38;2;") {
		t.Errorf("pill should render truecolor SGR fill and text, got %q", out)
	}
}

// TestEntityHuesAreFixedAndMidTone pins the entity-color decision: identity
// hints hash into fixed mid-tone hues, not the theme's background-specific
// EntityColors — a dark theme's palette rendered assignee names
// near-invisible on white terminals. Mid-tone means the Rec. 601 luma sits
// in a band readable on both black and white backgrounds.
func TestEntityHuesAreFixedAndMidTone(t *testing.T) {
	for _, c := range entityHues {
		r, g, b, _ := c.RGBA()
		luma := (299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000
		if luma < 80 || luma > 180 {
			t.Errorf("entity hue %v luma %d outside the both-backgrounds band [80,180]", c, luma)
		}
	}

	// The theme no longer supplies the colors: two themes with different
	// EntityColors render the same assignee identically.
	dark := clibtheme.Dark()
	altered := clibtheme.Dark().With(clibtheme.WithEntityColors(lipgloss.Color("#010101")))
	a := hashStyle(dark, "assignee:Alice").Render("Alice")
	b := hashStyle(altered, "assignee:Alice").Render("Alice")
	if a != b {
		t.Errorf("entity color followed the theme palette: %q vs %q", a, b)
	}
	// Monochrome presets ship an empty EntityColors slice as a deliberate
	// opt-out of entity coloring — they must keep rendering bare.
	mono := clibtheme.Dark().With(clibtheme.WithEntityColors())
	if got := hashStyle(mono, "assignee:Alice").Render("Alice"); got != "Alice" {
		t.Errorf("monochrome theme should render entities bare, got %q", got)
	}

	// Stability and distinctness still hold.
	if again := hashStyle(dark, "assignee:Alice").Render("Alice"); again != a {
		t.Errorf("entity color not stable: %q vs %q", again, a)
	}
	if bob := hashStyle(dark, "assignee:Bob").Render("Bob"); bob == a {
		t.Errorf("distinct assignees collapsed to one color")
	}
}
