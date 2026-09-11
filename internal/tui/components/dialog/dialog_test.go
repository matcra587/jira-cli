package dialog

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	pkey "github.com/gechr/primer/key"
)

// stubDialog is a minimal Dialog for driving the Stack and Shell without any
// real dialog. seen is a pointer so the messages it records survive the value
// copies the Stack makes when it stores each Update's returned dialog.
type stubDialog struct {
	id     string
	title  string
	body   string
	hints  []pkey.Hint
	result Result
	seen   *[]tea.Msg
}

func (d stubDialog) Title() string      { return d.title }
func (d stubDialog) Content(int) string { return d.body }
func (d stubDialog) Hints() []pkey.Hint { return d.hints }

func (d stubDialog) Update(msg tea.Msg) (Dialog, tea.Cmd, Result) {
	if d.seen != nil {
		*d.seen = append(*d.seen, msg)
	}
	return d, nil, d.result
}

// keyMsg is a stand-in for any non-resize message.
type keyMsg struct{}

// selfFramedStub is a stubDialog that reports itself self-framed.
type selfFramedStub struct {
	stubDialog
	self bool
}

func (d selfFramedStub) SelfFramed() bool { return d.self }

func TestStackSelfFramed(t *testing.T) {
	bordered := NewShell(ShellConfig{Styles: Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())}})
	backdrop := strings.Repeat(strings.Repeat(" ", 40)+"\n", 12)

	t.Run("plain dialog is framed by the shell", func(t *testing.T) {
		s := New(bordered)
		s.Push(stubDialog{body: "PAYLOAD"})
		got := s.View(backdrop, 40, 12)
		if !strings.ContainsAny(got, "│─") {
			t.Fatalf("plain dialog should gain the shell's border:\n%s", got)
		}
	})

	t.Run("self-framed dialog is placed verbatim", func(t *testing.T) {
		s := New(bordered)
		s.Push(selfFramedStub{body: "PAYLOAD", self: true})
		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "PAYLOAD") {
			t.Fatalf("self-framed content missing:\n%s", got)
		}
		if strings.ContainsAny(got, "│─") {
			t.Errorf("self-framed dialog must not gain the shell's border:\n%s", got)
		}
	})
}

// wrappingDialog is a Dialog whose Content honors the width it is given, hard-
// wrapping its body to that column count exactly as a real body-rendering
// dialog would. It is what makes an off-by-one in the width the Shell allots
// observable: a body given one column too few wraps a character early.
type wrappingDialog struct {
	stubDialog
}

func (d wrappingDialog) Content(width int) string {
	return lipgloss.NewStyle().Width(width).Render(d.body)
}

func TestShellFrameWidth(t *testing.T) {
	bordered := NewShell(ShellConfig{
		// MaxWidth 9 minus the border's two frame columns leaves an inner width
		// of exactly 7 — the width of the payload — so the body fits only when
		// the Shell does not steal a column for a scrollbar it will not draw.
		MaxWidth: 9,
		Styles:   Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
	})
	backdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 40)+"\n", 12), "\n")

	t.Run("a body that fits the inner width is not wrapped a column early", func(t *testing.T) {
		s := New(bordered)
		s.Push(wrappingDialog{stubDialog{body: "PAYLOAD"}})

		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "PAYLOAD") {
			t.Fatalf("7-column body wrapped inside a 7-column inner width:\n%s", got)
		}
	})

	t.Run("a scrolling body keeps full text width beside the scrollbar", func(t *testing.T) {
		// MaxWidth 12 minus the border leaves a 10-column inner region; the
		// scrollbar claims one, leaving exactly 7 for the payload. MaxHeight 5
		// forces the five-line body to scroll so the scrollbar actually draws.
		scrolling := NewShell(ShellConfig{
			MaxWidth:  12,
			MaxHeight: 5,
			Styles:    Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
		})
		s := New(scrolling)
		s.Push(wrappingDialog{stubDialog{body: strings.TrimSuffix(strings.Repeat("PAYLOAD\n", 5), "\n")}})

		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "PAYLOAD") {
			t.Fatalf("scrolling body lost a text column to the scrollbar:\n%s", got)
		}
		if !strings.ContainsAny(got, "│─") {
			t.Fatalf("scrolling dialog dropped its border:\n%s", got)
		}
	})
}

