//go:build windows

package prompt_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestWindowsAccessiblePipeInputHonorsContext(t *testing.T) {
	for _, kind := range []string{"text", "confirmation"} {
		for _, deadline := range []bool{false, true} {
			name := kind + "/cancel"
			if deadline {
				name = kind + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				read, write, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()  //nolint:errcheck // test owns both pipe ends
				defer write.Close() //nolint:errcheck // test owns both pipe ends
				if _, err := write.WriteString("entered"); err != nil {
					t.Fatal(err)
				}
				value, answer := "original", false
				var field huh.Field = huh.NewInput().Title("Native text").Value(&value)
				if kind == "confirmation" {
					field = huh.NewConfirm().Title("Native confirmation").Value(&answer)
				}
				entered := make(chan struct{})
				form := prompt.NewForm(windowsObservedAccessibleField{Field: field, entered: entered}).
					WithAccessible(true).WithInput(read).WithOutput(io.Discard)
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				finished := make(chan error, 1)
				go func() { finished <- prompt.RunForm(ctx, form) }()
				select {
				case <-entered:
				case err := <-finished:
					t.Fatalf("native accessible read did not enter: %v", err)
				case <-time.After(2 * time.Second):
					_ = write.Close()
					<-finished
					t.Fatal("native accessible read did not consume its prefix")
				}
				if !deadline {
					cancel()
				}
				select {
				case err := <-finished:
					if !errors.Is(err, want) || value != "original" || answer || prompt.IsInputFailure(err) {
						t.Fatalf("accessible cancellation lost context or published an answer: %v value=%q answer=%v", err, value, answer)
					}
				case <-time.After(2 * time.Second):
					_ = write.Close()
					<-finished
					t.Fatal("canceled accessible input waited for native writer closure")
				}
				if _, err := write.WriteString("reuse"); err != nil {
					t.Fatal(err)
				}
				reused := make([]byte, len("reuse"))
				if _, err := io.ReadFull(read, reused); err != nil || string(reused) != "reuse" {
					t.Fatalf("accessible cancellation damaged borrowed native input: %q %v", reused, err)
				}
			})
		}
	}
}

type windowsObservedAccessibleField struct {
	huh.Field
	entered chan struct{}
}

func (f windowsObservedAccessibleField) RunAccessible(w io.Writer, r io.Reader) error {
	prefix := make([]byte, len("entered"))
	if _, err := io.ReadFull(r, prefix); err != nil {
		return err
	}
	close(f.entered)
	return f.Field.RunAccessible(w, r)
}
