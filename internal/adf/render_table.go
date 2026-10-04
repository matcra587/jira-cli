package adf

import (
	"math"
	"strconv"
	"strings"

	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"
)

// Grid aligns strings but has no merged-cell geometry. The placement below
// interprets ADF spans, then delegates each physical row's alignment to Grid.
// Complex or malformed input falls back to authored row/cell order, preserving
// content and raw span attrs without allocating from untrusted dimensions.
const maxTerminalTableColumns = 128

type terminalCell struct {
	node              *Node
	row, col, colspan int
	text              string
}

func cellSpans(n *Node) (colspan, rowspan int, ok bool) {
	var cols, rows *float64
	switch NodeType(n.Type()) {
	case NodeTypeTableCell:
		v, err := AsTableCell(n)
		if err != nil {
			return 0, 0, false
		}
		cols, rows = v.Attrs.Colspan, v.Attrs.Rowspan
	case NodeTypeTableHeader:
		v, err := AsTableHeader(n)
		if err != nil {
			return 0, 0, false
		}
		cols, rows = v.Attrs.Colspan, v.Attrs.Rowspan
	default:
		return 0, 0, false
	}
	span := func(v *float64) (int, bool) {
		if v == nil {
			return 1, true
		}
		if *v < 1 || *v > maxTerminalTableColumns || math.Trunc(*v) != *v {
			return 0, false
		}
		return int(*v), true
	}
	colspan, ok = span(cols)
	if !ok {
		return 0, 0, false
	}
	rowspan, ok = span(rows)
	return colspan, rowspan, ok
}

func tableCells(n *Node) ([][]*terminalCell, []*terminalCell, int, bool) {
	rows := n.Content()
	grid := make([][]*terminalCell, len(rows))
	var cells []*terminalCell
	columns := 0
	for r := range rows {
		if NodeType(rows[r].Type()) != NodeTypeTableRow {
			return nil, nil, 0, false
		}
		authored := rows[r].Content()
		col := 0
		for c := range authored {
			for col < len(grid[r]) && grid[r][col] != nil {
				col++
			}
			cs, rs, ok := cellSpans(&authored[c])
			if !ok || col+cs > maxTerminalTableColumns || rs > len(rows)-r {
				return nil, nil, 0, false
			}
			cell := &terminalCell{node: &authored[c], row: r, col: col, colspan: cs}
			for rr := r; rr < r+rs; rr++ {
				if len(grid[rr]) < col+cs {
					grid[rr] = append(grid[rr], make([]*terminalCell, col+cs-len(grid[rr]))...)
				}
				for cc := col; cc < col+cs; cc++ {
					if grid[rr][cc] != nil {
						return nil, nil, 0, false
					}
					grid[rr][cc] = cell
				}
			}
			cells = append(cells, cell)
			columns = max(columns, col+cs)
			col += cs
		}
	}
	if len(cells) == 0 {
		return nil, nil, 0, false
	}
	// Pad validated rows only to the authored width, including empty rows
	// crossed by rowspans. Invalid input never allocates the maximum width.
	for r := range grid {
		grid[r] = append(grid[r], make([]*terminalCell, columns-len(grid[r]))...)
	}
	return grid, cells, columns, true
}

