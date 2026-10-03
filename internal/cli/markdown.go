package cli

import (
	"strings"

	"github.com/gechr/clib/theme"
	"github.com/gechr/primer/render"
	"github.com/gechr/x/ansi"
)

// Markdown is the shared document sink. Unstyled output keeps raw Markdown;
// styled output renders it through Primer using the resolved theme.
// Authored layout survives, while terminal control sequences cannot execute.
func Markdown(markdown string, width int) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		default:
			return r
		}
	}, ansi.Strip(markdown))
	if !StyleEnabled() {
		return clean
	}
	if width <= 0 {
		width = 100
	}
	th := ResolvedTheme()
	base := render.BackgroundDark
	if th.Background == theme.BackgroundLight {
		base = render.BackgroundLight
	}
	style := render.StyleFromPalette(render.MarkdownPalette{
		Base: base, Text: th.MarkdownText.GetForeground(), Heading: th.Blue.GetForeground(),
		H1: th.Magenta.GetForeground(), Link: th.Blue.GetForeground(),
		Code: th.MarkdownCode.GetForeground(), Dim: th.Dim.GetForeground(),
	})
	style.H2.Color = new(render.ColorToken(th.Orange.GetForeground()))
	style.H2.Underline = new(true)
	return render.NewMarkdownRenderer(style).Render("document", width, clean)
}
