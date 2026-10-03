package cli

import (
	"image/color"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/gechr/clib/theme"
	"github.com/gechr/clog/style"
	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/config"
)

// withStyle sets StyleEnabled for the duration of the test and restores
// the safe "no color" default afterward, so one test's styling choice
// cannot leak into the next.
func withStyle(t *testing.T, enabled bool) {
	t.Helper()
	SetStyleEnabled(enabled)
	t.Cleanup(func() { SetStyleEnabled(false) })
}

// deterministicTheme clears every theme env var and resets what
// applyTheme recorded, so ResolvedTheme() falls back to theme.Dark() —
// the same stub theme_test.go uses, needed here because Status,
// Priority and Entity all resolve their theme through the same
// once-per-process machinery.
func deterministicTheme(t *testing.T) {
	t.Helper()
	clearEnv(t, themeEnvNames()...)
	resetTheme(t)
}

func hasANSI(s string) bool {
	return strings.ContainsRune(s, 0x1b)
}

// backgroundSGRRe matches an SGR background color parameter in any of
// the three shapes a terminal color profile can pick: classic 40-47,
// bright 100-107, or extended 48;5;<n> (256-color) / 48;2;<r>;<g>;<b>
// (true-color) — used by the pill tests below, which must recognize a
// BACKGROUND color regardless of which of these profiles the test
// process's own color-capability detection lands on (unlike
// TestEntityRendersTrueColorWhenTerminalIsCapable, these tests do not
// pin COLORTERM/TERM themselves, so the profile is whatever the
// environment resolves to).
var backgroundSGRRe = regexp.MustCompile(`\x1b\[[0-9;]*(4[0-7]|10[0-7]|48;[0-9;]+)m`)

func hasBackgroundSGR(s string) bool {
	return backgroundSGRRe.MatchString(s)
}

// TestSemanticSinksNeverHashAnUnknownName pins the semantic-vs-entity
// split (x/palette.md): Status and Priority color only the small fixed
// vocabulary they know, and fall through to the EXACT input for
// anything else. Entity, by contrast, hashes every name it is given —
// the same contrast the design doc's "never use an entity palette for
// semantic state" line is protecting.
func TestSemanticSinksNeverHashAnUnknownName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, tt := range []struct {
		name string
		fn   func(string) string
	}{
		// category "" is Status's own documented "no category signal"
		// case (Status's doc comment) — this proves the SAME "unknown
		// stays plain" contract Priority already pins, for the fallback
		// path Status falls to once category cannot help.
		{name: "status", fn: func(s string) string { return Status(s, "") }},
		{name: "priority", fn: Priority},
	} {
		t.Run(tt.name, func(t *testing.T) {
			custom := "Pending Customer Review"
			if got := tt.fn(custom); got != custom {
				t.Errorf("%s(%q) = %q, want the name unchanged — an unrecognized name must never be colored", tt.name, custom, got)
			}
		})
	}
}

// TestStatusColorsTheThreeDefaultCategoriesByName pins Status's
// FALLBACK map (statusNameColor) — reached with category=="", exactly
// the shape Status colored by before it learned about statusCategory —
// case- and space-insensitively, and pilled (padding, background) like
// every other Status output, not merely foreground-colored.
func TestStatusColorsTheThreeDefaultCategoriesByName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, name := range []string{"To Do", "to do", " TO DO ", "In Progress", "Done"} {
		if got := Status(name, ""); !hasANSI(got) {
			t.Errorf("Status(%q, \"\") = %q, want it styled", name, got)
		}
	}
}

// TestStatusColorsByCategory pins the fix this whole slice exists for:
// Status colors from statusCategory.key, not the status NAME — proven
// here with a name Status has never heard of ("Zzyzx Unknown Status"),
// paired with each of Jira's four real category keys. The OLD name-only
// Status would have rendered every one of these completely plain; this
// must style every one of them, from the category alone.
func TestStatusColorsByCategory(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	// Pin the glyph set: these assert the padded-space pill, which is
	// GlyphSetDefault's shape. Without this they read the DEVELOPER's own
	// `glyphs` key and see capped pills on a machine set to nerd.
	setGlyphs(t, config.GlyphSetDefault)

	const unknownName = "Zzyzx Unknown Status"
	for _, category := range slices.Sorted(maps.Keys(jiraStatusColors)) {
		t.Run(string(category), func(t *testing.T) {
			got := Status(unknownName, category)
			if !hasANSI(got) {
				t.Fatalf("Status(%q, %q) = %q, want it styled from the category alone", unknownName, category, got)
			}
			if stripped := ansi.Strip(got); stripped != " "+unknownName+" " {
				t.Errorf("Status(%q, %q) stripped = %q, want the name padded by exactly one space either side (the pill's own padding)", unknownName, category, stripped)
			}
		})
	}
}

// TestStatusCustomNameWithKnownCategoryColors is the exact regression
// this task exists to fix, reproduced directly: a project's own custom
// workflow status name ("Blocked" — not one of Jira's three English
// defaults) still colors correctly as long as Jira told this CLI its
// category, because Status now keys off the category rather than
// pattern-matching the name.
func TestStatusCustomNameWithKnownCategoryColors(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	// Pin the glyph set: these assert the padded-space pill, which is
	// GlyphSetDefault's shape. Without this they read the DEVELOPER's own
	// `glyphs` key and see capped pills on a machine set to nerd.
	setGlyphs(t, config.GlyphSetDefault)

	got := Status("Blocked", statusCategoryIndeterminate)
	if !hasANSI(got) {
		t.Fatalf(`Status("Blocked", %q) = %q, want it styled — a custom status name with a known category must still color`, statusCategoryIndeterminate, got)
	}
	if stripped := ansi.Strip(got); stripped != " Blocked " {
		t.Errorf(`Status("Blocked", %q) stripped = %q, want " Blocked "`, statusCategoryIndeterminate, stripped)
	}
}

// TestStatusUnrecognizedCategoryFallsBackToNameMap pins the OTHER half
// of Status's documented fallback: a category value that is not one of
// Jira's four known keys (a future addition, or a malformed response)
// falls back to the name map exactly like category=="" does — proven
// with a name the fallback DOES know ("Done"), which must still color
// despite the bogus category.
func TestStatusUnrecognizedCategoryFallsBackToNameMap(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	got := Status("Done", "some-future-category-jira-has-not-invented-yet")
	if !hasANSI(got) {
		t.Errorf("Status(\"Done\", bogus-category) = %q, want the name fallback to still color it", got)
	}
}

