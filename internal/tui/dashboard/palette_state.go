package dashboard

import (
	"slices"

	"github.com/gechr/primer/key"
	"github.com/gechr/primer/palette"
)

// paletteState retains canonical entries independently of their dialog layout.
// Primer continues to own fuzzy filtering, query editing and cursor movement.
type paletteState struct {
	menu    palette.Model
	entries []palette.Entry
}

func newPaletteState(entries []palette.Entry) paletteState {
	entries = slices.Clone(entries)
	return paletteState{
		menu:    palette.New("Type to filter", entries, commandStyles()),
		entries: entries,
	}
}

func (p *paletteState) visibleEntries() []palette.Entry {
	return visiblePaletteEntries(p.menu, len(p.entries))
}

// Primer v0.2.9 exposes no filtered-entry accessor. Walking a value copy through
// its public navigation API preserves its admitted matches without duplicating
// fuzzy-filter rules or disturbing the live query and cursor.
func visiblePaletteEntries(menu palette.Model, count int) []palette.Entry {
	for range count {
		menu.Update(key.Replay("up"))
	}
	entries := make([]palette.Entry, 0, count)
	for range count {
		entry, ok := menu.Selected()
		if !ok {
			break
		}
		entries = append(entries, entry)
		menu.Update(key.Replay("down"))
		if next, _ := menu.Selected(); next == entry {
			break
		}
	}
	return entries
}
