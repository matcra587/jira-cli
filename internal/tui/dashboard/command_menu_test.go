package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/palette"
)

func TestCommandMenuKeepsShortcutVisibleAndPreservesCanonicalFilterState(t *testing.T) {
	const name = "日本語 command with a hidden ending"
	d := newCommandDialog([]palette.Entry{
		{Name: name, Desc: "Explanation belongs below the selected command, without crowding its shortcut.", Key: "ctrl+o"},
		{Name: "Quit", Desc: "Return to the terminal", Key: "q"},
	})
	before, _ := d.menu.Selected()
	for _, width := range []int{24, 40, 70} {
		rows := strings.Split(xansi.Strip(d.Content(width)), "\n")
		if !strings.HasSuffix(rows[3], "ctrl+o ") || !strings.HasSuffix(rows[6], "     q ") {
			t.Fatalf("shortcut column was clipped or misaligned at %d: %q", width, rows)
		}
		if strings.Contains(rows[3], "Explanation") || !strings.Contains(rows[4], "Explanation") {
			t.Fatal("selected explanation crowded the command row")
		}
		for _, row := range rows {
			if xansi.WcWidth.StringWidth(row) != width {
				t.Fatal("command rendering exceeded its cell budget")
			}
		}
		if selected, _ := d.menu.Selected(); selected != before || d.menu.Query() != "" {
			t.Fatal("rendering changed live command selection or filter")
		}
	}
	for _, r := range "hidden ending" {
		d.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	d.Content(24)
	if selected, ok := d.menu.Selected(); !ok || selected.Name != name || selected.Key != "ctrl+o" {
		t.Fatal("clipped names changed fuzzy matching or native replay keys")
	}
	if _, _, result := d.Update(key.Replay("enter")); result != dialog.ResultSubmit {
		t.Fatal("layout prevented the selected command from being submitted")
	}
}