// TestStatusPillHasBackgroundAndPadding is the H5-adjacent byte-level
// proof the design brief asks for: a category with a REAL theme color
// (done/indeterminate, not the colorless Dim slot new/undefined reach
// for) renders as an actual filled badge — a background SGR parameter
// (48;...) wrapping the name padded by one space either side — not
// merely foreground-colored text.
func TestStatusPillHasBackgroundAndPadding(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	// Pin the glyph set: these assert the padded-space pill, which is
	// GlyphSetDefault's shape. Without this they read the DEVELOPER's own
	// `glyphs` key and see capped pills on a machine set to nerd.
	setGlyphs(t, config.GlyphSetDefault)

	got := Status("Custom Done Name", statusCategoryDone)
	if !hasBackgroundSGR(got) {
		t.Errorf("Status(..., %q) = %q, want a background SGR parameter — a pill, not plain colored text", statusCategoryDone, got)
	}
	if stripped := ansi.Strip(got); stripped != " Custom Done Name " {
		t.Errorf("Status(..., %q) stripped = %q, want the name padded by exactly one space either side", statusCategoryDone, stripped)
	}
}

// TestStatusPillDegradesToPlainNameUnderNoColor is the NO_COLOR half of
// the pill contract (design brief H4): with styling disabled, a
// category that WOULD pill under color must instead render as EXACTLY
// the plain name — no padding artifact, zero escape bytes.
func TestStatusPillDegradesToPlainNameUnderNoColor(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)

	for _, category := range slices.Sorted(maps.Keys(jiraStatusColors)) {
		got := Status("Done", category)
		if got != "Done" {
			t.Errorf("Status(\"Done\", %q) = %q, want exactly the plain name with styling off", category, got)
		}
		if hasANSI(got) {
			t.Errorf("Status(\"Done\", %q) = %q, carries an escape byte with styling disabled", category, got)
		}
	}
}

// TestPriorityColorsTheFiveDefaultLevels pins the same for Priority's
// five default levels.
func TestPriorityColorsTheFiveDefaultLevels(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, name := range []string{"Highest", "High", "Medium", "Low", "Lowest", "  low  "} {
		if got := Priority(name); !hasANSI(got) {
			t.Errorf("Priority(%q) = %q, want it styled", name, got)
		}
	}
}

// TestEntityColorsAnyName pins that Entity, unlike Status/Priority,
// colors whatever it is given — that is the whole point of an entity
// palette (repeated identities with no semantic state of their own).
func TestEntityColorsAnyName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, name := range []string{"a.developer", "unassigned-project-key", "some label"} {
		if got := Entity(name); !hasANSI(got) {
			t.Errorf("Entity(%q) = %q, want it styled — entity coloring must not require a known name", name, got)
		}
	}
}

// TestNumberFormatsThousandsSeparators pins Number's formatting at the
// boundaries FormatNumber's own grouping logic actually changes
// behavior at — below the first group (0, 999), exactly at the boundary
// (1000), and a multi-group value (1234567) — plus a negative input:
// no call site produces one today, but Number must still do something
// sane (grouped, minus sign kept, no double-negative or dropped digit)
// rather than something surprising. Run with styling off so want is the
// exact byte-for-byte string, not a styled one this test would have to
// strip first.
func TestNumberFormatsThousandsSeparators(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)

	for _, tt := range []struct {
		n    int
		want string
	}{
		{n: 0, want: "0"},
		{n: 999, want: "999"},
		{n: 1000, want: "1,000"},
		{n: 1234567, want: "1,234,567"},
		{n: -1234, want: "-1,234"},
	} {
		if got := Number(tt.n); got != tt.want {
			t.Errorf("Number(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestNumberColorsWhenStylingIsOn is the positive half of the NO_COLOR
// contract TestSinksProduceZeroANSIWhenStylingIsOff pins the negative
// half of below: with styling on, Number must actually carry an SGR
// sequence, and the formatted digits must survive being wrapped in it.
func TestNumberColorsWhenStylingIsOn(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	got := Number(1234567)
	if !hasANSI(got) {
		t.Fatalf("Number(1234567) = %q, want it styled", got)
	}
	if !strings.Contains(got, "1,234,567") {
		t.Errorf("Number(1234567) = %q, lost the formatted digits", got)
	}
}

// TestNumberUsesClogsOwnFieldNumberStyle pins WHERE Number's color comes
// from: clog's own numeric-field style (style.Config.FieldNumber, via
// style.Default() — see numberStyle's own doc comment for why that is
// the exact Config clog itself renders every other int/float field
// with), not a color this package invented or copied. A count Number
// renders and an int clog itself logs must land on the IDENTICAL SGR
// sequence — this compares against style.Default().FieldNumber.Render
// directly, rather than a hard-coded "\x1b[35m" or similar, so the test
// keeps tracking clog's own choice if clog ever changes it.
func TestNumberUsesClogsOwnFieldNumberStyle(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	want := style.Default().FieldNumber.Render("1,000")
	if got := Number(1000); got != want {
		t.Errorf("Number(1000) = %q, want %q (clog's own FieldNumber style)", got, want)
	}
}

// TestSinksProduceZeroANSIWhenStylingIsOff is the NO_COLOR/non-TTY
// contract: every sink that colors anything must fall back to
// byte-for-byte plain text once StyleEnabled is false, regardless of
// what the resolved theme would otherwise apply.
func TestSinksProduceZeroANSIWhenStylingIsOff(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)

	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{name: "Status(done)", got: Status("Done", ""), want: "Done"},
		{name: "Status(done,category)", got: Status("Done", statusCategoryDone), want: "Done"},
		{name: "Type(bug)", got: Type("Bug", 0), want: "Bug"},
		{name: "Priority(highest)", got: Priority("Highest"), want: "Highest"},
		{name: "Entity(x)", got: Entity("a.developer"), want: "a.developer"},
		{name: "Number(1234567)", got: Number(1234567), want: "1,234,567"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want unstyled %q", tt.name, tt.got, tt.want)
		}
		if hasANSI(tt.got) {
			t.Errorf("%s carries an escape sequence with styling disabled", tt.name)
		}
	}
}

// TestSameColorNilGuard pins sameColor's own defensive behavior — a
// Palette.Color miss returns a nil color.Color, and this is the test
// helper every other palette test compares that result through, so a
// nil here must not panic the whole suite instead of failing the one
// assertion that actually hit it.
func TestSameColorNilGuard(t *testing.T) {
	real := lipgloss.Color("#ff0000")
	if !sameColor(nil, nil) {
		t.Error("sameColor(nil, nil) = false, want true — both absent counts as the same")
	}
	if sameColor(nil, real) {
		t.Error("sameColor(nil, real) = true, want false")
	}
	if sameColor(real, nil) {
		t.Error("sameColor(real, nil) = true, want false")
	}
	if !sameColor(real, real) {
		t.Error("sameColor(real, real) = false, want true")
	}
}

// TestEntityPaletteReservesSemanticColors proves entityPalette's actual
// reservation, now that Entity builds its own palette instead of
// deferring to clib's bundled RenderEntity (which never reserved
// against its own theme's semantic slots — see Entity's doc comment for
// the collision this replaces). semanticColors(theme.Dark()) is a real,
// non-empty reserved set, and newEntityPalette must keep every color it
// returns perceptually clear of it — on both the true-color and
// ANSI-256 capability paths, since x/palette.md notes the ANSI-256 path
// measures colors after terminal quantization, a different comparison
// than true-color's.
func TestEntityPaletteReservesSemanticColors(t *testing.T) {
	th := theme.Dark()
	reserved := semanticColors(th)
	if len(reserved) == 0 {
		t.Fatal("theme.Dark() yielded no real semantic foreground colors to reserve — this test would prove nothing")
	}

	for _, tt := range []struct {
		name      string
		colorterm string
		term      string
	}{
		{name: "true-color capable", colorterm: "truecolor", term: "xterm-256color"},
		{name: "ansi-256 only", colorterm: "", term: "xterm-256color"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLORTERM", tt.colorterm)
			t.Setenv("TERM", tt.term)

			entities := newEntityPalette(th)
			if len(entities) == 0 {
				t.Fatal("WithReserved emptied the palette entirely — nothing left to check")
			}
			for _, e := range entities {
				for _, r := range reserved {
					if sameColor(e, r) {
						t.Errorf("a reserved semantic color is still present in the entity palette: %v", e)
					}
				}
			}
		})
	}
}

