package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"
)

func TestSanitizeTerminalTextStripsControlBytes(t *testing.T) {
	in := "hel\x00lo\x07world\x1b"
	got := SanitizeTerminalText(in)
	if strings.ContainsAny(got, "\x00\x07\x1b") {
		t.Fatalf("control bytes survived sanitization: %q", got)
	}
	if got != "helloworld" {
		t.Fatalf("SanitizeTerminalText = %q, want helloworld", got)
	}
}

func TestSanitizeTerminalTextStripsANSISequences(t *testing.T) {
	in := "\x1b[31mred\x1b[0m"
	got := SanitizeTerminalText(in)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("ANSI escape survived: %q", got)
	}
	if got != "red" {
		t.Fatalf("SanitizeTerminalText = %q, want red", got)
	}
}

// SanitizeTerminalBlock is the multi-line variant used at the plain
// renderer's data boundary: escape sequences and control bytes go, but
// the layout characters a description legitimately carries survive.
func TestSanitizeTerminalBlockPreservesLayout(t *testing.T) {
	in := "line \x1b[31mone\x07\nline\ttwo\r\n"
	got := SanitizeTerminalBlock(in)
	if strings.ContainsAny(got, "\x1b\x07\x00\r") {
		t.Fatalf("control bytes survived block sanitization: %q", got)
	}
	if got != "line one\nline\ttwo\n" {
		t.Fatalf("SanitizeTerminalBlock = %q, want tabs/newlines kept and CRLF normalized", got)
	}
	// A bare carriage return must be dropped: a lone CR returns the cursor
	// to the start of the line, letting Jira text overwrite earlier output.
	if got := SanitizeTerminalBlock("visible\roverwrite"); got != "visibleoverwrite" {
		t.Fatalf("bare CR survived block sanitization: %q", got)
	}
}

// The generic plain renderer is a human-mode output boundary: Jira-
// controlled field keys and values (summaries, descriptions, custom
// field names) must never carry ANSI/control bytes to the terminal.
func TestWritePlainSanitizesJiraControlledText(t *testing.T) {
	var buf bytes.Buffer
	err := WritePlain(&buf, map[string]any{
		"summary":            "own\x1b[31med\x07 text",
		"desc\x1b[0mription": "plain",
	})
	if err != nil {
		t.Fatalf("WritePlain() error = %v", err)
	}
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("control bytes from Jira text reached the plain output:\n%q", got)
	}
	if !strings.Contains(got, "owned") || !strings.Contains(got, "description") {
		t.Fatalf("sanitized output lost printable text:\n%q", got)
	}
}

// The bespoke issue view renderer writes the header, the summary, and
// the flattened ADF description straight to the terminal — all
// Jira-controlled. The WriteCommandPlain data boundary must have
// sanitized them before any renderer runs.
func TestIssueViewPlainSanitizesJiraControlledText(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{
		"issue": map[string]any{
			"key": "PROJ-1",
			"fields": map[string]any{
				"summary": "own\x1b[31med\x07 summary",
				"status":  map[string]any{"name": "To \x1b[35mDo"},
				"description": map[string]any{
					"type":    "doc",
					"version": 1,
					"content": []any{map[string]any{
						"type": "paragraph",
						"content": []any{map[string]any{
							"type": "text",
							"text": "body\x1b]8;;https://evil\x07link\x1b]8;;\x07 text",
						}},
					}},
				},
			},
		},
	}
	if err := WriteCommandPlain(&buf, "issue.view", data); err != nil {
		t.Fatalf("WriteCommandPlain(issue.view) error = %v", err)
	}
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("control bytes from Jira text reached the issue view output:\n%q", got)
	}
	for _, want := range []string{"owned summary", "To Do", "bodylink text"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized issue view lost printable text %q:\n%q", want, got)
		}
	}
}

