package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/output"
)

// Kills a dependency-default change that replaces document link text with
// its URL, or sends OSC 8 escapes to a pipe without an explicit color request.
func TestViewPreservesLinkLabel(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode clog.ColorMode
		want string
	}{
		{name: "color disabled", mode: clog.ColorNever, want: "a link\n"},
		{name: "color forced", mode: clog.ColorAlways, want: "\x1b]8;;https://example.com\x1b\\a link\x1b]8;;\x1b\\\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			v := output.NewView(&buf, tt.mode)
			v.Line(v.Output().Hyperlink("https://example.com", "a link"))
			if got := buf.String(); got != tt.want {
				t.Errorf("link output = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestViewFieldsWritesLabelColonValue pins the plain, non-TTY shape: a
// bytes.Buffer is never a terminal, so Output.Width() is 0 and a single
// Field writes one unwrapped "Label: value" line, aligned to its own
// label width.
func TestViewFieldsWritesLabelColonValue(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Fields(output.Field{Label: "Status", Value: "In Progress"})

	got := buf.String()
	want := "Status:  In Progress\n"
	if got != want {
		t.Errorf("Fields output = %q, want %q", got, want)
	}
}

// TestViewFieldsAlignsLabelsToTheWidestInTheBlock pins the whole point of
// Fields over a bare Row: every value in one call starts in the SAME
// column, padded to the widest label — not each row aligned to its own
// label.
func TestViewFieldsAlignsLabelsToTheWidestInTheBlock(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Fields(
		output.Field{Label: "Type", Value: "Bug"},
		output.Field{Label: "Assignee", Value: "Ada Lovelace"},
	)

	got := buf.String()
	want := "Type:      Bug\nAssignee:  Ada Lovelace\n"
	if got != want {
		t.Errorf("Fields output = %q, want %q", got, want)
	}
}

// TestViewFieldsSkipsEmptyValues pins that a field with nothing to say
// produces no row at all — not a row with a blank value — and that
// skipping one field does not disturb the column the rest align to.
func TestViewFieldsSkipsEmptyValues(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Fields(
		output.Field{Label: "Reporter", Value: ""},
		output.Field{Label: "Assignee", Value: "  "},
		output.Field{Label: "Type", Value: "Bug"},
	)

	got := buf.String()
	want := "Type:  Bug\n"
	if got != want {
		t.Errorf("Fields output = %q, want %q", got, want)
	}
}

// TestViewFieldsSkipsAnEntityStyledEmptyValue pins the gotcha Fields
// exists to guard against: a colored value (cli.Entity, cli.Status, ...)
// can wrap an EMPTY name in real ANSI codes, since lipgloss.Style.Render
// does not special-case "". Checking the raw string for whitespace alone
// would see those escape bytes as content and print a row of bare color
// codes; Fields must strip ANSI first to see there is nothing there.
func TestViewFieldsSkipsAnEntityStyledEmptyValue(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorAlways)

	styledEmpty := "\x1b[38;5;208m\x1b[0m"
	v.Fields(
		output.Field{Label: "Assignee", Value: styledEmpty},
		output.Field{Label: "Type", Value: "Bug"},
	)

	got := buf.String()
	if strings.Contains(got, "Assignee") {
		t.Errorf("an ANSI-wrapped empty value was not skipped: %q", got)
	}
}

// TestViewLineJoinsCells pins Line's shape: cells joined by the same
// gutter Row uses, so a Line drawn alongside Row rows reads as one
// layout.
func TestViewLineJoinsCells(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Line("PROJ-123", "Fix the thing", "In Progress")

	got := strings.TrimSuffix(buf.String(), "\n")
	want := "PROJ-123  Fix the thing  In Progress"
	if got != want {
		t.Errorf("Line output = %q, want %q", got, want)
	}
}

// TestViewOutputAgreesWithConstructedMode pins that the View's
// clog.Output reflects the SAME color mode NewView was given — the
// property that keeps a View's own rendering consistent with whatever
// cli.ResolveColorMode already decided for the same writer.
func TestViewOutputAgreesWithConstructedMode(t *testing.T) {
	var buf bytes.Buffer

	never := output.NewView(&buf, clog.ColorNever)
	if !never.Output().ColorsDisabled() {
		t.Error("a View built with ColorNever reports colors enabled")
	}

	always := output.NewView(&buf, clog.ColorAlways)
	if always.Output().ColorsDisabled() {
		t.Error("a View built with ColorAlways reports colors disabled")
	}
}

// TestViewNonTTYNeverWraps pins that Fields never wraps or otherwise
// alters a value against a non-TTY writer — Output.Width() is 0 off a
// terminal, and x/ansi.WrapSoft already no-ops there too, so a
// redirected or piped invocation keeps exactly one line per field
// regardless of value length.
func TestViewNonTTYNeverWraps(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	long := strings.Repeat("word ", 40) + "end"
	v.Fields(output.Field{Label: "Summary", Value: long})

	got := buf.String()
	if strings.Count(got, "\n") != 1 {
		t.Errorf("Fields against a non-TTY writer produced %d lines, want exactly 1: %q", strings.Count(got, "\n"), got)
	}
	if !strings.Contains(got, long) {
		t.Errorf("Fields output does not contain the unwrapped value: %q", got)
	}
}

func TestViewPipePreservesLongRowsAndEmptyLine(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)
	v.Line(strings.Repeat("東京🙂", 40), "last")
	v.Line()
	want := strings.Repeat("東京🙂", 40) + "  last\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("pipe row changed: %q, want %q", got, want)
	}
}

func TestViewFieldsPreservesCRLFAndBlankLines(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)
	v.Fields(output.Field{Label: "Body", Value: "first\r\n\r\n  last\r\n"})
	want := "Body:  first\n       \n         last\n       \n"
	if got := buf.String(); got != want {
		t.Fatalf("field line endings or blank lines changed: %q, want %q", got, want)
	}
}

