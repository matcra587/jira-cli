package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
)

func TestLabelHintsStayOnOneRowWhenWideAndRemainVisibleAfterResize(t *testing.T) {
	m := editModel(t)
	applyCommand(m, press(m, 'l'))
	d := m.dialogs.Top().(*labelsDialog)
	for _, size := range []tea.WindowSizeMsg{{Width: 159, Height: 45}, {Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m.Update(size)
		for _, adding := range []bool{true, false} {
			if d.adding != adding {
				m.Update(key.Replay("tab"))
			}
			shown := xansi.Strip(m.View().Content)
			footer := strings.TrimSpace(xansi.Strip(d.Footer()))
			if size.Width >= 80 && strings.Contains(footer, "\n") {
				t.Fatalf("footer unnecessarily wrapped at %d: %q", size.Width, footer)
			}
			for _, hint := range []string{"enter next", "esc back"} {
				if !strings.Contains(shown, hint) {
					t.Fatalf("primary control %q clipped at %dx%d", hint, size.Width, size.Height)
				}
			}
			if strings.Contains(shown, "terminal too narrow") {
				t.Fatal("footer sizing invalidated the dialog")
			}
		}
	}
}

func TestLabelOverlayIsCompactAndCenteredAcrossWindowSizes(t *testing.T) {
	m := editModel(t)
	applyCommand(m, press(m, 'l'))
	for _, size := range []tea.WindowSizeMsg{{Width: 159, Height: 45}, {Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		m.Update(size)
		lines := strings.Split(xansi.Strip(m.View().Content), "\n")
		top, bottom, left, boxWidth := -1, -1, 0, 0
		for i, line := range lines {
			if start := strings.Index(line, "╭"); start >= 0 {
				top, left = i, xansi.WcWidth.StringWidth(line[:start])
				end := strings.Index(line[start:], "╮")
				if end >= 0 {
					boxWidth = xansi.WcWidth.StringWidth(line[start : start+end+len("╮")])
				}
			}
			if strings.Contains(line, "╰") {
				bottom = i
			}
		}
		if top < 0 || bottom < top || boxWidth == 0 {
			t.Fatalf("no complete overlay at %dx%d", size.Width, size.Height)
		}
		right := size.Width - left - boxWidth
		if delta := left - right; delta < -1 || delta > 1 {
			t.Fatalf("overlay is horizontally off center: left %d, right %d", left, right)
		}
		if delta := top - (size.Height - bottom - 1); delta < -1 || delta > 1 {
			t.Fatalf("overlay is vertically off center at %dx%d", size.Width, size.Height)
		}
		if size.Width >= 80 {
			for _, text := range []string{"Labels ·", "Enter adds a label.", "Add a label", "Labels to keep", "enter next"} {
				found := false
				for _, line := range lines {
					if pos := strings.Index(line, text); pos >= 0 {
						found = true
						if got := xansi.WcWidth.StringWidth(line[:pos]); got != left+8 {
							t.Fatalf("%q starts at %d; want shared centered group edge %d", text, got, left+8)
						}
					}
				}
				if !found {
					t.Fatalf("missing group member %q", text)
				}
			}
		}
		if size.Width >= 80 && boxWidth > 64 {
			t.Fatalf("short label editor is unnecessarily wide: %d", boxWidth)
		}
	}
}

func TestLabelConfirmationShowsEmptySourceAndBoundsItsButtons(t *testing.T) {
	m := editModel(t)
	m.Update(tea.WindowSizeMsg{Width: 159, Height: 45})
	m.acceptLabels(&labelsDialog{id: "101", target: "DEMO-1", labels: []string{"first", "second"}})
	d := m.dialogs.Top().(*writeReview)
	shown := xansi.Strip(m.View().Content)
	for _, want := range []string{"Confirm labels · DEMO-1", "Current labels (0):", "None", "Labels to apply (2):", "first", "second", "[Cancel]", "Apply", "enter confirm"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("confirmation omitted %q", want)
		}
	}
	for _, text := range []string{"Confirm labels · DEMO-1", "[Cancel]  Apply labels", "tab select · enter confirm · esc back"} {
		found := false
		for _, line := range strings.Split(shown, "\n") {
			if pos := strings.Index(line, text); pos >= 0 {
				found = true
				left := xansi.WcWidth.StringWidth(line[:pos])
				center := 2*left + xansi.WcWidth.StringWidth(text) - 1
				if delta := center - 158; delta < -2 || delta > 2 {
					t.Fatalf("%q is not centered in the overlay: doubled center %d", text, center)
				}
			}
		}
		if !found {
			t.Fatalf("missing centered element %q", text)
		}
	}
	if strings.Contains(shown, "issue ID") {
		t.Fatal("confirmation exposed implementation details instead of the change")
	}
	for _, line := range strings.Split(xansi.Strip(d.Confirm.Content(48)), "\n") {
		if xansi.WcWidth.StringWidth(line) > 48 {
			t.Fatalf("confirmation laid out buttons against an unbounded prompt: %q", line)
		}
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Active() {
		t.Fatal("confirmation did not retain cancellation as its default")
	}
}

func TestLabelEnterAddsThenAdvancesAndSaveDoesNotAdvance(t *testing.T) {
	m := editModel(t)
	applyCommand(m, press(m, 'l'))
	d := m.dialogs.Top().(*labelsDialog)
	m.Update(tea.PasteMsg{Content: "another,label"})
	if !strings.Contains(d.Footer(), "enter add") {
		t.Fatal("typing did not advertise Enter to add")
	}
	m.Update(key.Replay("ctrl+s"))
	if m.dialogs.Top() != d || d.line.Value() != "another,label" {
		t.Fatal("Ctrl+S still advanced or changed the label draft")
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Top() != d || d.line.Value() != "" || len(d.labels) != 2 {
		t.Fatal("Enter did not add exactly one literal label in place")
	}
	if !strings.Contains(d.Footer(), "enter next") || strings.Contains(d.Footer(), "ctrl+s") {
		t.Fatal("empty input did not advertise Enter to advance")
	}
	m.Update(key.Replay("enter"))
	if _, ok := m.dialogs.Top().(*writeReview); !ok {
		t.Fatal("Enter on empty input did not open confirmation")
	}
	m.Update(key.Replay("enter"))
	if m.dialogs.Active() {
		t.Fatal("confirmation did not retain cancellation as its default")
	}
}
