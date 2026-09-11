package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"
	changelog "github.com/matcra587/jira-cli"
	"github.com/matcra587/jira-cli/internal/config"
)

func releaseNotesFixture() ReleaseNotesResult {
	md := "## [0.3.3](https://example/tag/v0.3.3) — 2026-06-03\n\n### Added\n\n- A thing\n"
	return ReleaseNotesResult{
		Markdown: md,
		Releases: []changelog.Release{{
			Version:  "0.3.3",
			Tag:      "v0.3.3",
			Sections: []changelog.Section{{Kind: "Added", Changes: []string{"A thing"}}},
			Markdown: md,
		}},
	}
}

// TestWriteCommandPlainReleaseNotes checks that release.notes routes to the
// Markdown renderer and, on a buffer (no TTY), emits the raw Markdown untouched
// so it can be redirected into a file or a release body.
func TestWriteCommandPlainReleaseNotes(t *testing.T) {
	// No TTY (a buffer) and no theme: the raw Markdown passes through untouched
	// so it can be redirected into a file or a release body.
	var buf bytes.Buffer
	if err := WriteCommandPlain(&buf, "release.notes", releaseNotesFixture()); err != nil {
		t.Fatalf("WriteCommandPlain: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"## [0.3.3]", "### Added", "- A thing"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

// TestWriteCommandPlainReleaseNotesStyled checks that a resolved theme on a TTY
// routes through glamour: the raw "## [" / "### " Markdown markers are consumed
// by the renderer while the heading text and bullet content survive.
func TestWriteCommandPlainReleaseNotesStyled(t *testing.T) {
	// The styled path renders only when color is enabled; go test disables it.
	// Replace the default logger with a fresh one rather than mutating it:
	// SetColorMode rewrites clog.Default() in place, so a captured pointer
	// would restore nothing and the mode would leak into the package
	// globals for whatever test runs next.
	prev := clog.Default()
	t.Cleanup(func() { clog.SetDefault(prev) })
	clog.SetDefault(clog.New(clog.NewOutput(prev.Output().Writer(), clog.ColorAlways)))

	var buf bytes.Buffer
	err := WriteCommandPlain(&buf, "release.notes", releaseNotesFixture(),
		WithPlainTTY(true), WithPlainTheme(config.DefaultTheme()), WithPlainTermWidth(80))
	if err != nil {
		t.Fatalf("styled WriteCommandPlain: %v", err)
	}
	got := buf.String()
	// Glamour may split a bullet's words across styled spans, so assert on
	// individually contiguous tokens rather than the full phrase.
	for _, want := range []string{"Added", "thing", "0.3.3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("styled output missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "### Added") {
		t.Fatalf("glamour should consume the raw '### ' marker:\n%q", got)
	}
}

// TestWriteCommandPlainReleaseNotesWrongType exercises the fallback when the
// payload is not a ReleaseNotesResult.
func TestWriteCommandPlainReleaseNotesWrongType(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommandPlain(&buf, "release.notes", map[string]any{"unexpected": true}); err != nil {
		t.Fatalf("WriteCommandPlain fallback: %v", err)
	}
}