func TestShellTooNarrowShowsNoticeNotGarbage(t *testing.T) {
	shell := NewShell(ShellConfig{
		MaxWidth: 20,
		Styles:   Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
	})
	backdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 40)+"\n", 12), "\n")
	// A width-blind body wider than the 18-column inner width: boxing it would
	// interleave wrapped borders, so the Shell must swap in the notice.
	wide := strings.Repeat("X", 60)

	t.Run("plain frame", func(t *testing.T) {
		s := New(shell)
		s.Push(stubDialog{body: wide})
		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "too narrow") {
			t.Fatalf("overflowing body did not show the too-narrow notice:\n%s", got)
		}
		if strings.Contains(got, "XXX") {
			t.Fatalf("overflowing body leaked into the frame:\n%s", got)
		}
	})

	t.Run("footered frame", func(t *testing.T) {
		s := New(shell)
		s.Push(footeredStub{
			body:   wide,
			footer: "hints",
		})
		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "too narrow") {
			t.Fatalf("overflowing footered body did not show the too-narrow notice:\n%s", got)
		}
		if strings.Contains(got, "XXX") {
			t.Fatalf("overflowing footered body leaked into the frame:\n%s", got)
		}
	})

	t.Run("fitting body still renders", func(t *testing.T) {
		s := New(shell)
		s.Push(stubDialog{body: "PAYLOAD"})
		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "PAYLOAD") || strings.Contains(got, "too narrow") {
			t.Fatalf("fitting body was refused:\n%s", got)
		}
	})
}

func TestStack(t *testing.T) {
	t.Run("empty stack is inert", func(t *testing.T) {
		s := New(NewShell(ShellConfig{}))
		if s.Active() {
			t.Fatal("fresh stack reports active")
		}
		if s.Top() != nil {
			t.Fatal("fresh stack has a top dialog")
		}
		cmd, popped, res := s.Update(keyMsg{})
		if cmd != nil || popped != nil || res != ResultNone {
			t.Fatalf("update on empty stack = (%v, %v, %v), want (nil, nil, ResultNone)", cmd, popped, res)
		}
	})

	t.Run("push and top", func(t *testing.T) {
		s := New(NewShell(ShellConfig{}))
		s.Push(stubDialog{id: "a"})
		s.Push(stubDialog{id: "b"})
		if !s.Active() {
			t.Fatal("stack with dialogs reports inactive")
		}
		if got := s.Top().(stubDialog).id; got != "b" {
			t.Fatalf("top id = %q, want the last pushed %q", got, "b")
		}
	})

	t.Run("routes only to the top dialog", func(t *testing.T) {
		var bottomSeen, topSeen []tea.Msg
		s := New(NewShell(ShellConfig{}))
		s.Push(stubDialog{id: "bottom", seen: &bottomSeen})
		s.Push(stubDialog{id: "top", seen: &topSeen})

		s.Update(keyMsg{})

		if len(topSeen) != 1 {
			t.Fatalf("top saw %d messages, want 1", len(topSeen))
		}
		if len(bottomSeen) != 0 {
			t.Fatalf("bottom saw %d messages, want 0 (not the top)", len(bottomSeen))
		}
	})

	popCases := []struct {
		name   string
		result Result
	}{
		{name: "submit pops and returns the dialog", result: ResultSubmit},
		{name: "close pops and returns the dialog", result: ResultClose},
	}
	for _, tc := range popCases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(NewShell(ShellConfig{}))
			s.Push(stubDialog{id: "under"})
			s.Push(stubDialog{id: "done", result: tc.result})

			_, popped, res := s.Update(keyMsg{})

			if res != tc.result {
				t.Fatalf("result = %v, want %v", res, tc.result)
			}
			if popped == nil {
				t.Fatal("finished dialog was not returned")
			}
			if got := popped.(stubDialog).id; got != "done" {
				t.Fatalf("returned dialog id = %q, want %q", got, "done")
			}
			if got := s.Top().(stubDialog).id; got != "under" {
				t.Fatalf("after pop, top id = %q, want %q", got, "under")
			}
		})
	}

	t.Run("ResultNone keeps the dialog and returns nil", func(t *testing.T) {
		s := New(NewShell(ShellConfig{}))
		s.Push(stubDialog{id: "stay", result: ResultNone})

		_, popped, res := s.Update(keyMsg{})

		if popped != nil || res != ResultNone {
			t.Fatalf("update = (%v, %v), want (nil, ResultNone)", popped, res)
		}
		if !s.Active() {
			t.Fatal("ResultNone popped the dialog")
		}
	})
}