// The live issue-view path stores a TYPED value under data.issue (a
// *jira.Issue), which the WriteCommandPlain map walk cannot descend into
// — the renderer re-marshals it fresh through mapFromAny. This pins that
// gap: the bespoke renderer's own extraction boundary (stringFromMap /
// issueDescriptionPlain) must sanitize the re-marshaled text. A plain
// map payload would pass even with the boundary removed, so the typed
// shape here is load-bearing.
func TestIssueViewPlainSanitizesTypedPayload(t *testing.T) {
	type textNode struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type contentNode struct {
		Type    string     `json:"type"`
		Content []textNode `json:"content"`
	}
	type adfDoc struct {
		Type    string        `json:"type"`
		Version int           `json:"version"`
		Content []contentNode `json:"content"`
	}
	type named struct {
		Name string `json:"name"`
	}
	type issueFields struct {
		Summary     string `json:"summary"`
		Status      named  `json:"status"`
		Description adfDoc `json:"description"`
	}
	type typedIssue struct {
		Key    string      `json:"key"`
		Fields issueFields `json:"fields"`
	}
	var buf bytes.Buffer
	// data.issue is a typed struct, not a map — sanitizePlainData's map
	// walk skips it, so coverage rides entirely on the renderer boundary.
	data := map[string]any{
		"issue": typedIssue{
			Key: "PROJ-1",
			Fields: issueFields{
				Summary: "own\x1b[31med\x07 summary",
				Status:  named{Name: "To \x1b[35mDo"},
				Description: adfDoc{
					Type: "doc", Version: 1,
					Content: []contentNode{{
						Type: "paragraph",
						Content: []textNode{{
							Type: "text",
							Text: "body\x1b]8;;https://evil\x07link\x1b]8;;\x07 text",
						}},
					}},
				},
			},
		},
	}
	if err := WriteCommandPlain(&buf, "issue.view", data); err != nil {
		t.Fatalf("WriteCommandPlain(issue.view) error = %v", err)
	}
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("control bytes from a typed issue payload reached the output:\n%q", got)
	}
	for _, want := range []string{"owned summary", "To Do", "bodylink text"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized typed issue view lost printable text %q:\n%q", want, got)
		}
	}
}

// The issue list table builds its cells from Jira row fields (summary,
// status, assignee) — the boundary plus formatHumanField must strip
// injected bytes from every column.
func TestIssueListPlainSanitizesJiraControlledText(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{
		"issues": []any{map[string]any{
			"key":             "PROJ-1",
			"summary":         "own\x1b[31med\x07 summary",
			"status":          "To \x1b[35mDo",
			"status_category": "new",
			"assignee":        map[string]any{"display_name": "Ma\x1b[7mllory"},
			"priority":        "High\x07",
			"updated":         "2026-07-07T00:00:00Z",
		}},
		"detail": false,
	}
	if err := WriteCommandPlain(&buf, "issue.list", data); err != nil {
		t.Fatalf("WriteCommandPlain(issue.list) error = %v", err)
	}
	got := buf.String()
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("control bytes from Jira text reached the issue list output:\n%q", got)
	}
	for _, want := range []string{"owned summary", "To Do", "Mallory", "High"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized issue list lost printable text %q:\n%q", want, got)
		}
	}
}

func TestSanitizeCompletionFieldRemovesTabAndNewline(t *testing.T) {
	got := SanitizeCompletionField("multi\nline\tvalue")
	if strings.ContainsAny(got, "\t\n\r") {
		t.Fatalf("tab/newline survived: %q", got)
	}
	if got != "multi line value" {
		t.Fatalf("SanitizeCompletionField = %q, want \"multi line value\"", got)
	}
}

// Hyperlink construction must sanitize the inner text so a Jira-supplied
// control byte cannot break the OSC 8 span open/close pair.
func TestHyperlinkSanitizesInnerText(t *testing.T) {
	got := Hyperlink("https://example.com", "te\x1b]8;;evilxt")
	if strings.Contains(got, "evil") && strings.Count(got, "\x1b]8;;") > 2 {
		t.Fatalf("injected OSC 8 span survived: %q", got)
	}
}

func TestHyperlinkHonorsClogHyperlinkDisabled(t *testing.T) {
	formats := clog.Default().FieldFormats()
	disabled := formats
	disabled.HyperlinkEnabled = false
	clog.SetFieldFormats(disabled)
	t.Cleanup(func() { clog.SetFieldFormats(formats) })

	got := Hyperlink("https://example.com", "example")
	if strings.Contains(got, "\x1b]8;;") {
		t.Fatalf("hyperlink emitted OSC 8 escape while disabled: %q", got)
	}
	if got != "example" {
		t.Fatalf("Hyperlink = %q, want example", got)
	}
}