// TestEntityColorIsStableForCanonicalKey pins x/palette.md's
// verification list: "the same canonical key is stable within an
// explicit palette." Case and surrounding whitespace must not change
// the color — Entity canonicalizes the lookup key exactly like Status
// and Priority — and the same lookup on a freshly built palette (same
// inputs) must agree, since nothing about Palette.Color depends on
// encounter order or process-global state.
//
// Deliberately does NOT assert which color a key gets — x/palette.md
// says never to golden-test a particular color as an entity's identity
// — only that it is the SAME color every time.
func TestEntityColorIsStableForCanonicalKey(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")

	first := newEntityPalette(theme.Dark())
	second := newEntityPalette(theme.Dark())

	variants := []string{"Agent DX", "agent dx", " AGENT DX ", "  agent dx  "}
	want := first.Color(canonicalize(variants[0]))
	if want == nil {
		t.Fatal("newEntityPalette(theme.Dark()) returned no color for a real key")
	}
	for _, v := range variants {
		if got := first.Color(canonicalize(v)); !sameColor(got, want) {
			t.Errorf("first.Color(canonicalize(%q)) = %v, want %v (same identity as %q)", v, got, want, variants[0])
		}
		if got := second.Color(canonicalize(v)); !sameColor(got, want) {
			t.Errorf("a fresh newEntityPalette build resolved %q to %v, want %v — same inputs must agree", v, got, want)
		}
	}
}

// TestEntityIsEntityAsWithSameKeyAndText pins the ONE-color-choosing-path
// contract EntityAs's own doc comment promises: Entity(name) must be
// byte-for-byte identical to EntityAs(name, name), so a caller of either
// gets the same color for the same identity — never two independent
// lookups that could drift apart.
func TestEntityIsEntityAsWithSameKeyAndText(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, name := range []string{"a.developer", "Ada Lovelace", " Padded Name "} {
		if got, want := EntityAs(name, name), Entity(name); got != want {
			t.Errorf("EntityAs(%[1]q, %[1]q) = %q, want Entity(%[1]q) = %q", name, got, want)
		}
	}
}

// TestEntityAsColorsByKeyNotText is EntityAs's own reason to exist: the
// color must be chosen from key alone, even when text is a larger
// string built around it (a divider title combining an author and a
// timestamp) — a DIFFERENT text sharing the same key must land on the
// exact same color, and changing text alone (key held fixed) must never
// change which color comes back.
func TestEntityAsColorsByKeyNotText(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	key := "Ada Lovelace"
	longText := key + " • 4 weeks ago"
	shortForm := EntityAs(key, key)
	longForm := EntityAs(key, longText)

	// The style codes lipgloss opens with, up to where the visible text
	// itself starts — Render wraps text as prefix+text+suffix, and text
	// reaches the output verbatim (sanitize does not touch these inputs),
	// so its first occurrence marks where the color-opening codes end.
	openCodesOf := func(t *testing.T, rendered, text string) string {
		t.Helper()
		idx := strings.Index(rendered, text)
		if idx < 0 {
			t.Fatalf("rendered = %q, does not contain its own text %q", rendered, text)
		}
		return rendered[:idx]
	}
	shortOpen := openCodesOf(t, shortForm, key)
	longOpen := openCodesOf(t, longForm, longText)
	if shortOpen != longOpen {
		t.Errorf("EntityAs(%q, longer text) opened with %q, want the same color prefix as EntityAs(%q, %q) = %q — the color must come from key alone", key, longOpen, key, key, shortOpen)
	}
}

// TestEntityAsRendersOneUnbrokenSpan pins the actual mechanism item 1's
// fix relies on: EntityAs colors text in ONE lipgloss Render call, so
// the result carries exactly one reset, at the very end — never an
// embedded one that could reset mid-string once something else (clog's
// Divider, say) wraps the whole result in its own style.
func TestEntityAsRendersOneUnbrokenSpan(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	got := EntityAs("Ada Lovelace", "Ada Lovelace • 4 weeks ago")
	if !hasANSI(got) {
		t.Fatalf("EntityAs(...) = %q, want it styled — this test would prove nothing unstyled", got)
	}
	if !strings.Contains(got, "Ada Lovelace • 4 weeks ago") {
		t.Errorf("EntityAs(...) = %q, lost the text content", got)
	}
	// Exactly one reset, and it is the string's own suffix: anything else
	// would mean a reset landed mid-string, which is precisely the shape
	// that broke comment.go's divider title (see comment_test.go's
	// resetsStayTrailing for the full byte-level reasoning).
	const reset = "\x1b[m"
	if n := strings.Count(got, reset); n != 1 {
		t.Fatalf("EntityAs(...) = %q, has %d reset sequences, want exactly 1", got, n)
	}
	if !strings.HasSuffix(got, reset) {
		t.Errorf("EntityAs(...) = %q, its one reset is not at the very end", got)
	}
}

// Kills a palette that always picks true color or always picks ANSI-256.
// x/terminal caches capability per process; each case must start with its
// environment already set, not mutate it after another test populated the cache.
func TestEntityRendersTrueColorWhenTerminalIsCapable(t *testing.T) {
	if want := os.Getenv("JIRA_TEST_ENTITY_COLOR"); want != "" {
		p := newEntityPalette(theme.Dark())
		c := p.Color("agent-dx")
		if c == nil {
			t.Fatal("no color resolved for a real key")
		}
		got := lipgloss.NewStyle().Foreground(c).Render("agent-dx")
		if !strings.Contains(got, want) {
			t.Fatalf("rendered %q, want SGR %q", got, want)
		}
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		colorterm string
		want      string
	}{
		{name: "true-color capable terminal", colorterm: "truecolor", want: "38;2;"},
		{name: "ansi-256-only terminal", want: "38;5;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestEntityRendersTrueColorWhenTerminalIsCapable$", "-test.count=1")
			// Deliberately exclude host TERM_PROGRAM, TMUX and force-color
			// variables: they would describe the developer, not this case.
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"TERM=xterm-256color",
				"COLORTERM=" + tt.colorterm,
				"JIRA_TEST_ENTITY_COLOR=" + tt.want,
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("color subprocess: %v\n%s", err, out)
			}
		})
	}
}

