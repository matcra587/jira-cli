package cli

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTerminalTableRowProtectsPadding(t *testing.T) {
	tests := []struct {
		name   string
		cells  []string
		widths []int
		method ansi.Method
		want   string
	}{
		{"padding", []string{"A", "B"}, []int{3, 4}, ansi.WcWidth, "A\x1b[8m  \x1b[28m\x1b[8m  \x1b[28mB"},
		{"truncation", []string{"abcdef", "B"}, []int{3, 1}, ansi.WcWidth, "ab…\x1b[8m  \x1b[28mB"},
		{"grapheme", []string{"👨‍👩‍👧‍👦", "B"}, []int{3, 1}, ansi.GraphemeWidth, "👨‍👩‍👧‍👦\x1b[8m \x1b[28m\x1b[8m  \x1b[28mB"},
		{"wide", []string{"界", "B"}, []int{3, 1}, ansi.WcWidth, "界\x1b[8m \x1b[28m\x1b[8m  \x1b[28mB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalTableRow(tt.cells, tt.widths, tt.method); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
