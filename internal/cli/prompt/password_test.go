package prompt_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"
	"go.uber.org/goleak"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestExpiredPasswordContextRendersNothing(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
				want = context.DeadlineExceeded
			}
			defer cancel()
			var value string
			field := prompt.WithPasswordContext(ctx, huh.NewInput().EchoMode(huh.EchoModePassword).Value(&value))
			var screen bytes.Buffer
			if err := field.RunAccessible(&screen, strings.NewReader("synthetic-password\n")); !errors.Is(err, want) || !errors.Is(field.Err(), want) {
				t.Fatalf("expired password context: error=%v, saved=%v", err, field.Err())
			}
			if screen.Len() != 0 || value != "" {
				t.Fatal("expired password context rendered or accepted input")
			}
		})
	}
}

func TestPasswordFieldPreservesAccessibleReadError(t *testing.T) {
	var value string
	field := prompt.WithPasswordContext(t.Context(), huh.NewInput().EchoMode(huh.EchoModePassword).Value(&value))
	var screen bytes.Buffer
	form := huh.NewForm(huh.NewGroup(field)).WithAccessible(true).WithInput(strings.NewReader("synthetic-password\n")).WithOutput(&screen)
	if err := prompt.RunForm(t.Context(), form); err != nil {
		t.Fatalf("fixture no longer reproduces Huh's discarded field error: %v", err)
	}
	if field.Err() == nil || value != "" || strings.Contains(screen.String(), "synthetic-password") {
		t.Fatal("password read error was lost or a nonterminal input was accepted")
	}
}