func (w *walker) renderTable(n *Node, prefix string) {
	grid, cells, cols, ok := tableCells(n)
	if !ok {
		w.renderTableFallback(n, prefix)
		return
	}
	widths := make([]int, cols)
	for c := range widths {
		widths[c] = 2
	}
	for _, cell := range cells {
		sub := &walker{styler: w.styler}
		sub.blocks(cell.node.Content(), "")
		cell.text = strings.TrimRight(sub.buf.String(), "\n")

		for _, line := range strings.Split(cell.text, "\n") {
			needed := ansi.StringWidth(line)
			have := spanWidth(widths, cell.col, cell.colspan)
			// Allocate the minimum additional width equally across a merged cell.
			for c := cell.col; have < needed; c = cell.col + (c-cell.col+1)%cell.colspan {
				widths[c]++
				have++
			}
		}
	}
	available := w.width - ansi.StringWidth(prefix)
	if w.width > 0 {
		// One content cell plus framing is the smallest useful column. A narrow
		// display uses labeled authored cells instead of dropping columns.
		if available < cols*3+1 {
			w.renderTableFallback(n, prefix)
			return
		}
		for spanWidth(widths, 0, cols)+2 > available {
			widest := 0
			for c := range widths {
				if widths[c] > widths[widest] {
					widest = c
				}
			}
			widths[widest]--
		}
	}
	border := strings.Repeat("─", spanWidth(widths, 0, cols))
	w.writeLine(prefix, "┌"+border+"┐")
	for r, row := range grid {
		var lines [][]string
		var measure []string
		for c := 0; c < cols; {
			cell := row[c]
			cs := 1
			text := ""
			if cell != nil {
				cs = cell.colspan
				if cell.row == r {
					text = cell.text
				}
			}
			width := spanWidth(widths, c, cs)

			lines = append(lines, strings.Split(ansi.WrapSoft(text, width), "\n"))
			measure = append(measure, strings.Repeat(" ", width), "│")
			c += cs
		}
		height := 1
		for _, ls := range lines {
			height = max(height, len(ls))
		}
		physical := make([][]string, 0, height+1)
		physical = append(physical, measure)
		for line := 0; line < height; line++ {
			fields := make([]string, 0, len(measure))
			for _, ls := range lines {
				value := ""
				if line < len(ls) {
					value = ls[line]
				}
				fields = append(fields, value, "│")
			}
			physical = append(physical, fields)
		}
		aligned, _ := table.NewGrid(physical, table.WithColumnPadding(0), table.WithWidthMethod(ansi.GraphemeWidth)).AlignColumns()
		for _, line := range aligned[1:] {
			w.writeLine(prefix, "│"+line)
		}
		header := false
		for _, cell := range row[:cols] {
			if cell != nil && cell.row == r && NodeType(cell.node.Type()) == NodeTypeTableHeader {
				header = true
			}
		}
		if header && r+1 < len(grid) {
			w.writeLine(prefix, tableHeaderSeparator(widths, row, grid[r+1]))
		}
	}
	w.writeLine(prefix, "└"+border+"┘")
}

// spanWidth includes the separator slots absorbed by a colspan.
func spanWidth(widths []int, col, count int) int {
	width := count - 1
	for _, v := range widths[col : col+count] {
		width += v
	}
	return width
}

func (w *walker) renderTableFallback(n *Node, prefix string) {
	rows := n.Content()
	if len(rows) == 0 {
		w.renderPlaceholder(n, prefix)
		return
	}
	for r := range rows {
		if NodeType(rows[r].Type()) != NodeTypeTableRow {
			w.renderTableFallbackNode(&rows[r], prefix)
			continue
		}
		authored := rows[r].Content()
		if len(authored) == 0 {
			w.renderPlaceholder(&rows[r], prefix)
		}
		for c := range authored {
			label := "Row " + strconv.Itoa(r+1) + ", cell " + strconv.Itoa(c+1)
			if attrs := authored[c].Attrs(); len(attrs) > 0 {
				label += " " + w.styler.Text(string(attrs))
			}
			w.writeBlock(prefix, prefix, []string{label})
			w.renderTableFallbackNode(&authored[c], prefix)
		}
	}
}

// Header separators carry table semantics without wrapping an already styled
// cell in another ANSI span. Spaces preserve a rowspan crossing the boundary.
func tableHeaderSeparator(widths []int, row, next []*terminalCell) string {
	continuing := func(c int) bool { return row[c] != nil && row[c] == next[c] }
	var line strings.Builder
	if continuing(0) {
		line.WriteString("│")
	} else {
		line.WriteString("├")
	}
	for c, width := range widths {
		fill := "─"
		if continuing(c) {
			fill = " "
		}
		line.WriteString(strings.Repeat(fill, width))
		if c+1 < len(widths) {
			joint := "─"
			if continuing(c) && continuing(c+1) && row[c] == row[c+1] {
				joint = " "
			}
			line.WriteString(joint)
		}
	}
	if continuing(len(widths) - 1) {
		line.WriteString("│")
	} else {
		line.WriteString("┤")
	}
	return line.String()
}

func (w *walker) renderTableFallbackNode(n *Node, prefix string) {
	switch NodeType(n.Type()) {
	case NodeTypeTableCell, NodeTypeTableHeader:
		// The containing row already emitted the cell label and raw attrs.
	case NodeTypeText, NodeTypeStatus:
		w.writeBlock(prefix, prefix, []string{w.inline(n)})
		return
	default:
		w.block(n, prefix)
		if _, known := blockDispatch[NodeType(n.Type())]; known {
			return
		}
	}
	children := n.Content()
	for i := range children {
		if i > 0 {
			w.buf.WriteByte('\n')
		}
		w.renderTableFallbackNode(&children[i], prefix)
	}
}
