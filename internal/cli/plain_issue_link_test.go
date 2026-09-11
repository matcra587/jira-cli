package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"
)

// Mutation success lines make issue keys clickable: a field whose value is
// a bare issue key renders as an OSC 8 link to its browse URL when the
// output supports color, and degrades to plain text off a TTY — clog owns
// the fallback, not the renderer.

func linkLogger(buf *bytes.Buffer, mode clog.ColorMode) *clog.Logger {
	logger := newPlainLogger(buf)
	logger.SetColorMode(mode)
	// SetColorMode replaces the output, so restore the logger's link policy.
	logger.SetFieldFormats(logger.FieldFormats())
	return logger
}

func TestGenericPlainLinksIssueKeys(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultPlainConfig()
	cfg.baseURL = "https://example.atlassian.net"
	err := writeGenericPlain(linkLogger(&buf, clog.ColorAlways), cfg, "Edited issue", map[string]any{
		"issue": "PROJ-123",
	})
	if err != nil {
		t.Fatalf("writeGenericPlain: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\x1b]8;;https://example.atlassian.net/browse/PROJ-123\x1b\\") {
		t.Fatalf("issue key must link to its browse URL, got %q", got)
	}
	if !strings.Contains(got, "PROJ-123") {
		t.Fatalf("display text must remain the key, got %q", got)
	}
}

func TestGenericPlainKeyLinkDegradesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	cfg := defaultPlainConfig()
	cfg.baseURL = "https://example.atlassian.net"
	err := writeGenericPlain(linkLogger(&buf, clog.ColorNever), cfg, "Edited issue", map[string]any{
		"issue": "PROJ-123",
	})
	if err != nil {
		t.Fatalf("writeGenericPlain: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "https://") {
		t.Fatalf("plain issue key must not expand into a URL: %q", got)
	}
	if strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("no OSC 8 escapes without color support, got %q", got)
	}
	if !strings.Contains(got, "PROJ-123") {
		t.Fatalf("key must survive as plain text, got %q", got)
	}
}

func TestGenericPlainLeavesNonKeysAndNoBaseURLAlone(t *testing.T) {
	var buf bytes.Buffer
	// No base URL: even a key-shaped value stays a plain field.
	err := writeGenericPlain(linkLogger(&buf, clog.ColorAlways), defaultPlainConfig(), "Edited issue", map[string]any{
		"issue":   "PROJ-123",
		"summary": "not A-1 key",
	})
	if err != nil {
		t.Fatalf("writeGenericPlain: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b]8;;") {
		t.Fatalf("no browse URL known — nothing to link, got %q", buf.String())
	}
}

func TestUserSearchPlainRendersOneLinePerMatch(t *testing.T) {
	var buf bytes.Buffer
	err := writeUserSearchPlain(linkLogger(&buf, clog.ColorNever), map[string]any{
		"query": "sam",
		"count": 2,
		"users": []any{
			map[string]any{"account_id": "id-1", "display_name": "Sam One", "email_address": "one@example.com"},
			map[string]any{"account_id": "id-2", "display_name": "Sam Two", "email_address": "two@example.com"},
		},
	}, defaultPlainConfig())
	if err != nil {
		t.Fatalf("writeUserSearchPlain: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Sam One — one@example.com", "Sam Two — two@example.com", "id-1", "id-2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain user search missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "items") {
		t.Fatalf("users must render as lines, never a collapsed count: %s", got)
	}
}

func TestUserSearchPlainZeroMatches(t *testing.T) {
	var buf bytes.Buffer
	err := writeUserSearchPlain(linkLogger(&buf, clog.ColorNever), map[string]any{
		"query": "nobody", "count": 0, "users": []any{},
	}, defaultPlainConfig())
	if err != nil {
		t.Fatalf("writeUserSearchPlain: %v", err)
	}
	if !strings.Contains(buf.String(), "No users matched") {
		t.Fatalf("zero matches must say so plainly: %s", buf.String())
	}
}
