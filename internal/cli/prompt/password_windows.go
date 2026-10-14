//go:build windows

package prompt

import (
	"context"
	"errors"
	"io"

	"charm.land/huh/v2"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

// Keep Huh's native password reader and masking. The shared console owner pins
// its reading thread and joins cancellation before restoring borrowed modes.
func runAccessiblePassword(ctx context.Context, field *huh.Input, w io.Writer, r io.Reader) error {
	err, cleanupErr := input.RunTerminalContext(ctx, r, func(reader io.Reader) error {
		return field.RunAccessible(w, reader)
	})
	// The owner returns expected cancellation as the exact context sentinel.
	// A joined failure can also match context and must survive quiet abortion.
	if err != nil && err != ctx.Err() { //nolint:errorlint // Joined independent failures must survive quiet context abortion.
		err = &InputReadError{Err: err}
	}
	if cleanupErr != nil {
		err = errors.Join(err, &TerminalRestoreError{Err: cleanupErr})
	}
	return err
}
