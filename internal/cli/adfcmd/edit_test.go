package adfcmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestAnchoredEditedInputRefusesTerminalBeforeReading(t *testing.T) {
	t.Parallel()
	_, err := readTextInput(t.Context(), command.Invocation{Stdin: unreadableInput{}, StdinIsTerminal: true}, flagEdited, "-", true)
	failure, ok := errors.AsType[*cli.InputError](err)
	if !ok || failure.Field != flagEdited || failure.Code != cli.CodeFlagValueInvalid {
		t.Fatal("edited input read terminal or lost field")
	}
}

func TestAnchoredEditedInputCancellationPreventsIO(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readTextInput(ctx, command.Invocation{Stdin: unreadableInput{}}, flagEdited, "-", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled edited input attempted IO")
	}
}

func TestAnchoredEmptySourceIsAccepted(t *testing.T) {
	t.Parallel()
	doc, err := readEditDocument(t.Context(), command.Invocation{Stdin: strings.NewReader(`{"type":"doc","version":1,"content":[]}`)}, flagInput, "-")
	if err != nil || doc == nil {
		t.Fatal("valid empty source was refused")
	}
}
