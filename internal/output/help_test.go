package output

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestRenderHelpCancellationPreventsCallback(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := RenderHelp(ctx, io.Discard, func(io.Writer) error {
		t.Fatal("canceled native help invoked its renderer")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("native help lost its cancellation cause")
	}
}