// fakeClock drives a Stack's grace windows without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newGraceStack() (*Stack, *fakeClock) {
	s := New(NewShell(ShellConfig{}))
	c := &fakeClock{t: time.Unix(1000, 0)}
	s.now = c.now
	return s, c
}

// closeGraced pops a grace-pushed dialog cleanly: it advances past the quiet
// window so the closing key is delivered rather than absorbed.
func closeGraced(s *Stack, c *fakeClock, d Dialog) {
	s.PushWithGrace(d)
	c.advance(300 * time.Millisecond)
	s.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
}

func TestStackInputGrace(t *testing.T) {
	t.Run("quiet window gates delivery", func(t *testing.T) {
		cases := []struct {
			name  string
			delay time.Duration
			want  bool // key reaches the dialog
		}{
			{name: "within the window is swallowed", delay: 100 * time.Millisecond, want: false},
			{name: "exactly at the window delivers", delay: graceQuiet, want: true},
			{name: "past the window delivers", delay: 250 * time.Millisecond, want: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var seen []tea.Msg
				s, c := newGraceStack()
				s.PushWithGrace(stubDialog{seen: &seen})

				c.advance(tc.delay)
				s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
				if got := len(seen) == 1; got != tc.want {
					t.Fatalf("after %v the key reached the dialog: %v, want %v", tc.delay, got, tc.want)
				}
				if !s.Active() {
					t.Fatal("the key popped the dialog")
				}
			})
		}
	})

	t.Run("continuous input expires at the ceiling", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		s.PushWithGrace(stubDialog{seen: &seen})

		// A key every 100ms keeps the keyboard louder than the quiet window,
		// so only the ceiling can end the grace. The first delivery must land
		// on the keypress at exactly 1.5s (iteration 15): earlier would mean
		// a shrunken ceiling or a quiet window not refreshed by swallowed
		// keys, later a wedged grace.
		firstDelivered := 0
		for i := 1; i <= 20; i++ {
			c.advance(100 * time.Millisecond)
			before := len(seen)
			s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
			if len(seen) > before && firstDelivered == 0 {
				firstDelivered = i
			}
		}
		if firstDelivered != 15 {
			t.Fatalf("first key delivered at iteration %d, want 15 (the 1.5s ceiling)", firstDelivered)
		}
		if len(seen) != 6 {
			t.Fatalf("delivered %d keys, want 6 (iterations 15-20)", len(seen))
		}
	})

	t.Run("same-kind reopen within the exemption takes keys immediately", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		closeGraced(s, c, stubDialog{result: ResultClose})

		c.advance(100 * time.Millisecond)
		s.PushWithGrace(stubDialog{seen: &seen})
		c.advance(10 * time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(seen) != 1 {
			t.Fatalf("rapid same-kind reopen still ate the key (saw %d)", len(seen))
		}
	})

	t.Run("reopen of a different kind keeps the grace", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		closeGraced(s, c, stubDialog{result: ResultClose})

		c.advance(100 * time.Millisecond)
		s.PushWithGrace(selfFramedStub{seen: &seen})
		c.advance(10 * time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(seen) != 0 {
			t.Fatal("a different dialog kind rode the reopen exemption")
		}
	})

	t.Run("reopen at exactly the exemption window keeps the grace", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		closeGraced(s, c, stubDialog{result: ResultClose})

		c.advance(graceReopenExempt)
		s.PushWithGrace(stubDialog{seen: &seen})
		c.advance(10 * time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(seen) != 0 {
			t.Fatal("a reopen at the exemption boundary skipped the grace")
		}
	})

	t.Run("a plain-pushed close never feeds the exemption", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		s.Push(stubDialog{result: ResultClose})
		s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // user-driven close

		c.advance(100 * time.Millisecond)
		s.PushWithGrace(stubDialog{seen: &seen})
		c.advance(10 * time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(seen) != 0 {
			t.Fatal("a user-closed dialog disarmed an async pick's grace")
		}
	})

	t.Run("plain push has no grace", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		s.Push(stubDialog{seen: &seen})
		c.advance(time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(seen) != 1 {
			t.Fatal("plain push swallowed a key")
		}
	})

	t.Run("non-key messages route during the grace", func(t *testing.T) {
		var seen []tea.Msg
		s, c := newGraceStack()
		s.PushWithGrace(stubDialog{seen: &seen})
		c.advance(10 * time.Millisecond)
		s.Update(keyMsg{}) // not a tea.KeyPressMsg — must pass
		if len(seen) != 1 {
			t.Fatal("non-key message was absorbed by the grace")
		}
	})

	t.Run("a later push ends the grace", func(t *testing.T) {
		var underSeen, topSeen []tea.Msg
		s, c := newGraceStack()
		s.PushWithGrace(stubDialog{seen: &underSeen})
		s.Push(stubDialog{seen: &topSeen})
		c.advance(time.Millisecond)
		s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if len(topSeen) != 1 {
			t.Fatal("stale grace swallowed a key meant for a user-opened dialog")
		}
	})
}