func TestCustomThemeEntitySinkUsesAuthoredPaletteAndReservesRenderedSemantics(t *testing.T) {
	if profile := os.Getenv("JIRA_TEST_CUSTOM_ENTITY_PROFILE"); profile != "" {
		definition, err := config.ParseThemeFile([]byte("base='dark'\nentity_colors=['#ffffff','#242424','#ff0000']\n[colors]\nred='#ff0000'\n"))
		if err != nil {
			t.Fatal(err)
		}
		selected, _, err := customTheme(definition)
		if err != nil {
			t.Fatal(err)
		}
		original := slices.Clone(selected.EntityColors)
		setChosenTheme(selected)
		withStyle(t, true)
		entities := newEntityPalette(selected)
		if len(entities) != 2 {
			t.Fatalf("custom entity palette has %d colors; want two authored colors after excluding semantic red", len(entities))
		}
		for i, expected := range original[:2] {
			if profile == "ansi256" {
				expected = colorprofile.ANSI256.Convert(expected)
			}
			if !sameColor(entities[i], expected) {
				t.Fatalf("custom entity palette color %d is %v; want authored color %v", i, entities[i], expected)
			}
		}
		for i, expected := range original {
			if !sameColor(selected.EntityColors[i], expected) {
				t.Fatal("palette construction mutated the resolved theme")
			}
		}
		for _, key := range []string{"First Person", "second person", " THIRD PERSON "} {
			want := lipgloss.NewStyle().Foreground(entities.Color(canonicalize(key))).Render(key)
			if got := Entity(key); got != want {
				t.Fatalf("Entity(%q) ignored the authored palette: got %q; want %q", key, got, want)
			}
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"truecolor", "ansi256"} {
		t.Run(profile, func(t *testing.T) {
			colorterm := ""
			if profile == "truecolor" {
				colorterm = "truecolor"
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestCustomThemeEntitySinkUsesAuthoredPaletteAndReservesRenderedSemantics$", "-test.count=1")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "COLORTERM=" + colorterm, "JIRA_TEST_CUSTOM_ENTITY_PROFILE=" + profile}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("custom entity subprocess: %v\n%s", err, out)
			}
		})
	}
}

// sameColor compares two color.Color values by their rendered RGBA
// rather than Go type, since clib's semantic styles and entity palette
// need not use the identical concrete color implementation to
// represent the same color.
//
// nil-guarded before either RGBA() call: a Palette.Color miss returns a
// nil color.Color, and calling RGBA() through a nil interface value
// panics rather than returning zero — a caller that has not already
// t.Fatal-ed on a nil lookup (not every call site does, on every value
// it compares) must not crash the test on the compare instead. Both nil
// counts as the same color; exactly one nil never does, regardless of
// what the other's components happen to be.
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestMarkupSinksStyleWhenEnabled pins that each ADF-mark sink actually
// styles its input when styling is on — the counterpart to
// TestMarkupSinksProduceZeroANSIWhenStylingIsOff below.
func TestMarkupSinksStyleWhenEnabled(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, tt := range []struct {
		name string
		fn   func(string) string
	}{
		{name: "MarkupStrong", fn: MarkupStrong},
		{name: "MarkupEmphasis", fn: MarkupEmphasis},
		{name: "MarkupCode", fn: MarkupCode},
	} {
		if got := tt.fn("text"); !hasANSI(got) {
			t.Errorf("%s(%q) = %q, want it styled", tt.name, "text", got)
		}
	}
}

// TestMarkupSinksProduceZeroANSIWhenStylingIsOff mirrors
// TestSinksProduceZeroANSIWhenStylingIsOff for the mark sinks: the
// non-TTY/NO_COLOR contract applies to ADF-mark styling exactly as it
// does to Status/Priority/Entity.
func TestMarkupSinksProduceZeroANSIWhenStylingIsOff(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)

	for _, tt := range []struct {
		name string
		got  string
	}{
		{name: "MarkupStrong", got: MarkupStrong("text")},
		{name: "MarkupEmphasis", got: MarkupEmphasis("text")},
		{name: "MarkupCode", got: MarkupCode("text")},
	} {
		if tt.got != "text" {
			t.Errorf("%s = %q, want unstyled %q", tt.name, tt.got, "text")
		}
		if hasANSI(tt.got) {
			t.Errorf("%s carries an escape sequence with styling disabled", tt.name)
		}
	}
}

// TestTimeFromHumanizes pins the humanization itself and the injected
// reference instant that makes it deterministic.
func TestTimeFromHumanizes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	then := now.Add(-3 * time.Hour)

	got := TimeFrom(then.Format(time.RFC3339), now)
	if !strings.Contains(got, "ago") {
		t.Errorf("TimeFrom(3h ago) = %q, want it to read as relative time", got)
	}
}

// TestTimeFromLeavesUnparsableInputAlone pins that a value Jira did not
// send as RFC 3339 renders as given rather than vanishing or panicking.
func TestTimeFromLeavesUnparsableInputAlone(t *testing.T) {
	now := time.Now()
	bad := "not-a-timestamp"
	if got := TimeFrom(bad, now); got != bad {
		t.Errorf("TimeFrom(%q) = %q, want it unchanged", bad, got)
	}
}

// TestTimeFromParsesJiraColonlessOffset pins the regression a live render
// caught: Jira Cloud sends timestamps with a COLONLESS offset and
// milliseconds (2026-07-06T04:35:26.750-0400), which time.RFC3339 rejects
// — so before jiraTimeLayout every comment and date rendered as raw ISO
// instead of a relative string. A real Jira-shaped value must humanize,
// not fall through to the as-given branch.
func TestTimeFromParsesJiraColonlessOffset(t *testing.T) {
	const jira = "2026-07-06T04:35:26.750-0400" // 08:35:26 UTC
	now := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)

	got := TimeFrom(jira, now)
	if got == jira {
		t.Fatalf("TimeFrom left the Jira timestamp raw — colonless-offset layout not parsed: %q", got)
	}
	if !strings.Contains(got, "ago") {
		t.Errorf("TimeFrom(%q) = %q, want a relative string", jira, got)
	}
}

