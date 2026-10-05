package issue

import (
	"context"
	"errors"
	"io"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestDestructiveConfirmationRejectsAnExpiredAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	asked := false
	ask := func(received context.Context, _ prompt.Confirm) (bool, error) {
		asked = true
		if received != ctx {
			t.Fatal("confirmation did not receive the command context")
		}
		cancel()
		return true, nil
	}
	err := confirmDestructive(ctx, ask, command.Invocation{Interactive: true}, false, destructiveQuestion{Subject: "fixture"})
	if !asked || !errors.Is(err, context.Canceled) {
		t.Fatalf("expired affirmative answer allowed through: asked=%v error=%v", asked, err)
	}
}

func TestIssueConfirmationRetainsInputFailures(t *testing.T) {
	for _, cause := range []error{
		&prompt.InputCleanupError{Err: io.ErrClosedPipe},
		&prompt.InputReadError{Err: io.ErrClosedPipe},
		&prompt.TerminalRestoreError{Err: io.ErrClosedPipe},
	} {
		for _, interrupted := range []error{context.Canceled, context.DeadlineExceeded, huh.ErrUserAborted} {
			ask := func(context.Context, prompt.Confirm) (bool, error) {
				return true, errors.Join(interrupted, cause)
			}
			inv := command.Invocation{Interactive: true}
			outcomes := []error{
				confirmDestructive(t.Context(), ask, inv, false, destructiveQuestion{Subject: "fixture"}),
				confirmSelected(t.Context(), ask, inv, false, selectedWrite{Gerund: "editing", Imperative: "Edit"}, command.Targets{
					Keys: []string{"KEY-1"}, Summary: &envelope.SelectorSummary{Kind: "jql", Resolved: true},
				}),
			}
			for _, err := range outcomes {
				if errors.Is(err, errDestructiveAborted) || !errors.Is(err, interrupted) || !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("confirmation discarded an input failure: %v", err)
				}
			}
		}
	}
}

func TestDestructiveConfirmationRefusesAnExpiredContextBeforeAsking(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ask := func(context.Context, prompt.Confirm) (bool, error) {
		t.Fatal("expired command opened a confirmation")
		return true, nil
	}
	for _, force := range []bool{false, true} {
		err := confirmDestructive(ctx, ask, command.Invocation{Interactive: true}, force, destructiveQuestion{Subject: "fixture"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("force=%v allowed an expired command: %v", force, err)
		}
	}
}
