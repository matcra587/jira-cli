package issue

import (
	"context"
	"errors"

	"github.com/gechr/clog"

	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func recordTransitionHints(ctx context.Context, store core.Cache, outcomes []transitionReadOutcome) {
	for _, outcome := range outcomes {
		if outcome.result.OK {
			if ctx.Err() != nil {
				continue
			}
		} else if outcome.observed.IsZero() || errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded) {
			continue
		}
		// A completed non-cancellation failure invalidates its old observation
		// even when another issue later cancels the command. Canceled or unstarted
		// reads do not establish that the previous workflow changed.
		var err error
		if outcome.result.OK {
			err = cachecmd.RecordTransitionHints(ctx, store, outcome.result.Issue.Key, outcome.result.Data, outcome.observed)
		} else {
			err = cachecmd.ForgetTransitionHints(store, outcome.result.Issue.Key)
		}
		if err != nil {
			clog.Debug().Err(err).Msg("transition hints could not be updated")
		}
	}
}

func invalidateTransitionHints(store core.Cache, results []envelope.KeyedResult[envelope.IssueTransitionData]) {
	for _, row := range results {
		if row.Data == nil || !row.Data.Attempted {
			continue
		}
		if err := cachecmd.ForgetTransitionHints(store, row.Issue.Key); err != nil {
			clog.Debug().Err(err).Msg("transition hints could not be invalidated")
		}
	}
}

// transitionHintCache resolves authenticated public scope before remote work.
// Completed failed reads and attempted writes can then invalidate known stale
// hints without reopening credentials after a later command cancellation.
func transitionHintCache(ctx context.Context, deps Deps) core.Cache {
	if ctx.Err() != nil {
		return noCache{}
	}
	return issueCache(ctx, deps)
}
