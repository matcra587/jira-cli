package issue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// --no-input is the non-interactive guarantee, and on an agent-first CLI
// it is the flag that has to be true rather than nearly true: a caller who
// passes it is promising a harness with nobody at the keyboard, and a
// command that prompts anyway hangs that harness until something kills it.
//
// It is enforced at ONE seam. command.interactive() answers false when the
// flag is set, and every prompt in this CLI is already gated on
// Invocation.Interactive — the destructive confirmations here, and both of
// auth's. That is why there is no second mechanism and no per-command
// check: the guarantee is whole because the gate is single.

// TestNoInputRefusesTheConfirmationInsteadOfPrompting: the refusal must
// happen, carry the flag-required code, and point at --force, which is the
// one thing that makes the command work non-interactively.
func TestNoInputRefusesTheConfirmationInsteadOfPrompting(t *testing.T) {
	asked := false
	ask := func(_ context.Context, _ prompt.Confirm) (bool, error) { asked = true; return true, nil }

	err := confirmDestructive(t.Context(), ask, command.Invocation{Interactive: false, NoInput: true}, false, destructiveQuestion{
		Subject: "comment 10042",
	})
	if err == nil {
		t.Fatal("--no-input allowed a destructive action through without confirmation")
	}
	if asked {
		t.Error("--no-input still opened a dialog; a harness would hang here")
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("refusal is %T, want a typed *cli.InputError so the envelope carries a code", err)
	}
	if input.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q", input.Code, cli.CodeFlagRequired)
	}
	if input.Field != command.FlagForce {
		t.Errorf("field = %q, want %q — the flag that lets this proceed", input.Field, command.FlagForce)
	}
}

// TestNoInputRefusalNamesTheFlagRatherThanBlamingTheTerminal: the two ways
// to be non-interactive need different sentences. Telling someone who
// typed --no-input at a working terminal that there is "no terminal here"
// sends them to debug a TTY that is fine.
func TestNoInputRefusalNamesTheFlagRatherThanBlamingTheTerminal(t *testing.T) {
	q := destructiveQuestion{Subject: "comment 10042"}

	byFlag := confirmDestructive(t.Context(), nil, command.Invocation{Interactive: false, NoInput: true}, false, q)
	if byFlag == nil || !strings.Contains(byFlag.Error(), "--"+cli.FlagNoInput) {
		t.Errorf("refusal under --no-input = %v, want it to name --%s", byFlag, cli.FlagNoInput)
	}
	if byFlag != nil && strings.Contains(byFlag.Error(), "no terminal here") {
		t.Errorf("refusal under --no-input blames the terminal: %v", byFlag)
	}

	byPipe := confirmDestructive(t.Context(), nil, command.Invocation{Interactive: false}, false, q)
	if byPipe == nil || !strings.Contains(byPipe.Error(), "no terminal here") {
		t.Errorf("refusal without a terminal = %v, want the terminal wording", byPipe)
	}
}

// TestForceStillBeatsNoInput: --force is documented as the ONE thing a
// non-interactive caller has to add, and that has to hold for the caller
// who arrived non-interactively on purpose.
func TestForceStillBeatsNoInput(t *testing.T) {
	err := confirmDestructive(t.Context(), nil, command.Invocation{Interactive: false, NoInput: true}, true, destructiveQuestion{
		Subject: "comment 10042",
	})
	if err != nil {
		t.Errorf("--force with --no-input = %v, want the action allowed", err)
	}
}
