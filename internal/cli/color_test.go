package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/gechr/clog"
)

// withResolvedColorMode swaps the process-wide resolved --color mode for the
// duration of a test and restores it afterward. The mode is a package global
// (mirroring clog.Default()), so a test that sets it must not run in parallel
// with siblings that read it.
func withResolvedColorMode(t *testing.T, mode clog.ColorMode) {
	t.Helper()
	prev := ResolvedColorMode()
	t.Cleanup(func() { SetResolvedColorMode(prev) })
	SetResolvedColorMode(mode)
}

func TestStyleEnabledFoldsColorModeOverTTY(t *testing.T) {
	cases := []struct {
		name string
		mode clog.ColorMode
		tty  bool
		want bool
	}{
		{name: "always forces styling off a tty", mode: clog.ColorAlways, tty: false, want: true},
		{name: "always keeps styling on a tty", mode: clog.ColorAlways, tty: true, want: true},
		{name: "never suppresses styling on a tty", mode: clog.ColorNever, tty: true, want: false},
		{name: "never suppresses styling off a tty", mode: clog.ColorNever, tty: false, want: false},
		{name: "auto defers to tty when true", mode: clog.ColorAuto, tty: true, want: true},
		{name: "auto defers to tty when false", mode: clog.ColorAuto, tty: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withResolvedColorMode(t, tc.mode)
			// The auto rows read the environment; pin it so an ambient
			// NO_COLOR or TERM=dumb cannot flip the expectations.
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("NO_COLOR", "sentinel") // registers restore of the caller value
			_ = os.Unsetenv("NO_COLOR")
			if got := StyleEnabled(tc.tty); got != tc.want {
				t.Fatalf("StyleEnabled(%v) with mode %v = %v, want %v", tc.tty, tc.mode, got, tc.want)
			}
		})
	}
}

// newPlainLogger takes the resolved --color mode, so a mutation success line's
// issue-key link honors --color even though this stdout logger is not
// clog.Default(). never renders plain text; always renders an OSC 8 link even
// though the writer is a non-TTY buffer.
func TestNewPlainLoggerHonorsResolvedColorMode(t *testing.T) {
	cfg := defaultPlainConfig()
	cfg.baseURL = "https://example.atlassian.net"
	data := map[string]any{"issue": "PROJ-123"}
	const link = "\x1b]8;;https://example.atlassian.net/browse/PROJ-123\x1b\\"

	t.Run("never stays plain", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorNever)
		var buf bytes.Buffer
		if err := writeGenericPlain(newPlainLogger(&buf), cfg, "Edited issue", data); err != nil {
			t.Fatalf("writeGenericPlain: %v", err)
		}
		if strings.Contains(buf.String(), "\x1b") {
			t.Fatalf("never must emit no escape bytes, got %q", buf.String())
		}
		if !strings.Contains(buf.String(), "PROJ-123") {
			t.Fatalf("key must survive as plain text, got %q", buf.String())
		}
	})

	t.Run("always links off a tty", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorAlways)
		var buf bytes.Buffer
		if err := writeGenericPlain(newPlainLogger(&buf), cfg, "Edited issue", data); err != nil {
			t.Fatalf("writeGenericPlain: %v", err)
		}
		if !strings.Contains(buf.String(), link) {
			t.Fatalf("always must link the key even off a tty, got %q", buf.String())
		}
	})
}

// WriteHumanJSON's syntax highlighting follows the resolved --color mode: never
// strips it, always forces it even when stdout is a non-TTY buffer.
func TestWriteHumanJSONHonorsResolvedColorMode(t *testing.T) {
	data := map[string]any{"ok": true, "value": "hello"}

	t.Run("never strips highlighting", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorNever)
		var buf bytes.Buffer
		if err := WriteHumanJSON(&buf, data, nil); err != nil {
			t.Fatalf("WriteHumanJSON: %v", err)
		}
		if strings.Contains(buf.String(), "\x1b") {
			t.Fatalf("never must emit no escape bytes, got %q", buf.String())
		}
	})

	t.Run("always forces highlighting off a tty", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorAlways)
		var buf bytes.Buffer
		if err := WriteHumanJSON(&buf, data, nil); err != nil {
			t.Fatalf("WriteHumanJSON: %v", err)
		}
		if !strings.Contains(buf.String(), "\x1b") {
			t.Fatalf("always must syntax-highlight even off a tty, got %q", buf.String())
		}
	})
}

// The warning-mirror logger carries the plain-renderer styles, so a backticked
// Jira warning keeps its literal `code` delimiters instead of clog's default
// backtick styling dropping them.
func TestMirrorWarningsKeepsBacktickDelimiters(t *testing.T) {
	withResolvedColorMode(t, clog.ColorAlways)
	var buf bytes.Buffer
	warnings := []Warning{{Type: "lossy_adf_conversion", Message: "dropped `panel` node", Lossy: true}}
	if err := mirrorWarningsToStderr(&buf, warnings); err != nil {
		t.Fatalf("mirrorWarningsToStderr: %v", err)
	}
	if !strings.Contains(buf.String(), "`panel`") {
		t.Fatalf("backtick delimiters must survive, got %q", buf.String())
	}
}

// The environment can veto ColorAuto styling the way clog's own auto
// detection does: NO_COLOR presence (any value, even empty) and TERM=dumb
// suppress it, and --color=always overrides both.
func TestStyleEnabledHonorsColorSuppressingEnv(t *testing.T) {
	t.Run("NO_COLOR suppresses auto on a tty", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorAuto)
		t.Setenv("NO_COLOR", "")
		if StyleEnabled(true) {
			t.Fatal("auto with NO_COLOR set must not style, even on a TTY")
		}
	})
	t.Run("TERM=dumb suppresses auto on a tty", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorAuto)
		t.Setenv("NO_COLOR", "sentinel")
		_ = os.Unsetenv("NO_COLOR")
		t.Setenv("TERM", "dumb")
		if StyleEnabled(true) {
			t.Fatal("auto with TERM=dumb must not style")
		}
	})
	t.Run("always overrides NO_COLOR and non-tty state", func(t *testing.T) {
		withResolvedColorMode(t, clog.ColorAlways)
		t.Setenv("NO_COLOR", "")
		if !StyleEnabled(false) {
			t.Fatal("always must override NO_COLOR and non-TTY state")
		}
	})
}