// TestParseTimeParsesBothLayouts pins ParseTime's own contract in
// isolation from TimeFrom's humanizing: Jira's colonless-offset layout
// parses, plain RFC 3339 parses as the documented fallback, and a value
// matching neither reports ok false rather than a zero time.Time — the
// distinction TimeFrom's own "render as given" fallback depends on to
// tell "parsed to the zero instant" apart from "did not parse at all".
func TestParseTimeParsesBothLayouts(t *testing.T) {
	if _, ok := ParseTime("2026-07-06T04:35:26.750-0400"); !ok {
		t.Error("ParseTime did not parse Jira's own colonless-offset layout")
	}
	if _, ok := ParseTime("2024-01-01T00:00:00Z"); !ok {
		t.Error("ParseTime did not parse the RFC 3339 fallback")
	}
	if _, ok := ParseTime("not-a-timestamp"); ok {
		t.Error("ParseTime(\"not-a-timestamp\") = ok true, want false")
	}
}

// TestSemanticAndEntitySinksCannotDriveTheTerminal pins the same threat
// auth's TestRenderedIdentityCannotDriveTheTerminal guards against, for
// the three sinks that render a Jira-authored name directly: a status
// name, a priority name, or an entity name (assignee/reporter display
// name, label, project key, ...) carrying a hostile CSI/CR/BEL sequence
// must never let those bytes reach the terminal.
//
// Checked with styling both on and off. Off, the sink must produce ZERO
// ANSI of its own — the same byte-for-byte contract
// TestSinksProduceZeroANSIWhenStylingIsOff pins — so any escape found is
// unambiguously the hostile input leaking through. On, the sink legitimately
// wraps the name in its own SGR color codes (ESC "[" ... "m"), so a bare
// "any ESC present" check would misfire on that real styling; the hostile
// input is instead the CSI "\x1b[2K" (erase-line) sequence specifically,
// plus the bare CR and BEL bytes, neither of which any SGR color code
// contains — their absence, even under styling, is what proves the
// sanitizer ran before the style wrapper, not that no ESC exists at all.
func TestSemanticAndEntitySinksCannotDriveTheTerminal(t *testing.T) {
	deterministicTheme(t)
	hostile := "Real Name\x1b[2K\rSUDO PASSWORD:\x07"

	for _, tt := range []struct {
		name string
		fn   func(string) string
	}{
		// category "": hostile's own name matches nothing in either
		// Status map, so this exercises the SAME "no color found, render
		// plain sanitized" path the old 1-arg Status took for an unknown
		// name. TestStatusPillSanitizesHostileNameWhenCategorized below
		// covers the pill-styled path this table cannot reach with a
		// func(string) string shape.
		{name: "Status", fn: func(s string) string { return Status(s, "") }},
		{name: "Priority", fn: Priority},
		{name: "Entity", fn: Entity},
		{name: "Type", fn: func(s string) string { return Type(s, 0) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("styled", func(t *testing.T) {
				withStyle(t, true)
				got := tt.fn(hostile)
				if strings.Contains(got, "\x1b[2K") {
					t.Errorf("%s(%q) = %q, the hostile erase-line CSI sequence reached the terminal", tt.name, hostile, got)
				}
				if strings.ContainsRune(got, '\r') {
					t.Errorf("%s(%q) = %q, a carriage return from Jira reached the terminal", tt.name, hostile, got)
				}
				if strings.ContainsRune(got, '\x07') {
					t.Errorf("%s(%q) = %q, a BEL from Jira reached the terminal", tt.name, hostile, got)
				}
			})
			t.Run("unstyled", func(t *testing.T) {
				withStyle(t, false)
				got := tt.fn(hostile)
				if hasANSI(got) {
					t.Errorf("%s(%q) = %q, carries an escape byte with styling disabled", tt.name, hostile, got)
				}
				if strings.ContainsRune(got, '\r') {
					t.Errorf("%s(%q) = %q, a carriage return from Jira reached the terminal", tt.name, hostile, got)
				}
				if strings.ContainsRune(got, '\x07') {
					t.Errorf("%s(%q) = %q, a BEL from Jira reached the terminal", tt.name, hostile, got)
				}
			})
		})
	}
}

// TestStatusPillSanitizesHostileNameWhenCategorized is the pill-styled
// half of the H4 proof TestSemanticAndEntitySinksCannotDriveTheTerminal's
// own func(string) string table cannot reach: with a REAL category (so
// statusPill actually builds a Background/Foreground/Padding style and
// Renders through it, rather than falling through to the plain "no
// color found" branch), a hostile status name must still reach the
// pill with none of its attack bytes surviving — proving sanitize()
// runs BEFORE the name reaches statusPill's own Render call, not after.
func TestStatusPillSanitizesHostileNameWhenCategorized(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	hostile := "Real Name\x1b[2K\rSUDO PASSWORD:\x07"
	got := Status(hostile, statusCategoryDone)

	if !hasBackgroundSGR(got) {
		t.Fatalf("Status(hostile, %q) = %q, want it pilled (background SGR) — this test proves nothing about the pill path otherwise", statusCategoryDone, got)
	}
	if strings.Contains(got, "\x1b[2K") {
		t.Errorf("Status(hostile, %q) = %q, the hostile erase-line CSI sequence reached the terminal", statusCategoryDone, got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Errorf("Status(hostile, %q) = %q, a carriage return from Jira reached the terminal", statusCategoryDone, got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("Status(hostile, %q) = %q, a BEL from Jira reached the terminal", statusCategoryDone, got)
	}
}

// TestTypeStructuralCasesSanitizeHostileNames mirrors the above for
// Type's own two structural (hierarchyLevel-driven) branches — epic and
// subtask — which style directly via styleOrPlain rather than falling
// through to Entity's own (already independently tested) sanitization,
// so each needs its own proof that the hostile bytes never survive.
func TestTypeStructuralCasesSanitizeHostileNames(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	hostile := "Real Name\x1b[2K\rSUDO PASSWORD:\x07"
	for _, tt := range []struct {
		name           string
		hierarchyLevel int64
	}{
		{name: "epic", hierarchyLevel: 1},
		{name: "subtask", hierarchyLevel: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Type(hostile, tt.hierarchyLevel)
			if strings.Contains(got, "\x1b[2K") {
				t.Errorf("Type(hostile, %d) = %q, the hostile erase-line CSI sequence reached the terminal", tt.hierarchyLevel, got)
			}
			if strings.ContainsRune(got, '\r') {
				t.Errorf("Type(hostile, %d) = %q, a carriage return from Jira reached the terminal", tt.hierarchyLevel, got)
			}
			if strings.ContainsRune(got, '\x07') {
				t.Errorf("Type(hostile, %d) = %q, a BEL from Jira reached the terminal", tt.hierarchyLevel, got)
			}
		})
	}
}

// TestTypeColorsStructuralCasesByHierarchyLevel pins Type's own
// language-independent signal: hierarchyLevel 1 (epic) and -1 (subtask)
// color from the LEVEL alone, regardless of what the type is named — a
// renamed Epic ("Initiative") must still color as an epic.
func TestTypeColorsStructuralCasesByHierarchyLevel(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, tt := range []struct {
		name           string
		hierarchyLevel int64
	}{
		{name: "Initiative", hierarchyLevel: 1},
		{name: "Chore", hierarchyLevel: -1},
	} {
		got := Type(tt.name, tt.hierarchyLevel)
		if !hasANSI(got) {
			t.Errorf("Type(%q, %d) = %q, want it styled from hierarchyLevel alone", tt.name, tt.hierarchyLevel, got)
		}
		if !strings.Contains(got, tt.name) {
			t.Errorf("Type(%q, %d) = %q, lost the name", tt.name, tt.hierarchyLevel, got)
		}
	}
}

// TestTypeColorsWellKnownStandardNamesByName pins Type's second
// signal: at hierarchyLevel 0 (or unset — Nullable's own absent-value
// zero), Bug/Story/Task color by name, matching Jira's own near-brand
// colors.
func TestTypeColorsWellKnownStandardNamesByName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	for _, name := range []string{"Bug", "bug", "Story", "Task", " task "} {
		if got := Type(name, 0); !hasANSI(got) {
			t.Errorf("Type(%q, 0) = %q, want it styled by name", name, got)
		}
	}
}

