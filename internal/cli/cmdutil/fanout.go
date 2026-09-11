package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gechr/clog"
	"github.com/gechr/clog/field/duration"
	"github.com/gechr/clog/fx"
	"golang.org/x/sync/errgroup"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

// KeyResult carries one per-key read result while preserving the original key.
type KeyResult[T any] struct {
	Key   string
	Value T
	Err   error
}

// FanOutKeysProgress runs FanOutKeys with user feedback for the operation named
// by op (a cli.VerbFor key like "issue.delete"): a live multi-line block with
// one row per key (finished rows scroll away, a footer ticks done/total), plus
// a per-key debug lifecycle that records each key's elapsed time and, on
// failure, the HTTP status and reason as fields:
//
//	DBG Deleting issue key=ABC-1
//	DBG Deleted issue key=ABC-1 time=210ms
//	DBG Failed to delete issue key=ABC-2 time=95ms status=404 reason="..."
//
// The block mirrors the auth-login spinner's gating: NonTTYSilent suppresses
// every row whenever stderr is not a terminal — piped, redirected, or captured
// by an agent — and clog writes status to stderr, so a stdout JSON envelope
// stays clean even under --output=json. The debug lines surface only under
// --debug. A single key gets the debug lifecycle but no rendered block.
func FanOutKeysProgress[T any](
	ctx context.Context,
	op string,
	keys []string,
	parallelism int,
	fn func(context.Context, string) (T, error),
) ([]KeyResult[T], error) {
	return fanOutKeysProgressVerb(ctx, cli.VerbFor(op), keys, parallelism, fn, true)
}

// FanOutKeysProgressPreview is FanOutKeysProgress for a dry-run: every
// lifecycle surface — spinner rows, the footer, and the per-key debug
// lines — speaks about the preview ("previewing issue edits",
// "previewed issue edit") instead of claiming the mutation happened.
// Callers whose dry-run path still fans out (local pipeline validation,
// --validate-remote reads) select it on their dry-run flag.
func FanOutKeysProgressPreview[T any](
	ctx context.Context,
	op string,
	keys []string,
	parallelism int,
	fn func(context.Context, string) (T, error),
) ([]KeyResult[T], error) {
	return fanOutKeysProgressVerb(ctx, cli.VerbFor(op).Preview(), keys, parallelism, fn, false)
}

