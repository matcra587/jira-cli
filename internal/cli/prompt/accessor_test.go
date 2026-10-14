package prompt_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestContextAccessorRefusesRejectedTextAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	value := "original"
	validated := false
	field := huh.NewInput().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&value))).
		Validate(func(candidate string) error {
			validated = candidate == "rejected"
			cancel()
			return errors.New("authored rejection")
		})
	// Huh returns its earlier rejected answer when the next scan sees EOF.
	// Exercise that native setter rather than invoking the accessor directly.
	err := field.RunAccessible(io.Discard, strings.NewReader("rejected\n"))
	if err != nil || !validated || value != "original" || field.GetValue() != "original" {
		t.Fatalf("canceled validation retry published its rejected answer: value=%q validated=%v error=%v", value, validated, err)
	}
}

func TestContextAccessorPreservesConfirmationPublication(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "canceled"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			answer := false
			field := huh.NewConfirm().Accessor(prompt.WithContextAccessor(ctx, huh.NewPointerAccessor(&answer)))
			var reader io.Reader = strings.NewReader("yes\n")
			if canceled {
				reader = passwordPublicationReader{reader: reader, cancel: cancel}
			}
			if err := field.RunAccessible(io.Discard, reader); err != nil || answer == canceled || field.GetValue() != answer {
				t.Fatalf("native confirmation publication changed: answer=%v canceled=%v error=%v", answer, canceled, err)
			}
		})
	}
}
