package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// A Markdown body that could not survive the trip to ADF must say so in
// the ENVELOPE, and this file asserts that end rather than the machinery
// behind it.
//
// The distinction is the whole reason the guard is written this way.
// command.encodeMarkdown computed these warnings, mapped them onto
// envelope.Warning and stored them on Body.Lossy for the whole of the
// Markdown port — and nothing read the field, so every body-taking write
// (`comment add`, `comment edit`, `transition --comment`, `create`,
// `edit`) posted mangled content silently. A test reading Body.Lossy
// would have been green throughout. Only warnings[] on the real binary's
// real output distinguishes "computed" from "published".

// lossyWarning mirrors one envelope warning. Lossy is a pointer so an
// absent field is distinguishable from a present false — the bit is the
// contract here, and defaulting it would let a producer that stopped
// emitting it pass.
type lossyWarning struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Lossy   *bool  `json:"lossy"`
}

// markdownLossyConversion is the taxonomy code internal/adf stamps on
// every Markdown conversion diagnostic, dropped or downgraded alike.
const markdownLossyConversion = "markdown_lossy_conversion"

// TestMarkdownBodyLossPublishesEnvelopeWarnings pins the published end of
// a lossy Markdown body: a preview that changed the author's content is a
// SUCCESS carrying warnings, not a refusal, and each warning reports
// whether content was lost or merely reshaped.
//
// The two constructs are chosen to cover both of the converter's warning
// kinds in one invocation, because collapsing them is the failure that
// actually happened: a raw HTML block is DROPPED (lossy: true), while an
// image survives as a link (lossy: false). A guard using only one would
// pass against a producer that hard-coded the bit.
//
// --dry-run keeps this local: no client is built and nothing reaches a
// tenant, so the warning is proven on the composition path every body
// route shares.
func TestMarkdownBodyLossPublishesEnvelopeWarnings(t *testing.T) {
	t.Parallel()
	const markdown = "<div>dropped</div>\n\nand ![diagram](http://example.test/d.png)"

	stdout, stderr, exit := runBinaryEnv(t,
		[]string{"JIRA_BASE_URL=http://127.0.0.1:9"},
		"issue", "comment", "add", "KEY-123",
		"--markdown", markdown, "--dry-run", "--output=json")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 — a lossy body warns, it does not refuse (stderr: %q)", exit, stderr)
	}

	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, stdout)
	}
	if !f.OK {
		t.Error("ok = false, want a lossy conversion to succeed")
	}
	if len(f.Warnings) == 0 {
		t.Fatalf("warnings = [], want the Markdown conversion's losses published — they are computed on Body.Lossy and this is the only thing that proves anything reads them.\nstdout: %q", stdout)
	}

	warnings := make([]lossyWarning, 0, len(f.Warnings))
	for i, raw := range f.Warnings {
		var w lossyWarning
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatalf("warnings[%d] does not decode: %v", i, err)
		}
		if w.Type != markdownLossyConversion {
			t.Errorf("warnings[%d].type = %q, want %q", i, w.Type, markdownLossyConversion)
		}
		if w.Lossy == nil {
			t.Errorf("warnings[%d] has no lossy field — it is serialized even when false so a consumer branches without a presence check", i)
		}
		warnings = append(warnings, w)
	}

	assertWarning(t, warnings, "raw HTML", true)
	assertWarning(t, warnings, "image", false)
}

// assertWarning finds the one warning naming construct and checks its
// lossy bit. Matched on the construct rather than by index because the
// order is the converter's business, not this contract's.
func assertWarning(t *testing.T, warnings []lossyWarning, construct string, wantLossy bool) {
	t.Helper()
	for _, w := range warnings {
		if !strings.Contains(w.Message, construct) {
			continue
		}
		if w.Lossy == nil {
			t.Errorf("warning for %s has no lossy field, want %v (%q)", construct, wantLossy, w.Message)
			return
		}
		if *w.Lossy != wantLossy {
			t.Errorf("warning for %s has lossy = %v, want %v — dropped content and reshaped content must stay distinguishable (%q)",
				construct, *w.Lossy, wantLossy, w.Message)
		}
		return
	}
	got := make([]string, 0, len(warnings))
	for _, w := range warnings {
		got = append(got, w.Message)
	}
	t.Errorf("no warning naming %q; got %q", construct, got)
}
