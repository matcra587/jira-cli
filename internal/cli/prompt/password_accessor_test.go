package prompt_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestPasswordAccessorRefusesPublicationAfterReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	value := "original"
	field := prompt.WithPasswordContext(ctx, huh.NewInput(), huh.NewPointerAccessor(&value))
	// Exercise Huh's actual setter after a successful read canceled its context.
	// Plain input isolates publication from platform-specific password masking.
	err := field.Input.RunAccessible(io.Discard, passwordPublicationReader{reader: strings.NewReader("authored\n"), cancel: cancel})
	if err != nil || ctx.Err() == nil || value != "original" || field.GetValue() != "original" {
		t.Fatalf("canceled native setter published its candidate: value=%q error=%v", value, err)
	}
}

func TestPasswordAccessorKeepsNativeBindingOnSuccess(t *testing.T) {
	value := "original"
	field := prompt.WithPasswordContext(t.Context(), huh.NewInput(), huh.NewPointerAccessor(&value))
	if err := field.Input.RunAccessible(io.Discard, strings.NewReader("authored\n")); err != nil || value != "authored" || field.GetValue() != "authored" {
		t.Fatalf("successful native setter lost its binding: value=%q error=%v", value, err)
	}
}

type passwordPublicationReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r passwordPublicationReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.cancel()
	return n, err
}
