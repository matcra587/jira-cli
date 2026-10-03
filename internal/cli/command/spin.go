package command

import (
	"context"
	"time"

	"github.com/gechr/clog"
)

// spinnerDelay is how long a blocking call may run before a spinner
// appears.
//
// Below it the call returns first and nothing is drawn — a fetch that
// comes back fast does not flash a spinner the eye only registers as a
// flicker. Above it the network was slow enough to be worth telling the
// person the CLI is still working. clog's After does exactly this: if
// the task finishes first, no live region is ever painted.
const spinnerDelay = 120 * time.Millisecond

// Spin runs a blocking operation behind a spinner and returns its
// result.
//
// Every call that waits on Jira goes through here, so waiting looks the
// same everywhere and no command reinvents it. What each part buys,
// verified against clog rather than assumed:
//
//   - After(spinnerDelay): a fast response paints nothing; a slow one
//     gets a spinner. No flicker, no lost result.
//   - NonTTYSilent(true): a pipe or an agent gets NOTHING drawn. A
//     spinner is repaint control sequences, and on a non-terminal those
//     are corruption in the stream — and the envelope is on stdout while
//     the spinner rides clog's stderr, so the data is untouched anyway.
//   - the closure takes its OWN context and passes it onward, so the
//     call runs under the spinner's cancellation and Ctrl-C stops the
//     work, not just the animation.
//   - Silent(): on success nothing is logged — the command's own output
//     is the result, and a "done" line would clutter stderr beside it.
//     On failure the error returns unlogged, because the root's boundary
//     renders it; Send() would draw it twice. context.Canceled comes
//     straight back and maps to the canceled exit.
//
// clog 0.17.1 stops its animation on cancellation without joining its task.
// The completion channel joins the result producer before this function returns:
// cancellation still reaches fn, but cannot discard a late receipt or race its
// storage. fn must honor its context and finish releasing its own resources.
// Its completion select can also choose a nil task error after cancellation.
// Recheck the shared context after joining so a completed receipt cannot turn
// an interrupted or expired command into success.
func Spin[T any](ctx context.Context, label string, fn func(context.Context) (T, error)) (T, error) {
	completed := make(chan T, 1)
	err := clog.Ctx(ctx).Spinner(label).
		After(spinnerDelay).
		NonTTYSilent(true).
		Wait(ctx, func(taskCtx context.Context) error {
			result, callErr := fn(taskCtx)
			completed <- result
			return callErr
		}).
		Silent()
	result := <-completed
	if err == nil {
		err = ctx.Err()
	}
	return result, err
}
