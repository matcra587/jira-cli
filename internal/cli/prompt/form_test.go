package prompt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"
)

func TestExpiredFormContextRendersNothing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		deadline bool
	}{
		{name: "cancel", deadline: false},
		{name: "deadline", deadline: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if tt.deadline {
				ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			defer cancel()
			var screen bytes.Buffer
			form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Remove fixture?"))).
				WithAccessible(true).WithInput(strings.NewReader("y\n")).WithOutput(&screen)
			if err := RunForm(ctx, form); !errors.Is(err, want) {
				t.Fatalf("expired form error=%v, want %v", err, want)
			}
			if screen.Len() != 0 {
				t.Fatal("expired form rendered a question")
			}
		})
	}
}

func TestFormCancellationDuringAnswerIsNotSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var answer bool
	var screen bytes.Buffer
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Remove fixture?").Value(&answer))).
		WithAccessible(true).WithInput(cancelingFormReader{reader: strings.NewReader("y\n"), cancel: cancel}).WithOutput(&screen)
	if err := RunForm(ctx, form); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled answer error=%v, want context cancellation", err)
	}
	if !answer {
		t.Fatal("fixture did not submit an affirmative answer")
	}
}

type cancelingFormReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r cancelingFormReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.cancel()
	return n, err
}

func TestRunFormRetainsInputFailureAfterCancellation(t *testing.T) {
	for _, cause := range []error{
		&InputCleanupError{Err: io.ErrClosedPipe},
		&InputReadError{Err: io.ErrClosedPipe},
		&TerminalRestoreError{Err: io.ErrClosedPipe},
	} {
		ctx, cancel := context.WithCancel(t.Context())
		form := failingContextForm{cancel: cancel, err: cause}
		err := RunForm(ctx, form)
		cancel()
		if !errors.Is(err, context.Canceled) || !errors.Is(err, io.ErrClosedPipe) || !IsInputFailure(err) {
			t.Fatalf("form discarded input failure after cancellation: %v", err)
		}
	}
}

type failingContextForm struct {
	cancel context.CancelFunc
	err    error
}

func (f failingContextForm) RunWithContext(context.Context) error {
	f.cancel()
	return f.err
}
