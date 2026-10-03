package cli

import (
	"io"
	"os"
	"sync"

	"github.com/gechr/clog"
)

// v16 has exactly one human stdout surface per invocation — the View
// piece of the output-surface design builds it — and nothing resolves a
// color decision for it yet. conductor's ApplyFlags folds --color onto
// clog.Default, but that governs only STDERR diagnostics; a fresh
// clog.Output built for stdout (what View wraps) does not read that
// state. The two variables below are the missing resolution, published
// once by root after flags parse — mirroring how ResolvedTheme (theme.go)
// is resolved once at the lowest layer every command can reach, rather
// than threaded through every signature that might need it.
var (
	colorMu sync.RWMutex
	// resolvedColorMode is the raw --color decision, so the human View's
	// own clog.Output is constructed with the SAME mode this package
	// resolved for the same writer — the two must never disagree about
	// styling one stream.
	resolvedColorMode = clog.ColorAuto
	// stylingEnabled is the FOLDED decision the pure value sinks (Status,
	// Priority, Entity, ...) read. Its zero value is false: no color is
	// the safe unresolved state, because a sink that somehow ran before
	// root resolved anything (a wiring bug — root always resolves this
	// before a renderer runs) must never emit ANSI nobody asked for.
	stylingEnabled bool
)

// SetResolvedColorMode publishes the raw --color decision.
func SetResolvedColorMode(mode clog.ColorMode) {
	colorMu.Lock()
	defer colorMu.Unlock()
	resolvedColorMode = mode
}

// ResolvedColorMode reports the published --color decision — the mode a
// caller building the human View's clog.Output should pass to
// clog.NewOutput so the two color decisions for one writer agree.
func ResolvedColorMode() clog.ColorMode {
	colorMu.RLock()
	defer colorMu.RUnlock()
	return resolvedColorMode
}

// SetStyleEnabled publishes ResolveColorMode's answer for the pure value
// sinks.
func SetStyleEnabled(enabled bool) {
	colorMu.Lock()
	defer colorMu.Unlock()
	stylingEnabled = enabled
}

// StyleEnabled reports whether the human stdout surface renders in
// color for this invocation.
//
// The pure value sinks (Status, Priority, Entity) render with lipgloss
// directly — no writer, no clog.Output, nowhere per call to ask "should
// THIS string be styled" — so root resolves the question once, for the
// one stdout surface a single invocation has, and they read the answer
// from here instead of taking a parameter each.
func StyleEnabled() bool {
	colorMu.RLock()
	defer colorMu.RUnlock()
	return stylingEnabled
}

// ResolveColorMode folds requested — the parsed --color flag — over w's
// own TTY state into whether the human stdout surface should style at
// all: ColorAlways always does, ColorNever never does, and ColorAuto
// styles only a real terminal that NO_COLOR or TERM=dumb has not
// silenced.
//
// clog.NewOutput already detects a writer's TTY state for any io.Writer
// (not just *os.File, which x/terminal.Is requires), so probing through
// it — rather than duplicating Fd() detection — is the gechr-first way
// to ask "is w a terminal" for whatever cobra hands root as
// OutOrStdout.
//
// A pure function, deliberately: it takes the writer as an argument
// instead of reading os.Stdout, so a test proves the fold against an
// explicit writer rather than the developer's own terminal. Root is the
// one production caller, publishing the answer via SetStyleEnabled
// right after parsing --color.
func ResolveColorMode(w io.Writer, requested clog.ColorMode) bool {
	switch requested {
	case clog.ColorAlways:
		return true
	case clog.ColorNever:
		return false
	default:
		return clog.NewOutput(w, clog.ColorAuto).IsTTY() && !envSuppressesColor()
	}
}

// envSuppressesColor mirrors the two signals clog's own Logger-bound
// auto-detection treats as color-off for a TTY writer: NO_COLOR (any
// value, including empty, per https://no-color.org/) and TERM=dumb.
// clog applies both internally whenever it renders through a real
// Logger/Output, but the pure value sinks style text with lipgloss
// directly — no Output in reach to ask — so ResolveColorMode mirrors
// the same two checks for that path.
func envSuppressesColor() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return true
	}
	return os.Getenv("TERM") == "dumb"
}
