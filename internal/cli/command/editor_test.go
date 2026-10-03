package command

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestEditorCanceledBeforeLaunchDoesNotBorrowTerminals(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runEditor(ctx, []string{"must-not-start"}, "unused", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled editor reached launch or terminal setup: %v", err)
	}
}

func TestEditorRefusesMissingCommandOrTerminals(t *testing.T) {
	t.Parallel()
	for _, editor := range [][]string{nil, {}, {""}} {
		err := runEditor(context.Background(), editor, "unused", nil, nil)
		if classification := cli.MapError(err); classification.Code != cli.CodeEditorMissing {
			t.Fatalf("empty editor was not classified: %v", err)
		}
	}
	err := runEditor(context.Background(), []string{"must-not-start"}, "unused", nil, nil)
	if classification := cli.MapError(err); classification.Code != cli.CodeTerminalRequired {
		t.Fatalf("missing terminal reached editor launch: %v", err)
	}
}
