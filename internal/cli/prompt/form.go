package prompt

import (
	"context"
	"errors"
)

// RunForm runs a form with the caller's context and preserves its error.
// Huh maps Bubble Tea cancellation to ErrTimeout, so the context must supply
// the cancellation/deadline distinction used by the CLI's exit contract.
// NewForm preserves accessible field errors and supplies owned stream readers for
// text and confirmation input. WithPasswordContext owns native password reads.
func RunForm(ctx context.Context, form interface {
	RunWithContext(context.Context) error
},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := form.RunWithContext(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		if IsInputFailure(err) {
			return errors.Join(err, contextErr)
		}
		return contextErr
	}
	return err
}