func fanOutKeysProgressVerb[T any](
	ctx context.Context,
	verb cli.OperationVerb,
	keys []string,
	parallelism int,
	fn func(context.Context, string) (T, error),
	recordElapsed bool,
) ([]KeyResult[T], error) {
	if ctx == nil || fn == nil {
		return FanOutKeys(ctx, keys, parallelism, fn)
	}

	// The completion line reports the fan-out's wall time, not the sum of
	// per-key durations — under -p those overlap and a sum would overstate
	// what the user actually waited. Preview fan-outs (recordElapsed=false)
	// run local pipeline validation, not round trips, so they stay out of
	// the sink.
	if recordElapsed {
		start := time.Now()
		defer func() { recordAPIElapsed(ctx, time.Since(start)) }()
	}

	logger := clog.Ctx(ctx)
	// traced wraps a per-key call with the debug lifecycle, keeping the timing
	// and field-shaping in one place for both the single- and multi-key paths.
	traced := func(ctx context.Context, key string) (T, error) {
		logger.Debug().Str("key", key).Msg(verb.Gerundf())
		start := time.Now()
		value, err := fn(ctx, key)
		elapsed := time.Since(start)
		if err != nil {
			// duration.WithMinimum(0) keeps time= visible below clog's default
			// 1s cutoff: this per-key debug lifecycle documents sub-second timings above.
			event := logger.Debug().Str("key", key).Duration("time", elapsed, duration.WithMinimum(0), duration.WithGradientMax(debugTimeGradientMax))
			if apiErr, ok := errors.AsType[*jira.APIError](err); ok {
				event = event.Int("status", apiErr.StatusCode)
			}
			// The error text embeds Jira-supplied messages, so the reason field
			// crosses the terminal sanitizer before reaching stderr.
			event.Str("reason", cli.SanitizeTerminalText(err.Error())).Msg(verb.Failuref())
		} else {
			logger.Debug().Str("key", key).Duration("time", elapsed, duration.WithMinimum(0), duration.WithGradientMax(debugTimeGradientMax)).Msg(verb.Pastf())
		}
		return value, err
	}

	// A single key never warrants a bar, and under --debug the per-key debug
	// lifecycle narrates progress — an animated bar sharing stderr with those
	// verbose lines would only strand its redraw frames between them. In both
	// cases run the traced fan-out without a bar.
	if len(keys) <= 1 || clog.IsVerbose() {
		return FanOutKeys(ctx, keys, parallelism, traced)
	}

	// The keys render as a live multi-line block: one row per key,
	// finished rows scroll away (WithHideDone), and a footer ticks the
	// done/total count. Each row is registered with fx.Manual, which
	// spawns no goroutine and consumes no group parallelism slot —
	// FanOutKeys stays the sole concurrency owner; clog only renders.
	// The footer label is user-facing UI, so it is Sentence-cased; the
	// per-key debug lifecycle in traced stays lower case as a structured
	// log. Every row is NonTTYSilent so a piped or captured stderr sees
	// nothing, exactly like the aggregate bar this block replaces.
	label := cli.SentenceCase(verb.GerundPlural())
	// The footer carries the fan-out's timeout countdown; per-key rows stay
	// label-only so the block reads as one deadline, not one per key.
	footer := clog.Spinner(label)
	if dl, ok := ctx.Deadline(); ok {
		footer = footer.Deadline("timeout", time.Until(dl))
	}
	group := clog.Group(
		ctx,
		fx.WithHideDone(),
		fx.WithFooter(
			footer,
			func(done, total int, u *fx.Update) {
				u.Msg(label).Str("progress", fmt.Sprintf("%d/%d", done, total)).Send()
			},
		),
	)
	finishers := make([]func(error), len(keys))
	// Duplicate keys are legal in a key expression; hand each worker its
	// own row by queueing row indices per key and popping one per call.
	rowIndex := make(map[string]chan int, len(keys))
	for i, key := range keys {
		_, finish := group.Add(clog.Spinner(key).NonTTYSilent(true)).Manual()
		finishers[i] = finish
		if rowIndex[key] == nil {
			rowIndex[key] = make(chan int, len(keys))
		}
		rowIndex[key] <- i
	}
	// The render loop lives inside Wait, so it must run concurrently with
	// the fan-out for the block to animate while keys complete.
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		// The group's aggregate error is deliberately discarded: per-key
		// errors live in the fan-out results and fanErr is authoritative;
		// a completion line here would double-report them.
		group.Wait().Silent() //nolint:errcheck // per-key results and fanErr are authoritative
	}()
	tracked := func(ctx context.Context, key string) (T, error) {
		value, err := traced(ctx, key)
		select {
		case i := <-rowIndex[key]:
			finishers[i](err)
		default:
		}
		return value, err
	}
	results, fanErr := FanOutKeys(ctx, keys, parallelism, tracked)
	// A canceled fan-out can leave rows unfinished, which would wedge
	// Wait forever. finish is idempotent (sync.Once inside clog), so
	// sweep every row with its final per-key error.
	for i := range finishers {
		err := results[i].Err
		if err == nil && fanErr != nil {
			err = fanErr
		}
		finishers[i](err)
	}
	<-waited
	return results, fanErr
}

// FanOutKeys runs fn for each key with bounded concurrency and returns results
// in the same order as keys. Per-key errors are stored in the result slot so
// one failed key does not cancel unrelated work.
func FanOutKeys[T any](
	ctx context.Context,
	keys []string,
	parallelism int,
	fn func(context.Context, string) (T, error),
) ([]KeyResult[T], error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}
	if fn == nil {
		return nil, errors.New("fanout function must not be nil")
	}
	if parallelism < defaultParallelism || parallelism > maxParallelism {
		return nil, fmt.Errorf("parallelism must be between %d and %d", defaultParallelism, maxParallelism)
	}

	results := make([]KeyResult[T], len(keys))
	for i, key := range keys {
		results[i].Key = key
	}
	if len(keys) == 0 {
		return results, nil
	}
	if parallelism == defaultParallelism {
		for i, key := range keys {
			if err := ctx.Err(); err != nil {
				return results, err
			}
			value, err := fn(ctx, key)
			results[i] = KeyResult[T]{Key: key, Value: value, Err: err}
		}
		if err := ctx.Err(); err != nil {
			return results, err
		}
		return results, nil
	}

	workerCount := min(parallelism, len(keys))

	jobs := make(chan int)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(workerCount)
	for range workerCount {
		group.Go(func() error {
			for {
				select {
				case <-groupCtx.Done():
					return nil
				case i, ok := <-jobs:
					if !ok {
						return nil
					}
					if err := groupCtx.Err(); err != nil {
						return nil
					}
					value, err := fn(groupCtx, keys[i])
					results[i] = KeyResult[T]{Key: keys[i], Value: value, Err: err}
				}
			}
		})
	}

	var feedErr error
feed:
	for i := range keys {
		if err := groupCtx.Err(); err != nil {
			feedErr = err
			break
		}
		select {
		case <-groupCtx.Done():
			feedErr = groupCtx.Err()
			break feed
		case jobs <- i:
		}
	}
	close(jobs)

	if err := group.Wait(); err != nil {
		return results, err
	}
	if feedErr != nil {
		return results, feedErr
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}
