package adf

import (
	"regexp"
	"strings"

	"github.com/gechr/clog"
	"github.com/gechr/clog/field/hyperlink"
)

// RenderOptions configure RenderActivatable.
type RenderOptions struct {
	// IsTerminal toggles OSC 8 hyperlink emission. When false, the
	// renderer emits plain text.
	IsTerminal bool
	// BaseURL is the active profile's Jira base URL, used to expand
	// issue keys (JCT-7) into browseable links.
	BaseURL string
}

// RenderActivatable renders an ADF document as terminal text with issue
// keys and bare URLs marked as OSC 8 hyperlinks. When IsTerminal is
// false, the same call returns plain text without escapes.
func RenderActivatable(doc Document, opts RenderOptions) string {
	plain := ToPlain(doc)
	if !opts.IsTerminal {
		return plain
	}
	return activate(plain, opts.BaseURL)
}

// issueKeyPattern matches Jira issue keys: 1+ uppercase letters, dash,
// digits. Conservative enough to avoid false positives like "PRE-2026".
var issueKeyPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9_]+)-(\d+)\b`)

// urlPattern matches bare http/https URLs.
var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

// activate wraps every issue-key and bare URL with an OSC 8 hyperlink
// escape. Modern terminals render the visible text as a clickable link
// to the URL; older terminals strip the escapes and show only the text.
//
// The plain text is stripped of C0/C1 control bytes first: a
// Jira-controlled string could carry a bare ESC or BEL that would
// corrupt the terminal or break an OSC 8 span.
func activate(text, baseURL string) string {
	text = stripControlBytes(text)
	// URLs first so issue-key matches inside URLs don't double-wrap.
	text = urlPattern.ReplaceAllStringFunc(text, func(m string) string {
		return osc8(m, m)
	})
	// Issue keys.
	text = issueKeyPattern.ReplaceAllStringFunc(text, func(m string) string {
		base := strings.TrimRight(baseURL, "/")
		if base == "" {
			return m
		}
		return osc8(base+"/browse/"+m, m)
	})
	return text
}

// Hyperlink wraps text in an OSC 8 escape pointing at url, for UI chrome
// (issue keys, breadcrumbs) — terminals that support it make the text
// clickable; others show it unchanged. Shares osc8's control-byte hygiene.
func Hyperlink(url, text string) string { return osc8(url, text) }

// osc8 returns the OSC 8 hyperlink escape for url displayed as text.
// Both halves are stripped of control bytes so a control byte cannot
// break the span open/close pair.
func osc8(url, text string) string {
	url = stripControlBytes(url)
	text = stripControlBytes(text)
	// Honor the user-level hyperlink switch on the default logger, then
	// emit through clog's primitive — no logger construction per link.
	// Emission is deliberately TTY-independent; callers gate on TTY.
	if !clog.Default().FieldFormats().HyperlinkEnabled {
		return text
	}
	return hyperlink.OSC8(url, text)
}

// stripControlBytes drops C0 and C1 control characters. Tab, newline and
// carriage return are kept because ToPlain output uses them as legitimate
// layout; only the corrupting control bytes (ESC, BEL, NUL, etc.) go.
func stripControlBytes(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
