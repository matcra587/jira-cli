package command

import (
	"context"
	"sync"

	"github.com/matcra587/jira-cli/internal/core"
)

// TrackResults observes a frozen fan-out without changing its native x/sync
// scheduling, cancellation or ordered receipts. Call the returned function
// after assigning each result row, including an early failure. Single-issue
// operations retain indeterminate status. The observer is serialized so concurrent
// completions cannot publish counts out of order.
func TrackResults(ctx context.Context, total int, phase core.ProgressPhase) func(bool) {
	if total <= 0 || (total == 1 && phase == core.ProgressBulkResults) || !core.HasProgressObserver(ctx) {
		return func(bool) {}
	}
	progress := core.Progress{Phase: phase, Total: int64(total)}
	core.ObserveProgress(ctx, progress)
	var mu sync.Mutex
	return func(ok bool) {
		mu.Lock()
		defer mu.Unlock()
		progress.Completed++
		if !ok {
			progress.Failed++
		}
		progress.Complete = progress.Completed == progress.Total
		core.ObserveProgress(ctx, progress)
	}
}
