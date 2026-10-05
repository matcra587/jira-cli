package prompt_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestAccessibleFormPreservesQueuedAnswers(t *testing.T) {
	var first, second string
	form := prompt.NewForm(
		huh.NewInput().Title("First").Value(&first),
		huh.NewInput().Title("Second").Value(&second),
	).WithAccessible(true).WithInput(strings.NewReader("first answer\nsecond answer\n")).WithOutput(io.Discard)
	if err := prompt.RunForm(t.Context(), form); err != nil || first != "first answer" || second != "second answer" {
		t.Fatalf("queued native answers: first=%q second=%q error=%v", first, second, err)
	}
}

func TestAccessibleFormRequiresACompleteAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{name: "complete affirmative", input: "yes\n", valid: true},
		{name: "complete default", input: "\n", valid: true},
		{name: "incomplete affirmative", input: "yes"},
		{name: "incomplete negative", input: "no"},
		{name: "empty EOF"},
		{name: "invalid then incomplete affirmative", input: "unknown\nyes"},
		{name: "overlong affirmative", input: "yes" + strings.Repeat(" ", 70*1024) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer, later := true, "unchanged"
			var screen bytes.Buffer
			form := prompt.NewForm(
				huh.NewConfirm().Title("Remove fixture?").Value(&answer),
				huh.NewInput().Title("Later field").Value(&later),
			).WithAccessible(true).WithInput(strings.NewReader(tc.input + "later answer\n")).WithOutput(&screen)
			if !tc.valid {
				// EOF is the boundary under test; later input would turn the
				// unfinished answer into a different, completed line.
				form.WithInput(strings.NewReader(tc.input))
			}
			err := prompt.RunForm(t.Context(), form)
			if tc.valid {
				if err != nil || !answer || later != "later answer" {
					t.Fatalf("complete native answer rejected: answer=%v later=%q error=%v", answer, later, err)
				}
				return
			}
			var readErr *prompt.InputReadError
			if !errors.As(err, &readErr) || later != "unchanged" || strings.Contains(screen.String(), "Later field") {
				t.Fatal("invalid native answer lost its read failure or advanced to another field")
			}
		})
	}
}

func TestAccessibleFormAcceptsCompleteLineWithEOF(t *testing.T) {
	var answer bool
	form := prompt.NewForm(huh.NewConfirm().Title("Remove fixture?").Value(&answer)).
		WithAccessible(true).WithInput(eofAfterNewline{reader: strings.NewReader("yes\n")}).WithOutput(io.Discard)
	if err := prompt.RunForm(t.Context(), form); err != nil || !answer {
		t.Fatalf("complete final line and EOF: answer=%v error=%v", answer, err)
	}
}

func TestAccessibleFormRejectsInvalidCompleteLineWithEOF(t *testing.T) {
	value := "https://example.invalid"
	form := prompt.NewForm(huh.NewInput().Title("Site").Value(&value).Validate(func(answer string) error {
		if !strings.HasPrefix(answer, "https://") {
			return errors.New("site requires HTTPS")
		}
		return nil
	})).WithAccessible(true).WithInput(eofAfterNewline{reader: strings.NewReader("invalid-site\n")}).WithOutput(io.Discard)
	var readErr *prompt.InputReadError
	if err := prompt.RunForm(t.Context(), form); !errors.As(err, &readErr) || !errors.Is(err, io.EOF) {
		t.Fatalf("native validation retry lost final EOF: %v", err)
	}
}

type eofAfterNewline struct{ reader io.Reader }

func (r eofAfterNewline) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n != 0 && p[n-1] == '\n' {
		return n, io.EOF
	}
	return n, err
}

func TestAccessibleFormRetainsReaderFailure(t *testing.T) {
	failure := errors.New("synthetic input failure")
	value := "stored@example.invalid"
	form := prompt.NewForm(prompt.WithAccessibleDefault(huh.NewInput().Title("Email").Value(&value), func(string) error { return nil })).
		WithAccessible(true).WithInput(failingFormReader{err: failure}).WithOutput(io.Discard)
	var readErr *prompt.InputReadError
	err := prompt.RunForm(t.Context(), form)
	if !errors.As(err, &readErr) || !errors.Is(err, failure) {
		t.Fatalf("accessible reader failure was discarded: %v", err)
	}
}

type failingFormReader struct{ err error }

func (r failingFormReader) Read([]byte) (int, error) { return 0, r.err }
