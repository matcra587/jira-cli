package cli

import (
	"bytes"
	"testing"

	"github.com/gechr/clog"
)

// TestResolveColorMode pins the fold: ColorAlways and ColorNever are
// absolute regardless of the writer or environment; ColorAuto styles
// only a real terminal that NO_COLOR or TERM=dumb has not silenced. A
// plain bytes.Buffer is never a TTY, so it stands in for "not a
// terminal" without needing a real one — the positive "auto + real TTY"
// case cannot be proven from a unit test (clog/core.md: "color-enabled
// output only for tests that specifically cover styling"), which is
// exactly why ColorAlways exists as the forced-on escape hatch.
func TestResolveColorMode(t *testing.T) {
	t.Run("always styles even off a TTY and with nothing cleared", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		var buf bytes.Buffer
		if !ResolveColorMode(&buf, clog.ColorAlways) {
			t.Error("ColorAlways did not style a non-TTY writer")
		}
	})

	t.Run("never styles even with nothing suppressing it", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		var buf bytes.Buffer
		if ResolveColorMode(&buf, clog.ColorNever) {
			t.Error("ColorNever styled")
		}
	})

	t.Run("auto does not style a non-TTY writer", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		var buf bytes.Buffer
		if ResolveColorMode(&buf, clog.ColorAuto) {
			t.Error("ColorAuto styled a bytes.Buffer, which is never a TTY")
		}
	})

	t.Run("NO_COLOR suppresses auto", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		t.Setenv("NO_COLOR", "1")
		var buf bytes.Buffer
		if ResolveColorMode(&buf, clog.ColorAuto) {
			t.Error("NO_COLOR did not suppress ColorAuto")
		}
	})

	t.Run("NO_COLOR suppresses auto even when empty (no-color.org: any value counts)", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		t.Setenv("NO_COLOR", "")
		var buf bytes.Buffer
		if ResolveColorMode(&buf, clog.ColorAuto) {
			t.Error("an empty NO_COLOR did not suppress ColorAuto")
		}
	})

	t.Run("TERM=dumb suppresses auto", func(t *testing.T) {
		clearEnv(t, "NO_COLOR", "TERM")
		t.Setenv("TERM", "dumb")
		var buf bytes.Buffer
		if ResolveColorMode(&buf, clog.ColorAuto) {
			t.Error("TERM=dumb did not suppress ColorAuto")
		}
	})
}

// TestResolvedColorModeAndStyleEnabledPublish pins the publish/read
// pair root calls once per invocation: whatever is set is what a later
// reader (the View's constructor, the pure value sinks) gets back.
func TestResolvedColorModeAndStyleEnabledPublish(t *testing.T) {
	t.Cleanup(func() {
		SetResolvedColorMode(clog.ColorAuto)
		SetStyleEnabled(false)
	})

	SetResolvedColorMode(clog.ColorAlways)
	if got := ResolvedColorMode(); got != clog.ColorAlways {
		t.Errorf("ResolvedColorMode() = %v, want ColorAlways", got)
	}

	SetStyleEnabled(true)
	if !StyleEnabled() {
		t.Error("StyleEnabled() = false after SetStyleEnabled(true)")
	}

	SetStyleEnabled(false)
	if StyleEnabled() {
		t.Error("StyleEnabled() = true after SetStyleEnabled(false)")
	}
}
