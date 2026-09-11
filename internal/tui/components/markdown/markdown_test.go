package markdown

import (
	"strings"
	"testing"

	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	clibtheme "github.com/gechr/clib/theme"

	"github.com/matcra587/jira-cli/internal/config"
)

func testRenderer() *Renderer {
	return NewRenderer(StyleFromTheme(config.DefaultTheme()))
}

func TestRenderWrapsToWidth(t *testing.T) {
	r := testRenderer()
	out := r.Render("JCT-1", 30, strings.Repeat("wrap me please ", 20))
	for line := range strings.SplitSeq(out, "\n") {
		if w := lipgloss.Width(line); w > 30 {
			t.Fatalf("line wider than wrap width: %d > 30 (%q)", w, line)
		}
	}
}

func TestRenderStylesHeading(t *testing.T) {
	r := testRenderer()
	out := r.Render("JCT-1", 60, "## Section\n\nbody text\n")
	if !strings.Contains(out, "Section") {
		t.Fatalf("heading text missing:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("output carries no ANSI styling")
	}
}

func TestRenderIsStableAcrossRepeatCalls(t *testing.T) {
	r := testRenderer()
	first := r.Render("JCT-1", 40, "hello **world**")
	if again := r.Render("JCT-1", 40, "hello **world**"); again != first {
		t.Error("repeat render returned different output")
	}
	// Cache-internal behavior (entry keying, eviction caps) is primer's
	// tested contract; this package only asserts the observable surface.
}

func TestRenderInvalidatesOnContentChange(t *testing.T) {
	r := testRenderer()
	r.Render("JCT-1", 40, "first version")
	out := r.Render("JCT-1", 40, "second version")
	if !strings.Contains(ansi.Strip(out), "second version") {
		t.Errorf("stale cache served after content changed:\n%q", out)
	}
}

func TestRenderClampsWidth(t *testing.T) {
	r := testRenderer()
	if out := r.Render("JCT-1", -3, "tiny"); out == "" {
		t.Error("non-positive width returned empty output")
	}
}

func TestStyleFromThemeUsesPalette(t *testing.T) {
	th := config.DefaultTheme()
	cfg := StyleFromTheme(th)
	if cfg.H2.Color == nil {
		t.Fatal("H2 color not set from theme")
	}
	want := ColorToken(th.Blue.GetForeground())
	if *cfg.H2.Color != want {
		t.Errorf("H2 color = %q, want theme blue %q", *cfg.H2.Color, want)
	}
	if cfg.Document.Margin == nil || *cfg.Document.Margin != 0 {
		t.Error("document margin must be 0 inside TUI panes")
	}
}

func TestColorTokenPreservesANSIIndexes(t *testing.T) {
	// An indexed color must pass through as its index so the user's terminal
	// palette renders it — converting to hex bakes in the standard VGA value
	// and stops matching the lipgloss-rendered chrome around the markdown.
	for in, want := range map[string]string{"4": "4", "212": "212", "#ff00aa": "#ff00aa"} {
		if got := ColorToken(lipgloss.Color(in)); got != want {
			t.Errorf("ColorToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStyleFromThemePicksBaseByBackground(t *testing.T) {
	light := clibtheme.Light()
	dark := clibtheme.Dark()
	lightCfg := StyleFromTheme(light)
	darkCfg := StyleFromTheme(dark)
	wantLight := styles.LightStyleConfig.CodeBlock.Chroma.Text.Color
	wantDark := styles.DarkStyleConfig.CodeBlock.Chroma.Text.Color
	if lightCfg.CodeBlock.Chroma == nil || *lightCfg.CodeBlock.Chroma.Text.Color != *wantLight {
		t.Error("light theme should ride glamour's light base (chroma palette)")
	}
	if darkCfg.CodeBlock.Chroma == nil || *darkCfg.CodeBlock.Chroma.Text.Color != *wantDark {
		t.Error("dark theme should ride glamour's dark base (chroma palette)")
	}
}
