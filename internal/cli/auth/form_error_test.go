package auth

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestLoginFormCancellationPreservesTerminalRestorationFailure(t *testing.T) {
	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded, huh.ErrUserAborted} {
		restore := &prompt.TerminalRestoreError{Err: io.ErrClosedPipe}
		original := errors.Join(canceled, restore)
		got := loginFormError(original)
		if errors.Is(got, errAborted) || !errors.Is(got, io.ErrClosedPipe) || !strings.Contains(got.Error(), "password terminal restoration failed") {
			t.Fatal("login discarded the password terminal restoration failure")
		}
		if !errors.Is(canceled, huh.ErrUserAborted) {
			classification := cli.MapError(got)
			want := cli.ExitCanceled
			if errors.Is(canceled, context.DeadlineExceeded) {
				want = cli.ExitTimeout
			}
			if classification.Exit != want || !strings.Contains(classification.Message, "password terminal restoration failed") {
				t.Fatal("terminal restoration failure lost context classification or diagnostics")
			}
		}
	}
}

func TestLoginFormKeepsOrdinaryCancellationQuiet(t *testing.T) {
	for _, err := range []error{context.Canceled, huh.ErrUserAborted} {
		if !errors.Is(loginFormError(err), errAborted) {
			t.Fatal("ordinary canceled login lost its quiet abort")
		}
	}
	if loginFormError(nil) != nil || !errors.Is(loginFormError(io.ErrUnexpectedEOF), io.ErrUnexpectedEOF) {
		t.Fatal("login changed an unrelated form outcome")
	}
}

func TestAuthenticationConfirmationRetainsInputFailures(t *testing.T) {
	for _, cause := range []error{
		&prompt.InputCleanupError{Err: io.ErrClosedPipe},
		&prompt.InputReadError{Err: io.ErrClosedPipe},
		&prompt.TerminalRestoreError{Err: io.ErrClosedPipe},
	} {
		for _, interrupted := range []error{context.Canceled, context.DeadlineExceeded, huh.ErrUserAborted} {
			original := errors.Join(interrupted, cause)
			ask := func(context.Context, prompt.Confirm) (bool, error) { return true, original }
			ok, logoutErr := confirmLogout(t.Context(), ask, "fixture@example.invalid", "https://example.invalid")
			if ok {
				t.Fatal("failed logout confirmation authorized removal")
			}
			for _, err := range []error{loginFormError(original), logoutErr} {
				if errors.Is(err, errAborted) || !errors.Is(err, interrupted) || !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("authentication discarded input failure: %v", err)
				}
			}
		}
	}
}
