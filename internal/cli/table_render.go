package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// terminalTableRow protects table spacing from terminal hard-tab optimization.
// Primer supplies column widths; padding uses the same width method as layout.
func terminalTableRow(cells []string, widths []int, method ansi.Method) string {
	var line strings.Builder
	for i, cell := range cells {
		if i > 0 {
			line.WriteString("\x1b[8m  \x1b[28m")
		}
		cell = method.Truncate(cell, widths[i], "…")
		line.WriteString(cell)
		if padding := widths[i] - method.StringWidth(cell); i < len(cells)-1 && padding > 0 {
			line.WriteString("\x1b[8m")
			line.WriteString(strings.Repeat(" ", padding))
			line.WriteString("\x1b[28m")
		}
	}
	return line.String()
}