func TestScrollOffset(t *testing.T) {
	cases := []struct {
		name             string
		top, height, vph int
		want             int
	}{
		{name: "region above the fold stays at top", top: 0, height: 3, vph: 10, want: 0},
		{name: "region straddling the fold scrolls just enough", top: 8, height: 3, vph: 10, want: 1},
		{name: "region far below scrolls to reveal its bottom", top: 20, height: 2, vph: 10, want: 12},
		{name: "region taller than the window keeps its top", top: 5, height: 20, vph: 10, want: 5},
		{name: "exact bottom fit needs no scroll", top: 8, height: 2, vph: 10, want: 0},
		{name: "zero height counts as one line", top: 10, height: 0, vph: 10, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrollOffset(tc.top, tc.height, tc.vph); got != tc.want {
				t.Fatalf("scrollOffset(%d,%d,%d) = %d, want %d", tc.top, tc.height, tc.vph, got, tc.want)
			}
		})
	}
}

// scrollHintStub is a stubDialog that reports a scroll region, so the Shell's
// focus-follows scrolling is observable: its ScrollTo names the body line to
// keep visible.
type scrollHintStub struct {
	stubDialog
	top, height int
}

func (d scrollHintStub) ScrollTo() (int, int, bool) { return d.top, d.height, true }

func TestShellFollowsFocus(t *testing.T) {
	// A 30-line body in a viewport far shorter than it: without a scroll hint the
	// Shell shows the top lines only. The hint points near the bottom, so the
	// Shell must scroll that line into view.
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	body := strings.Join(lines, "\n")
	shell := NewShell(ShellConfig{
		MaxHeight: 12,
		Styles:    Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
	})
	backdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 40)+"\n", 40), "\n")

	t.Run("no hint anchors at the top", func(t *testing.T) {
		s := New(shell)
		s.Push(stubDialog{body: body})
		got := s.View(backdrop, 40, 40)
		if !strings.Contains(got, "L00") {
			t.Fatalf("top of an unscrolled body should show:\n%s", got)
		}
		if strings.Contains(got, "L27") {
			t.Fatalf("an unscrolled body should not reach its bottom:\n%s", got)
		}
	})

	t.Run("a hint scrolls the region into view", func(t *testing.T) {
		s := New(shell)
		s.Push(scrollHintStub{body: body, top: 27, height: 1})
		got := s.View(backdrop, 40, 40)
		if !strings.Contains(got, "L27") {
			t.Fatalf("hinted line should scroll into view:\n%s", got)
		}
		if strings.Contains(got, "L00") {
			t.Fatalf("scrolling to the bottom should leave the top off-screen:\n%s", got)
		}
	})
}

// footeredStub scrolls its body (via the embedded scroll hint) and pins a
// footer, so the Shell's footer-pinning is observable: the footer must show
// even when the body has scrolled its own top off.
type footeredStub struct {
	scrollHintStub
	footer string
}

func (d footeredStub) Footer() string { return d.footer }

func TestShellPinsFooterWhileBodyScrolls(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	shell := NewShell(ShellConfig{
		MaxHeight: 12,
		Styles:    Styles{Box: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
	})
	backdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 40)+"\n", 40), "\n")

	s := New(shell)
	// Focus is on the last body line; the footer must still be pinned on screen.
	s.Push(footeredStub{
		body: strings.Join(lines, "\n"), top: 29, height: 1,
		footer: "PINNED-FOOT",
	})
	got := s.View(backdrop, 40, 40)
	if !strings.Contains(got, "PINNED-FOOT") {
		t.Fatalf("footer must stay pinned while the body scrolls:\n%s", got)
	}
	if !strings.Contains(got, "L29") {
		t.Fatalf("body should have scrolled to the focused last line:\n%s", got)
	}
	if strings.Contains(got, "L00") {
		t.Fatalf("scrolling to the bottom should leave the body top off-screen:\n%s", got)
	}
}

