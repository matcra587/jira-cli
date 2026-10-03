package cli

import (
	"strings"
	"testing"

	"github.com/gechr/x/ansi"
)

func TestMarkdownSinkPreservesLayoutAndRemovesTerminalControls(t *testing.T) {
	before := StyleEnabled()
	SetStyleEnabled(false)
	t.Cleanup(func() { SetStyleEnabled(before) })
	const hostile = "# Header\n\n\tindented **Markdown** & <text>\n\x1b[31mred\x1b[0m\a\r\x7f\u0085"
	const want = "# Header\n\n\tindented **Markdown** & <text>\nred"
	if got := Markdown(hostile, 20); got != want {
		t.Fatalf("document lost authored layout or retained controls: %q", got)
	}
}

func TestMarkdownSinkUsesResolvedStyleAndPreservesLongText(t *testing.T) {
	before := StyleEnabled()
	SetStyleEnabled(true)
	t.Cleanup(func() { SetStyleEnabled(before) })
	const body = "# Heading\n\nA **strong** phrase with `code` and [link](https://example.test)."
	for _, width := range []int{0, 25} {
		got := Markdown(body, width)
		plain := ansi.Strip(got)
		if !strings.Contains(got, "\x1b[") || !strings.Contains(plain, "Heading") || !strings.Contains(plain, "strong") || !strings.Contains(plain, "example.test") {
			t.Fatalf("styled Markdown lost content or style at width%d: %q", width, got)
		}
	}
}
