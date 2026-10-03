package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clib/theme"
	"github.com/gechr/clog"
	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/output"
)

// tableTestRow is the generic row type these tests render — deliberately
// two plain fields (an identity-shaped key and a free-text summary),
// mirroring the shape a real `jira issue list` row will eventually have
// without depending on anything from internal/cli or internal/envelope,
// which this package cannot import (view.go's own doc comment on why).
type tableTestRow struct {
	Key     string
	Summary string
}

// styledKeyColumn simulates what a real caller's Column.Render does:
// call a value sink (cli.Entity, here faked with a raw ANSI wrap since
// this package cannot import cli) and hand StyledCell both the styled
// display text and the plain text the sink derived it from.
func styledKeyColumn() table.Column[tableTestRow] {
	return table.Column[tableTestRow]{
		Name: "key", Header: "KEY",
		Render: func(r tableTestRow, _ *table.RenderContext) table.Cell {
			return table.StyledCell("\x1b[38;5;33m"+r.Key+"\x1b[0m", r.Key)
		},
	}
}

func summaryColumn() table.Column[tableTestRow] {
	return table.Column[tableTestRow]{
		Name: "summary", Header: "SUMMARY",
		Render: func(r tableTestRow, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Summary)
		},
	}
}

// TestTableAlignsStyledCellsByPlainWidthNotRawBytes pins the reason
// StyledCell exists at all: a cell built from a sink's ANSI-wrapped
// display text must still align by its VISIBLE width, not the raw byte
// count the escape codes would otherwise inflate it by. It asserts this
// by stripping ANSI from every rendered line and checking the second
// column starts at the identical column offset in the header and both
// data rows — proving the "ABC-1" row's shorter styled text was padded
// out to match "ABC-123"'s wider one.
func TestTableAlignsStyledCellsByPlainWidthNotRawBytes(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	columns := []table.Column[tableTestRow]{styledKeyColumn(), summaryColumn()}
	rows := []tableTestRow{
		{Key: "ABC-1", Summary: "Fix the thing"},
		{Key: "ABC-123", Summary: "Do the other"},
	}

	output.Table(v, theme.Dark(), columns, rows)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows): %q", len(lines), lines)
	}

	headerCol := strings.Index(ansi.Strip(lines[0]), "SUMMARY")
	row1Col := strings.Index(ansi.Strip(lines[1]), "Fix the thing")
	row2Col := strings.Index(ansi.Strip(lines[2]), "Do the other")
	if headerCol < 0 || row1Col < 0 || row2Col < 0 {
		t.Fatalf("missing an expected column value: header=%d row1=%d row2=%d, lines=%q", headerCol, row1Col, row2Col, lines)
	}
	if headerCol != row1Col || row1Col != row2Col {
		t.Errorf("summary column not aligned: header at %d, row1 at %d, row2 at %d (%q)", headerCol, row1Col, row2Col, lines)
	}

	// The styled Key cell's own ANSI must survive untouched — Table lines
	// values up, it never re-styles or strips what a sink already built.
	if !strings.Contains(lines[1], "\x1b[38;5;33mABC-1\x1b[0m") {
		t.Errorf("row 1 lost its styled Key cell: %q", lines[1])
	}
}

// TestTableAtWidthZeroDoesNotTruncateFlexColumns pins non-TTY behavior:
// a bytes.Buffer is never a terminal, so Output.Width() is 0, and
// primer's own flex-shrink only engages when MaxWidth > 0 (its Grid.
// MaxWidth doc: "0 = disabled") — the same "0 means don't wrap" contract
// writeAligned already relies on for Fields. A long Flex value must
// therefore come through whole, with no "…" truncation.
func TestTableAtWidthZeroDoesNotTruncateFlexColumns(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	long := strings.Repeat("word ", 40) + "end"
	columns := []table.Column[tableTestRow]{
		{Name: "key", Header: "KEY", Render: func(r tableTestRow, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Key)
		}},
		{Name: "summary", Header: "SUMMARY", Flex: true, Render: func(r tableTestRow, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Summary)
		}},
	}
	rows := []tableTestRow{{Key: "ABC-1", Summary: long}}

	output.Table(v, theme.Dark(), columns, rows)

	got := buf.String()
	if strings.Contains(got, "…") {
		t.Errorf("a Flex column was truncated at Output.Width()==0: %q", got)
	}
	if !strings.Contains(got, long) {
		t.Errorf("the long Flex value did not come through whole: %q", got)
	}
}

// TestTableEmptyItemsWritesNothing pins that Table has no domain
// knowledge of what an empty list means — it is a generic layout
// primitive, so an empty items slice produces no output at all (not
// even a bare header), leaving "no results" messaging to whichever list
// command knows what its own rows represent, the same way
// renderCommentList's own "No comments." line lives in comment.go, not
// in a shared primitive.
func TestTableEmptyItemsWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	columns := []table.Column[tableTestRow]{styledKeyColumn(), summaryColumn()}
	output.Table(v, theme.Dark(), columns, nil)

	if got := buf.String(); got != "" {
		t.Errorf("Table with no items wrote %q, want nothing", got)
	}
}

// TestTableColorDisabledEmitsNoANSI pins the audit's own human-mode
// contract (COLUMNS=100, TERM=dumb, NO_COLOR=1): with color off, Table's
// OWN styling — the bold header primer renders by default, and the dim
// row index WithShowIndex adds — must degrade to plain text exactly like
// every other sink in this codebase, not just the caller-supplied cell
// values. Every column here uses TextCell (no caller-injected ANSI), so
// any escape byte in the output can only have come from Table itself.
func TestTableColorDisabledEmitsNoANSI(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)

	columns := []table.Column[tableTestRow]{
		{Name: "key", Header: "KEY", Render: func(r tableTestRow, _ *table.RenderContext) table.Cell {
			return table.TextCell(r.Key)
		}},
		summaryColumn(),
	}
	rows := []tableTestRow{
		{Key: "ABC-1", Summary: "Fix the thing"},
		{Key: "ABC-2", Summary: "Do the other"},
	}

	output.Table(v, theme.Dark(), columns, rows, table.WithShowIndex(true))

	got := buf.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("color-disabled Table output carries an ANSI escape byte: %q", got)
	}
}