// TestTypeFallsBackToEntityHashForACustomType pins Type's third
// signal: a tenant-custom type name (not Bug/Story/Task, hierarchyLevel
// 0) still colors — via Entity's hash, the same fallback Type's own doc
// comment documents — rather than rendering plain. Proven against
// Entity's own output directly: Type must delegate, not reimplement.
func TestTypeFallsBackToEntityHashForACustomType(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	const custom = "Feature Request"
	if got, want := Type(custom, 0), Entity(custom); got != want {
		t.Errorf("Type(%q, 0) = %q, want the same as Entity(%q) = %q — Type's own documented fallback", custom, got, custom, want)
	}
}

// hyperlinkOpen and hyperlinkClose are the exact OSC 8 open/close byte
// shapes x/ansi.ANSI.Hyperlink emits — verified once against the real
// pinned v0.5.14 (BEL as the ST terminator, not the ESC-backslash form;
// both are valid OSC terminators, this is simply which one this specific
// dependency writes) and reused by every byte-exact test below so a
// future dependency bump that changed the terminator would fail loudly
// here rather than in several places at once.
func hyperlinkOpen(url string) string { return "\x1b]8;;" + url + "\a" }

const hyperlinkClose = "\x1b]8;;\a"

// TestIssueKeyLinksWhenStyledAndPlainWhenNot is IssueKey's own core
// contract, byte-exact both ways: with styling on and a real site, the
// key renders as an OSC 8 hyperlink wrapping the sanitized key text and
// nothing else; with styling off, it renders as exactly the plain
// sanitized key — never the "key (url)" HyperlinkFallback shape, which
// piped/non-TTY output must never see (see IssueKey's own doc comment).
func TestIssueKeyLinksWhenStyledAndPlainWhenNot(t *testing.T) {
	const site = "https://example.atlassian.net"
	const key = "ABC-123"
	const wantURL = site + "/browse/" + key

	t.Run("styled", func(t *testing.T) {
		withStyle(t, true)
		got := IssueKey(site, key)
		want := hyperlinkOpen(wantURL) + key + hyperlinkClose
		if got != want {
			t.Errorf("IssueKey(%q, %q) = %q, want the OSC 8 hyperlink %q", site, key, got, want)
		}
	})

	t.Run("unstyled", func(t *testing.T) {
		withStyle(t, false)
		got := IssueKey(site, key)
		if got != key {
			t.Errorf("IssueKey(%q, %q) = %q, want the plain key %q", site, key, got, key)
		}
		if hasANSI(got) {
			t.Errorf("IssueKey(%q, %q) = %q, carries an escape byte with styling disabled", site, key, got)
		}
		if strings.Contains(got, wantURL) {
			t.Errorf("IssueKey(%q, %q) = %q, leaked the browse URL into unstyled output", site, key, got)
		}
	})
}

// TestIssueKeyMissingSiteDegradesToPlain pins the "not every invocation
// is authenticated" requirement: styling genuinely on is not, by itself,
// enough to link — an empty site (no client ever resolved a base URL, or
// one failed to construct) must fall back to the plain sanitized key,
// never a partial or malformed URL built from nothing.
func TestIssueKeyMissingSiteDegradesToPlain(t *testing.T) {
	withStyle(t, true)

	got := IssueKey("", "ABC-123")
	if got != "ABC-123" {
		t.Errorf(`IssueKey("", "ABC-123") = %q, want the plain key`, got)
	}
	if hasANSI(got) {
		t.Errorf(`IssueKey("", "ABC-123") = %q, carries an escape byte despite having no site to link to`, got)
	}
}

// issueKeyAttackPayload mirrors tests/audit/hostile.go's own
// attackPayload (an ESC[ CSI SGR sequence, an OSC 8 hyperlink including
// its own ST terminator, a lone BEL, DEL, a correctly UTF-8-encoded C1
// NEL, and a bare CR) — reused here rather than imported, since that
// file is outputaudit-tagged and this one is not. Every byte class in it
// sanitizes to nothing (sinks.go's sanitize: ansi.Strip removes both
// escape sequences, the rune filter drops the rest), so appending it to
// a real key proves the sanitized result is byte-for-byte the clean key,
// exactly as hostile.go's own doc comment states for the audit's
// identical composition.
const issueKeyAttackPayload = "\x1b[31m" +
	"\x1b]8;;http://evil.example/\x1b\\" +
	"\x1b]8;;\x1b\\" +
	"\x07" +
	"\x7f" +
	"\u0085" +
	"\r"

// TestIssueKeyHostileKeyNeverProducesRawEscapesOrAMalformedURL is the H4
// proof for this sink, in two shapes:
//
//   - A key carrying issueKeyAttackPayload sanitizes down to a real
//     Jira-shaped key, which DOES get linked — but the emitted bytes
//     must carry nothing of the attack: no raw ESC/CR/BEL/DEL/C1 byte
//     class from the payload, and a browse URL built only from the
//     clean, sanitized key.
//   - A key that survives sanitize() but still is not a real Jira key
//     shape (a literal space is not a control byte, so sanitize leaves it
//     alone) must get NO link at all — IssueBrowseURL's own shape gate,
//     not sanitize, is what stops a malformed value from ever reaching a
//     URL.
func TestIssueKeyHostileKeyNeverProducesRawEscapesOrAMalformedURL(t *testing.T) {
	withStyle(t, true)
	const site = "https://example.atlassian.net"

	t.Run("control/escape payload sanitizes to a real key and still links cleanly", func(t *testing.T) {
		hostile := "ABC-123" + issueKeyAttackPayload
		got := IssueKey(site, hostile)
		wantURL := site + "/browse/ABC-123"
		want := hyperlinkOpen(wantURL) + "ABC-123" + hyperlinkClose
		if got != want {
			t.Errorf("IssueKey(%q) = %q, want exactly %q — a clean link built from the sanitized key alone, none of the attack payload surviving", hostile, got, want)
		}
	})

	t.Run("a space survives sanitize but still gets no link", func(t *testing.T) {
		hostile := "ABC 123"
		got := IssueKey(site, hostile)
		if got != hostile {
			t.Errorf("IssueKey(%q) = %q, want the plain (space intact, non-key-shaped) value, no link", hostile, got)
		}
		if strings.Contains(got, "/browse/") {
			t.Errorf("IssueKey(%q) = %q, built a URL from a value that is not a real Jira key shape", hostile, got)
		}
	})
}

