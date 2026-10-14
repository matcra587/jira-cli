package prompt

import (
	"context"
	"errors"
	"fmt"
	"io"

	"charm.land/huh/v2"
)

// TerminalRestoreError reports a failure to restore a borrowed password terminal.
// It must remain visible even when the input was canceled successfully.
type TerminalRestoreError struct {
	Err error
}

func (e *TerminalRestoreError) Error() string {
	return fmt.Sprintf("password terminal restoration failed: %v", e.Err)
}

// Unwrap preserves the operating system's restoration failure.
func (e *TerminalRestoreError) Unwrap() error {
	return e.Err
}

// PasswordInput retains Huh's password field and its accessible read error.
// Huh v2.0.3 discards accessible field errors, so callers must check Err after
// RunForm before using the password. A field is owned by one form at a time.
type PasswordInput struct {
	*huh.Input
	ctx context.Context //nolint:containedctx // Huh's Field.RunAccessible has no context parameter; this adapter lives for one form.
	err error
}

// WithPasswordContext gives a native password field the command's context.
// The explicit accessor retains native Huh binding while preventing a canceled
// read from publishing a partial password: native terminal cancellation can
// appear as EOF, which Huh's password reader otherwise accepts after some bytes.
// Linux and Windows accessible reads interrupt cancellation and restore the
// borrowed terminal. Other platforms check context before and after input.
// The form must exclusively own the terminal throughout the password read.
func WithPasswordContext(ctx context.Context, input *huh.Input, accessor huh.Accessor[string]) *PasswordInput {
	input.Accessor(WithContextAccessor(ctx, accessor))
	return &PasswordInput{Input: input, ctx: ctx}
}

// RunAccessible preserves native Huh masking and records its read outcome.
func (f *PasswordInput) RunAccessible(w io.Writer, r io.Reader) error {
	if err := f.ctx.Err(); err != nil {
		f.err = err
		return err
	}
	f.err = errors.Join(runAccessiblePassword(f.ctx, f.Input, w, r), f.ctx.Err())
	return f.err
}

// Err returns the accessible read error, or nil for a successful or rich form.
func (f *PasswordInput) Err() error {
	return f.err
}
