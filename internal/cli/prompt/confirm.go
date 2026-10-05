package prompt

import (
	"context"
	"errors"
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/clib/theme"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Confirm is a yes/no gate in front of something the caller cannot undo.
//
// It exists as one object rather than a form assembled per command so
// that every dialog in the CLI looks and behaves the same by
// construction. Assembled ad hoc, the first one styled its title and
// left huh's own palette on the buttons, so a themed question sat above
// two buttons in a different color scheme entirely.
type Confirm struct {
	// Question is the sentence, built with [Question] so the values in
	// it are highlighted.
	Question string
	// Caveat qualifies the question in the quieter register — what will
	// STILL be true afterwards. Optional.
	Caveat string
	// Affirmative and Negative label the buttons. Name the act ("Remove",
	// "Keep"), never "Yes" and "No": the labels are read after the
	// question and have to make sense alone.
	Affirmative string
	Negative    string
}

// Run draws the dialog and reports the answer.
//
// A caller context error is preserved. Otherwise [huh.ErrUserAborted] means
// the person closed the form, which is a choice rather than a failure; the
// caller decides what that means for its exit code.
func Run(ctx context.Context, c Confirm) (bool, error) {
	// A dialog with no question is unanswerable, and the zero value of
	// this struct would draw one: an empty title over two empty
	// buttons. Refusing is better than asking someone to guess.
	if strings.TrimSpace(c.Question) == "" {
		return false, errors.New("confirm: no question to ask")
	}
	// The labels DO have a sensible default, so the zero value is
	// usable once a question is set rather than a second thing to
	// remember. Named actions still beat these — see the field docs.
	if c.Affirmative == "" {
		c.Affirmative = "Yes"
	}
	if c.Negative == "" {
		c.Negative = "No"
	}

	styles := dialogStyles()

	description := ""
	if c.Caveat != "" {
		description = styles.Focused.Description.Render(c.Caveat)
	}

	answer := false
	// Set the stream explicitly: Huh's accessible mode otherwise uses stdout
	// even though its normal renderer defaults to stderr.
	form := NewForm(
		&confirmationField{Confirm: huh.NewConfirm().
			Title(c.Question).
			Description(description).
			Affirmative(c.Affirmative).
			Negative(c.Negative).
			Value(&answer), caveat: description},
		// ThemeFunc ignores the isDark huh offers: huh learns it from a
		// Bubble Tea background-color message, which arrives only if
		// the terminal answers, and the resolved theme already knows.
	).WithOutput(os.Stderr).WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return styles }))
	err := RunForm(ctx, form)
	if err != nil {
		return false, err
	}
	return answer, nil
}

// confirmationField retains Huh's rich rendering and input behavior. Huh
// v2.0.3 omits Confirm.Description in accessible mode, so a native note must
// show the caveat before the native yes/no reader asks for an answer.
type confirmationField struct {
	*huh.Confirm
	caveat string
}

func (f *confirmationField) RunAccessible(w io.Writer, r io.Reader) error {
	if f.caveat != "" {
		if err := huh.NewNote().Description(f.caveat).RunAccessible(w, r); err != nil {
			return err
		}
	}
	return f.Confirm.RunAccessible(w, r)
}

// Question renders a dialog sentence from alternating parts: the first
// is plain, the second is a value to check, and so on.
//
//	Question("Remove the stored token for ", account, " on ", site, "?")
//
// Alternating rather than a format string because EVERY segment has to
// carry a style, including the plain ones. A styled span emits its own
// escape sequence and resets at the end, so styling only the highlights
// leaves everything after them at the terminal default — which produced
// a title reading indigo, white, indigo, white. Building the sentence
// here makes that structural instead of something each caller has to
// remember.
func Question(parts ...string) string {
	// The two roles a question has: the sentence, and the values in it.
	//
	// The highlight is the theme's CODE role — the same color an inline
	// `code` span gets in every error hint, and the same one the
	// selected button carries. A value to read exactly is the same kind
	// of thing wherever it appears, and taking it from a different role
	// than the buttons put two accents in one dialog. Bold on top,
	// because a theme may give the role a color and no weight.
	plain := dialogStyles().Focused.Title
	highlight := plain.Bold(true)
	if accent := dialogAccentColor(cli.ResolvedTheme()); accent != nil {
		highlight = highlight.Foreground(accent)
	}

	var b strings.Builder
	for i, part := range parts {
		style := plain
		if i%2 == 1 {
			style = highlight
		}
		b.WriteString(style.Render(part))
	}
	return b.String()
}

// dialogStyles maps the CLI's resolved theme onto huh's.
//
// huh v2's themes take an isDark flag, and its guide suggests getting
// it from lipgloss.HasDarkBackground() — the OSC 11 terminal probe that
// costs five seconds against a terminal that stays quiet, and which
// tests/guardrails bans. The resolved theme already carries the answer,
// so it comes from there and nothing is asked.
//
// ThemeBase itself ignores the flag (its parameter is unnamed and
// unread). It is still computed and passed honestly, because the
// signature asks for it and any other base theme would use it — but no
// behavior here turns on it today.
func dialogStyles() *huh.Styles {
	th := cli.ResolvedTheme()
	styles := huh.ThemeBase(cli.BackgroundIsDark(th))

	// The sentence takes the theme's prose style and the caveat under it
	// the dim one, so a dialog reads in the same voice as everything
	// else the command says.
	if th.MarkdownText != nil {
		styles.Focused.Title = *th.MarkdownText
		styles.Blurred.Title = *th.MarkdownText
	}
	if th.Dim != nil {
		dim := th.Dim.Italic(true)
		styles.Focused.Description = dim
		styles.Blurred.Description = dim
	}

	// The selected button carries the accent as its BACKGROUND, so the
	// choice being made is the thing the eye lands on. The unselected
	// one stays dim: both are legible, only one is loud.
	//
	// The accent is the color an inline `code` span already gets in
	// every error hint — a value to read exactly is the same kind of
	// thing wherever it appears.
	if accent := dialogAccentColor(th); accent != nil {
		styles.Focused.FocusedButton = styles.Focused.FocusedButton.
			Background(accent).Foreground(lipgloss.Color("0")).Bold(true)
		styles.Blurred.FocusedButton = styles.Focused.FocusedButton
	}
	if th.Dim != nil {
		blurred := styles.Focused.BlurredButton.Foreground(th.Dim.GetForeground())
		styles.Focused.BlurredButton = blurred
		styles.Blurred.BlurredButton = blurred
	}
	return styles
}

// dialogAccentColor is the theme's code role, or nil when the theme
// leaves it unset.
//
// lipgloss returns NoColor{} rather than nil for a style with no
// foreground, so "unset" has to be tested for explicitly — a nil check
// alone never fires, and painting a button NoColor{} makes it
// unreadable rather than unstyled.
func dialogAccentColor(th *theme.Theme) color.Color {
	if th.MarkdownCode == nil {
		return nil
	}
	fg := th.MarkdownCode.GetForeground()
	if fg == (lipgloss.NoColor{}) {
		return nil
	}
	return fg
}