// TestIssueKeyHyperlinkBytesWellFormed is the H5 byte-level proof: the
// OSC 8 span IssueKey emits carries no SGR reset of its own to strand —
// it is open (ESC ] 8 ; ; URL ST), text, close (ESC ] 8 ; ; ST), with
// nothing else in between. Every current call site (issue view's glance
// line, issue list's KEY column) renders the key with no color wrapped
// around it, so this span never nests against another style at all —
// this test pins that the span itself could not corrupt one even if it
// did, by proving it contains zero "\x1b[...m" SGR bytes.
func TestIssueKeyHyperlinkBytesWellFormed(t *testing.T) {
	withStyle(t, true)
	got := IssueKey("https://example.atlassian.net", "ABC-123")

	if strings.Contains(got, "\x1b[") {
		t.Fatalf("IssueKey(...) = %q, carries a CSI/SGR byte sequence — the hyperlink span must be the only escape content here", got)
	}
	open := hyperlinkOpen("https://example.atlassian.net/browse/ABC-123")
	if !strings.HasPrefix(got, open) {
		t.Errorf("IssueKey(...) = %q, want it to open with %q", got, open)
	}
	if !strings.HasSuffix(got, hyperlinkClose) {
		t.Errorf("IssueKey(...) = %q, want it to close with %q", got, hyperlinkClose)
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(got, open), hyperlinkClose)
	if middle != "ABC-123" {
		t.Errorf("IssueKey(...) = %q, want exactly the key %q between open and close, got %q", got, "ABC-123", middle)
	}
}

// TestTextStripsANSIAndControlCharacters pins the H4 sanitization
// policy: a hostile or malformed Jira field cannot carry an escape
// sequence or a stray control byte into a rendered row.
func TestTextStripsANSIAndControlCharacters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ansi color codes", in: "\x1b[31mRED\x1b[0m", want: "RED"},
		{name: "bell", in: "click\x07here", want: "clickhere"},
		{name: "carriage return", in: "over\rwrite", want: "overwrite"},
		{name: "newline (single-line row policy)", in: "line one\nline two", want: "line oneline two"},
		{name: "c1 control byte", in: "a\u0085b", want: "ab"},
		{name: "plain text is untouched", in: "In Progress", want: "In Progress"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.in)
			if got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if hasANSI(got) {
				t.Errorf("Text(%q) left an escape byte in %q", tt.in, got)
			}
		})
	}
}

// TestStatusPaddedWidensShorterPillsToOneWidth pins the uniform-width
// rule a column of pills depends on: a name shorter than the requested
// width is padded INSIDE the pill, so "Done" occupies exactly as many
// cells as "To Do" and a column of them reads as an aligned stack rather
// than ragged badges. Asserted on visible width, since the pill carries
// ANSI the terminal never draws.
func TestStatusPaddedWidensShorterPillsToOneWidth(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetDefault)

	const width = 5 // len("To Do")
	short := ansi.StringWidth(ansi.Strip(StatusPadded("Done", statusCategoryDone, width)))
	long := ansi.StringWidth(ansi.Strip(StatusPadded("To Do", statusCategoryNew, width)))
	if short != long {
		t.Errorf("padded widths differ: Done = %d, To Do = %d — a column of these would not align", short, long)
	}
}

// TestStatusPaddedNeverTruncates proves width is a floor, not a ceiling:
// a name wider than the requested width keeps every character, because a
// clipped status ("In Progr") reads as data loss rather than as layout.
func TestStatusPaddedNeverTruncates(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetDefault)

	const name = "Ready for a Very Thorough Review"
	if got := ansi.Strip(StatusPadded(name, statusCategoryIndeterminate, 4)); !strings.Contains(got, name) {
		t.Errorf("StatusPadded(%q, ..., 4) = %q, want the whole name kept", name, got)
	}
}

// TestStatusPaddedAddsNoPaddingWhenUnstyled is the H4/H3 half: with
// styling off there is no background for padding spaces to color, so
// they would be bare trailing whitespace on a human line — noise in a
// pipe, and an artifact the output audit's own NO_COLOR captures assert
// against.
func TestStatusPaddedAddsNoPaddingWhenUnstyled(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)
	setGlyphs(t, config.GlyphSetDefault)

	if got := StatusPadded("Done", statusCategoryDone, 40); got != "Done" {
		t.Errorf("StatusPadded unstyled = %q, want exactly %q with no padding", got, "Done")
	}
}

// TestStatusPillCapsUnderNerdGlyphs pins GlyphSetNerd's own shape: the
// pill is capped with the Powerline half-circles instead of padding
// spaces, and never both.
func TestStatusPillCapsUnderNerdGlyphs(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetNerd)

	got := ansi.Strip(Status("Done", statusCategoryDone))
	if !strings.HasPrefix(got, pillCapLeft) || !strings.HasSuffix(got, pillCapRight) {
		t.Fatalf("Status under nerd glyphs = %q, want it capped with %q/%q", got, pillCapLeft, pillCapRight)
	}
	if strings.Contains(got, " Done ") {
		t.Errorf("Status under nerd glyphs = %q, want caps INSTEAD of padding spaces, not both", got)
	}
}

// TestStatusPillCapsAbsentUnderDefaultGlyphs is the other half: a
// machine without a Nerd Font must never be sent a glyph it renders as
// tofu.
func TestStatusPillCapsAbsentUnderDefaultGlyphs(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetDefault)

	if got := ansi.Strip(Status("Done", statusCategoryDone)); strings.ContainsAny(got, pillCapLeft+pillCapRight) {
		t.Errorf("Status under default glyphs = %q, want no Powerline cap glyphs", got)
	}
}

