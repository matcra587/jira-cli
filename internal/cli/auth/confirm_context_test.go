package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestLogoutConfirmationRejectsAnExpiredAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ask := func(received context.Context, _ prompt.Confirm) (bool, error) {
		if received != ctx {
			t.Fatal("logout confirmation did not receive the command context")
		}
		cancel()
		return true, nil
	}
	if ok, err := confirmLogout(ctx, ask, "fixture@example.invalid", "https://example.invalid"); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("expired affirmative logout answer allowed through: ok=%v error=%v", ok, err)
	}
}

func TestLogoutConfirmationRejectsExpiredContextBeforeAssumingYes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deps := Deps{Confirm: func(context.Context, prompt.Confirm) (bool, error) {
		t.Fatal("expired logout opened a confirmation")
		return true, nil
	}}
	for _, assumeYes := range []bool{false, true} {
		err := confirmRemoval(ctx, deps, command.Invocation{Interactive: true}, SiteInfo{}, "fixture@example.invalid", assumeYes)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("assumeYes=%v allowed expired logout: %v", assumeYes, err)
		}
	}
}