// TestViewBlockWritesEachPhysicalLine pins Block's contract: it writes
// pre-formatted multi-line text verbatim, one physical line per call,
// with no wrapping or indentation of its own — the caller (internal/adf's
// Render) already did both.
func TestViewBlockWritesEachPhysicalLine(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Block("first line\nsecond line\n  indented third")

	got := buf.String()
	want := "first line\nsecond line\n  indented third\n"
	if got != want {
		t.Errorf("Block output = %q, want %q", got, want)
	}
}

// TestViewBlockTrailingNewlineDoesNotAddABlankLine pins that a
// "\n"-terminated block writes the SAME lines as its non-terminated
// equivalent — no extra trailing blank line from strings.Split's own
// final "" element after the trailing separator. internal/adf.Render
// already trims its own output before handing it to Block, so this
// guards a general caller of Block, not that one specifically.
func TestViewBlockTrailingNewlineDoesNotAddABlankLine(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Block("first line\nsecond line\n")

	got := buf.String()
	want := "first line\nsecond line\n"
	if got != want {
		t.Errorf("Block(text ending in \\n) = %q, want %q (no extra blank line)", got, want)
	}
}

// TestViewBlockOfEmptyStringWritesNothing pins that an absent block (no
// description, a parse failure the caller chose to skip) produces no
// output at all, not a stray blank line.
func TestViewBlockOfEmptyStringWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Block("")

	if got := buf.String(); got != "" {
		t.Errorf("Block(\"\") wrote %q, want nothing", got)
	}
}

// TestViewLoggerAndOutputShareOneUnderlyingWriter proves View's Logger()
// and Output() accessors both reach the same destination NewView was
// given, rather than each building their own.
func TestViewLoggerAndOutputShareOneUnderlyingWriter(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Logger().Output().WriteLine("from the logger")
	v.Output().WriteLine("from output")

	got := buf.String()
	if !strings.Contains(got, "from the logger") || !strings.Contains(got, "from output") {
		t.Errorf("Logger() and Output() did not share one writer: %q", got)
	}
}

// TestViewSection pins the titled-divider shape: the title plus a rule
// of the divider character. ColorNever keeps it plain so the assertion
// sees the text, not escape codes.
func TestViewSection(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	v.Section("Description")

	got := buf.String()
	if !strings.Contains(got, "Description") {
		t.Errorf("Section missing its title: %q", got)
	}
	if !strings.Contains(got, "─") {
		t.Errorf("Section missing the divider rule: %q", got)
	}
}

// Pipe output has no terminal width. The native divider keeps fitting titles
// on its 80-column rule and leaves longer titles intact on one physical line.
func TestViewSectionNonTTYKeepsCompleteTitles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, title, want string
	}{
		{name: "fitting title", title: "Rich text", want: "─── Rich text " + strings.Repeat("─", 66) + "\n"},
		{name: "long title", title: strings.Repeat("long", 25), want: strings.Repeat("long", 25) + "\n"},
		{name: "wide title", title: strings.Repeat("東京🙂", 20), want: strings.Repeat("東京🙂", 20) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			output.NewView(&buf, clog.ColorNever).Section(tc.title)
			if got := buf.String(); got != tc.want {
				t.Errorf("pipe section = %q, want %q", got, tc.want)
			}
		})
	}
}
