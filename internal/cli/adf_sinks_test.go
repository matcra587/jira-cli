package cli

import (
	"strings"
	"testing"

	"github.com/gechr/x/ansi"
)

func TestADFStatusUsesSemanticBadgeAndRejectsInvalidHues(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, true)
	for _, hue := range []string{"neutral", "purple", "blue", "red", "yellow", "green", "#aA55Cc"} {
		got := ADFStatus("Ready", hue)
		if !strings.Contains(got, "\x1b[") || strings.Trim(strings.TrimSpace(ansi.Strip(got)), pillCapLeft+pillCapRight) != "Ready" {
			t.Fatalf("hue %q did not draw a filled badge: %q", hue, got)
		}
	}
	for _, hue := range []string{"#abc", "#aa55cc00", "123", "future", "#zz55cc"} {
		if got := ADFStatus("Ready\x1b\a", hue); got != "Ready" {
			t.Fatalf("invalid hue/control fallback %q: %q", hue, got)
		}
	}
}

func TestADFStatusPlainModeSanitizesNativeLabel(t *testing.T) {
	deterministicTheme(t)
	withStyle(t, false)
	if got := ADFStatus("Ready\x1b\a", "#aa55cc"); got != "Ready" {
		t.Fatalf("plain status leaked styling or controls: %q", got)
	}
}
