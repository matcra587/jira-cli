package cli

import (
	"strings"

	"github.com/gechr/clog"
	clhyperlink "github.com/gechr/clog/field/hyperlink"
	termansi "github.com/gechr/x/ansi"
)

// SanitizeTerminalText makes a string safe to write to a terminal at an
// output boundary. It first strips any ANSI escape sequences, then drops
// the remaining C0/C1 control bytes (including bare ESC, BEL and NUL)
// that Jira-controlled text could carry to corrupt the terminal or an
// OSC 8 hyperlink span. Printable text — including non-ASCII runes — is
// preserved unchanged.
func SanitizeTerminalText(s string) string {
	if s == "" {
		return s
	}
	s = termansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControlRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SanitizeTerminalBlock makes multi-line display text safe for a
// terminal: ANSI escape sequences are stripped and control bytes
// dropped exactly as in SanitizeTerminalText, but tab and newline
// survive so legitimate multi-line Jira content (descriptions, comment
// bodies) keeps its layout. A CRLF is normalized to a newline and any
// bare carriage return is dropped, so Jira text cannot use a lone CR to
// overwrite earlier output on the line.
func SanitizeTerminalBlock(s string) string {
	if s == "" {
		return s
	}
	s = termansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r != '\t' && r != '\n' && isControlRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SanitizeCompletionField makes a single shell-completion candidate
// field safe. Shell completion is one tab-separated record per line, so
// an embedded tab, newline or carriage return in a candidate field would
// corrupt the grammar; those are collapsed to single spaces. Control
// bytes are stripped as in SanitizeTerminalText.
func SanitizeCompletionField(s string) string {
	if s == "" {
		return s
	}
	s = termansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case isControlRune(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Hyperlink builds an OSC 8 terminal hyperlink for url displayed as
// text. Both the URL and the display text are sanitized first so a
// control byte in Jira-supplied text cannot break the span open/close
// pair or smuggle a second hyperlink.
func Hyperlink(url, text string) string {
	url = SanitizeTerminalText(url)
	text = SanitizeTerminalText(text)
	// Honor the user-level hyperlink switch on the default logger, then
	// emit through clog's primitive — no logger construction per link.
	// Emission is deliberately TTY-independent; callers gate on TTY.
	return HyperlinkPreStyled(url, text)
}

// HyperlinkPreStyled links display text that already carries ANSI styling
// (which sanitization would strip). Callers sanitize the raw content BEFORE
// styling; the URL must already be sanitized too. Honors the user-level
// hyperlink switch on the default logger and emits through clog's
// primitive — no logger construction per link.
func HyperlinkPreStyled(url, styledText string) string {
	if !clog.Default().FieldFormats().HyperlinkEnabled {
		return styledText
	}
	return clhyperlink.OSC8(url, styledText)
}

// isControlRune reports whether r is a C0 or C1 control character (the
// bytes that corrupt terminals and protocols). Tab, newline and
// carriage return are control characters too — callers that need them
// preserved must special-case those before calling this.
func isControlRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}
