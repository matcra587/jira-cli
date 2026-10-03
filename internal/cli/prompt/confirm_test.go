package prompt

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

// TestTheDialogThemesItsButtonsToo pins the gap that made a themed
// question sit above huh's own palette: the styles handed to the form
// must carry the resolved theme on the BUTTONS, not only the title.
func TestTheDialogThemesItsButtonsToo(t *testing.T) {
	th := cli.ResolvedTheme()
	if th.MarkdownCode == nil {
		t.Skip("the resolved theme sets no code role, so there is no accent to check")
	}
	want := th.MarkdownCode.GetForeground()

	got := dialogStyles().Focused.FocusedButton.GetBackground()
	if got != want {
		t.Errorf("selected button background = %v, want the theme's accent %v — "+
			"huh's own palette is showing through", got, want)
	}
}

// TestAQuestionStylesEverySegment pins that the plain parts are styled
// too. A styled span resets at its end, so an unstyled plain part
// leaves the rest of the line at the terminal default.
func TestAQuestionStylesEverySegment(t *testing.T) {
	got := Question("Remove the token for ", "a@b.c", " on ", "https://x", "?")
	if n := strings.Count(got, "\x1b["); n < 5 {
		t.Errorf("only %d escape sequences for 5 segments: %q", n, got)
	}
}

// TestTheZeroValueRefusesRatherThanAsksNothing pins the guard: an empty
// question would draw an unanswerable dialog.
func TestTheZeroValueRefusesRatherThanAsksNothing(t *testing.T) {
	if _, err := (Confirm{}).Run(); err == nil {
		t.Error("a dialog with no question was accepted")
	}
}

// TestOneAccentThroughout pins that the values in a question and the
// selected button carry the SAME color.
//
// They were taken from different theme roles — the question from the
// text role bolded, the button from the code role — which put two
// accents in one dialog for no reason a reader could infer.
func TestOneAccentThroughout(t *testing.T) {
	accent := dialogAccentColor(cli.ResolvedTheme())
	if accent == nil {
		t.Skip("the resolved theme sets no code role, so there is no accent to compare")
	}
	if got := dialogStyles().Focused.FocusedButton.GetBackground(); got != accent {
		t.Errorf("button background = %v, want the accent %v", got, accent)
	}
	// And the highlighted segment of a question must carry it too. The
	// rendered value is compared against the same text rendered with
	// only the accent applied, so this fails if the question's
	// highlight drifts to another role.
	rendered := Question("", "value")
	wanted := dialogStyles().Focused.Title.Bold(true).Foreground(accent).Render("value")
	if !strings.Contains(rendered, wanted) {
		t.Errorf("highlighted value does not use the accent:\n got %q\nwant it to contain %q", rendered, wanted)
	}
}
