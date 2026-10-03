//go:build outputaudit

package audit

import (
	"cmp"
	"slices"
)

// ExpectedCell is one point of the independent expected Cartesian
// product: op × scenario category × mode × applicable axis.
type ExpectedCell struct {
	Op       string
	Variant  string
	Category Category
	Mode     Mode
	Axis     Axis
}

// Key is the cell's stable identity, shared with recorded cells and
// the exemption table.
func (c ExpectedCell) Key() string {
	return cellKey(c.Op, c.Variant, string(c.Category), string(c.Mode), string(c.Axis))
}

// evaluated is this cell's recorded form, carrying its verdict.
func (c ExpectedCell) evaluated(status, detail string) Cell {
	return Cell{
		Op: c.Op, Variant: c.Variant, Category: string(c.Category),
		Mode: string(c.Mode), Axis: string(c.Axis), Status: status, Detail: detail,
	}
}

// expectedCells derives the full expected cell set from the inventory
// members' METADATA joined to the applicability map — never from which
// recipes happen to exist. That independence is the R10 rule: a
// hand-authored scenario table cannot define its own completeness,
// because a wrong or missing label would silently skip a check. An
// expected cell with no recorded result fails the run by name.
//
// Paths may share an op (a shorthand delegating to another command's
// operation), so cells deduplicate on op+variant: the contract is per
// operation, and each op's recipes prove it once.
func expectedCells(members []Member) []ExpectedCell {
	seen := map[string]bool{}
	var cells []ExpectedCell
	for _, member := range members {
		m := member.Meta
		for _, category := range categoriesFor(m) {
			for _, mode := range modesFor(category) {
				for _, axis := range cellAxes(m, category, mode) {
					cell := ExpectedCell{Op: m.Op, Variant: m.Variant, Category: category, Mode: mode, Axis: axis}
					if seen[cell.Key()] {
						continue
					}
					seen[cell.Key()] = true
					cells = append(cells, cell)
				}
			}
		}
	}
	slices.SortFunc(cells, func(a, b ExpectedCell) int { return cmp.Compare(a.Key(), b.Key()) })
	return cells
}