func TestShellClampWidth(t *testing.T) {
	cases := []struct {
		name   string
		cfg    ShellConfig
		screen int
		want   int
	}{
		{name: "no caps returns the screen", cfg: ShellConfig{}, screen: 100, want: 100},
		{name: "margin is kept clear", cfg: ShellConfig{Margin: 6}, screen: 100, want: 94},
		{name: "absolute max binds", cfg: ShellConfig{MaxWidth: 40}, screen: 100, want: 40},
		{name: "fraction binds", cfg: ShellConfig{WidthFraction: 0.5}, screen: 100, want: 50},
		{name: "smallest of all caps wins", cfg: ShellConfig{MaxWidth: 60, WidthFraction: 0.5, Margin: 6}, screen: 100, want: 50},
		{name: "floors at one column", cfg: ShellConfig{Margin: 200}, screen: 10, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewShell(tc.cfg).clampWidth(tc.screen); got != tc.want {
				t.Fatalf("clampWidth = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestShellClampHeight(t *testing.T) {
	cases := []struct {
		name   string
		cfg    ShellConfig
		screen int
		want   int
	}{
		{name: "no caps returns the screen", cfg: ShellConfig{}, screen: 50, want: 50},
		{name: "margin is kept clear", cfg: ShellConfig{Margin: 4}, screen: 50, want: 46},
		{name: "absolute max binds", cfg: ShellConfig{MaxHeight: 30}, screen: 50, want: 30},
		{name: "fraction binds", cfg: ShellConfig{HeightFraction: 0.8}, screen: 50, want: 40},
		{name: "smallest of all caps wins", cfg: ShellConfig{MaxHeight: 30, HeightFraction: 0.8, Margin: 4}, screen: 50, want: 30},
		{name: "floors at one row", cfg: ShellConfig{Margin: 100}, screen: 10, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewShell(tc.cfg).clampHeight(tc.screen); got != tc.want {
				t.Fatalf("clampHeight = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStackView(t *testing.T) {
	// A screenH-row backdrop with no trailing newline, so its measured height
	// is exactly the screen height the view is placed over.
	backdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat("·", 40)+"\n", 12), "\n")

	t.Run("inactive stack returns the backdrop unchanged", func(t *testing.T) {
		s := New(NewShell(ShellConfig{}))
		if got := s.View(backdrop, 40, 12); got != backdrop {
			t.Fatal("inactive View mutated the backdrop")
		}
	})

	t.Run("active stack overlays the dialog body", func(t *testing.T) {
		s := New(NewShell(ShellConfig{MaxWidth: 30, MaxHeight: 8}))
		s.Push(stubDialog{id: "a", title: "hello", body: "the dialog body"})

		got := s.View(backdrop, 40, 12)
		if !strings.Contains(got, "the dialog body") {
			t.Fatal("framed view is missing the dialog body")
		}
		// overlay.Place composites onto the screenH-row backdrop, so a clamped
		// dialog can never make the view taller than the screen.
		if h := lipgloss.Height(got); h != 12 {
			t.Fatalf("framed view height = %d, want the screen height 12", h)
		}
	})

	t.Run("a body taller than the cap stays within the screen", func(t *testing.T) {
		var tall strings.Builder
		for i := range 40 {
			tall.WriteString("line ")
			tall.WriteByte(byte('a' + i%26))
			tall.WriteByte('\n')
		}
		tallBackdrop := strings.TrimSuffix(strings.Repeat(strings.Repeat("·", 40)+"\n", 20), "\n")
		s := New(NewShell(ShellConfig{MaxWidth: 30, MaxHeight: 8}))
		s.Push(stubDialog{id: "tall", body: tall.String()})

		if h := lipgloss.Height(s.View(tallBackdrop, 40, 20)); h != 20 {
			t.Fatalf("framed view height = %d, want the screen height 20", h)
		}
	})
}
