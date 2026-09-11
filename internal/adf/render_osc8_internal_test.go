package adf

import (
	"strings"
	"testing"

	"github.com/gechr/clog"
)

// A Jira-controlled string carrying C0/C1 control bytes (NUL, BEL, a
// bare ESC) must be stripped before it reaches the terminal — those
// bytes corrupt the terminal or break an OSC 8 hyperlink span. Tab,
// newline and carriage return are legitimate layout and survive.
func TestStripControlBytesDropsCorruptingBytes(t *testing.T) {
	got := stripControlBytes("a\x00b\x07c\x1bd\te\nf")
	if strings.ContainsAny(got, "\x00\x07\x1b") {
		t.Fatalf("control bytes survived: %q", got)
	}
	if got != "abcd\te\nf" {
		t.Fatalf("stripControlBytes = %q, want %q", got, "abcd\te\nf")
	}
}

// osc8 must sanitize both the URL and the display text so a control
// byte cannot break the span open/close pair or smuggle a second link.
func TestOSC8SanitizesURLAndText(t *testing.T) {
	got := osc8("https://x\x07.test", "li\x1bnk")
	if strings.Contains(got, "x\x07.test") {
		t.Fatalf("BEL survived in osc8 url payload: %q", got)
	}
	if strings.Contains(got, "li\x1bnk") {
		t.Fatalf("ESC survived in osc8 text payload: %q", got)
	}
	// Exactly two OSC 8 introducers: the open and the close.
	if strings.Count(got, "\x1b]8;;") != 2 {
		t.Fatalf("osc8 span corrupted, want 2 introducers: %q", got)
	}
}

func TestOSC8HonorsClogHyperlinkDisabled(t *testing.T) {
	formats := clog.Default().FieldFormats()
	disabled := formats
	disabled.HyperlinkEnabled = false
	clog.SetFieldFormats(disabled)
	t.Cleanup(func() { clog.SetFieldFormats(formats) })

	got := osc8("https://example.com", "example")
	if strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("osc8 emitted OSC 8 escape while disabled: %q", got)
	}
	if got != "example" {
		t.Fatalf("osc8 = %q, want example", got)
	}
}