// TestPriorityUsesJiraRampNotThemeSlots pins why jiraPriorityColors
// exists: every rank in the ramp renders a DISTINCT color, and none of
// them is the near-white a theme's own Orange slot resolved to when
// Priority borrowed it — the bug that made "High" arrive as plain text.
func TestPriorityUsesJiraRampNotThemeSlots(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	seen := map[string]string{}
	for _, rank := range []string{"Highest", "High", "Medium", "Low", "Lowest"} {
		got := Priority(rank)
		if !hasANSI(got) {
			t.Errorf("Priority(%q) = %q, want it colored", rank, got)
		}
		if prior, dup := seen[got]; dup {
			t.Errorf("Priority(%q) renders identically to %q — the ramp must stay distinguishable", rank, prior)
		}
		seen[got] = rank
	}
}

// TestPriorityLeavesCustomRankPlain pins the deliberate non-decision: a
// tenant's own priority name is left uncolored rather than hashed, since
// priority is an ORDERED severity and an arbitrary color would imply a
// rank this CLI cannot know.
func TestPriorityLeavesCustomRankPlain(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	const custom = "Blocker-ish"
	if got := Priority(custom); got != custom {
		t.Errorf("Priority(%q) = %q, want it plain", custom, got)
	}
}

// TestPriorityIconRendersDistinctGlyphPerRank pins the shape half of the
// ramp: every rank draws its OWN glyph, so a column of them is readable
// without color — which is the case that matters under NO_COLOR, where
// the ramp's hue carries nothing.
func TestPriorityIconRendersDistinctGlyphPerRank(t *testing.T) {
	deterministicTheme(t)

	for _, set := range []config.GlyphSet{config.GlyphSetDefault, config.GlyphSetNerd} {
		t.Run(map[config.GlyphSet]string{config.GlyphSetDefault: "default", config.GlyphSetNerd: "nerd"}[set], func(t *testing.T) {
			withStyle(t, false)
			setGlyphs(t, set)

			seen := map[string]string{}
			for _, rank := range []string{"Highest", "High", "Medium", "Low", "Lowest"} {
				got := PriorityIcon(rank)
				if got == "" {
					t.Errorf("PriorityIcon(%q) = empty, want a glyph", rank)
				}
				if prior, dup := seen[got]; dup {
					t.Errorf("PriorityIcon(%q) draws the same glyph as %q — the ranks must stay distinguishable without color", rank, prior)
				}
				seen[got] = rank
			}
		})
	}
}

// TestPriorityIconUsesNerdGlyphsOnlyUnderNerd pins the degrade: a
// terminal whose font was never patched must never be handed a Private
// Use Area codepoint, which it would draw as tofu.
func TestPriorityIconUsesNerdGlyphsOnlyUnderNerd(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)

	setGlyphs(t, config.GlyphSetDefault)
	for _, rank := range []string{"Highest", "High", "Medium", "Low", "Lowest"} {
		for _, r := range PriorityIcon(rank) {
			if r >= 0xE000 && r <= 0xF8FF {
				t.Errorf("PriorityIcon(%q) under default glyphs = %q, which carries a Private Use Area rune", rank, PriorityIcon(rank))
			}
		}
	}

	setGlyphs(t, config.GlyphSetNerd)
	if got := PriorityIcon("Highest"); !strings.ContainsFunc(got, func(r rune) bool { return r >= 0xE000 && r <= 0xF8FF }) {
		t.Errorf("PriorityIcon(\"Highest\") under nerd glyphs = %q, want a Nerd Font glyph", got)
	}
}

// TestPriorityIconKeepsCustomRankAsItsName pins the deliberate refusal:
// a tenant's own priority gets no arrow, because an arrow asserts a
// place in an ordered ramp this CLI cannot know it belongs to.
func TestPriorityIconKeepsCustomRankAsItsName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)
	setGlyphs(t, config.GlyphSetNerd)

	const custom = "Blocker-ish"
	if got := PriorityIcon(custom); got != custom {
		t.Errorf("PriorityIcon(%q) = %q, want the name itself", custom, got)
	}
}

// TestPriorityIconAndWordShareOneColor pins that the glyph a list shows
// and the word issue view shows can never drift onto different colors —
// both resolve through priorityColor.
func TestPriorityIconAndWordShareOneColor(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetNerd)

	for _, rank := range []string{"Highest", "Medium", "Lowest"} {
		icon, word := PriorityIcon(rank), Priority(rank)
		iconSGR, wordSGR := sgrCodes(icon), sgrCodes(word)
		if len(iconSGR) == 0 || iconSGR[0] != wordSGR[0] {
			t.Errorf("PriorityIcon(%q) and Priority(%q) use different colors: %q vs %q", rank, rank, icon, word)
		}
	}
}

// sgrCodes extracts the SGR escape sequences from s, in order — enough
// to compare which color two renderings chose without asserting on the
// specific value, which the theme owns.
func sgrCodes(s string) []string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).FindAllString(s, -1)
}

// TestLabelLeadsWithTagGlyphOnlyUnderNerd pins both halves of Label: the
// tag marker appears when the font can draw it, and NEVER otherwise —
// a terminal without a patched font must not be handed a Private Use
// Area codepoint it would render as tofu.
func TestLabelLeadsWithTagGlyphOnlyUnderNerd(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)

	setGlyphs(t, config.GlyphSetNerd)
	withGlyph := ansi.Strip(Label("agent-dx"))
	if !strings.HasPrefix(withGlyph, labelGlyph) {
		t.Errorf("Label under nerd glyphs = %q, want it led by the tag glyph", withGlyph)
	}

	setGlyphs(t, config.GlyphSetDefault)
	plain := ansi.Strip(Label("agent-dx"))
	if strings.Contains(plain, labelGlyph) {
		t.Errorf("Label under default glyphs = %q, want no Private Use Area glyph", plain)
	}
	if plain != "agent-dx" {
		t.Errorf("Label under default glyphs = %q, want exactly the label", plain)
	}
}

// TestLabelSharesOneColorWithItsEntity pins that a label's marker and
// its text are one object: the glyph takes the label's OWN hashed color,
// so the pair cannot read as two separately-colored things.
func TestLabelSharesOneColorWithItsEntity(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetNerd)

	got := Label("agent-dx")
	if codes := sgrCodes(got); len(codes) != 2 {
		t.Errorf("Label = %q emitted %d SGR codes, want exactly one open and one reset — the glyph and text must be ONE span", got, len(codes))
	}
	if want := sgrCodes(Entity("agent-dx"))[0]; sgrCodes(got)[0] != want {
		t.Errorf("Label's color = %q, want the same color Entity gives it (%q)", sgrCodes(got)[0], want)
	}
}

// TestLabelSanitizesHostileName pins H4 on the glyph path: the marker is
// prepended to a SANITIZED name, so a label carrying escapes cannot ride
// into the terminal alongside it.
func TestLabelSanitizesHostileName(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	setGlyphs(t, config.GlyphSetNerd)

	got := Label("evil\x1b[31m\x07label")
	for _, r := range ansi.Strip(got) {
		if r == 0x1b || r == 0x07 {
			t.Errorf("Label(hostile) = %q, want the escapes stripped", got)
		}
	}
}
