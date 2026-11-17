package core

import "context"

// ProgressPhase identifies an owned workflow, never caller-authored content.
type ProgressPhase uint8

const (
	ProgressBulkResults ProgressPhase = iota + 1
	ProgressUpdateDiscover
	ProgressUpdateChecksums
	ProgressUpdateVerifyManifest
	ProgressUpdateDownload
	ProgressUpdateVerifyArchive
	ProgressUpdateExtract
	ProgressUpdateInstall
	ProgressUpdateInstalled
	ProgressUpdateBrew
)

// Progress is an advisory observation. Counts describe completed result rows
// or accepted bytes, not successful Jira writes. Complete closes this phase;
// it never establishes publication, durability or overall command success.
type Progress struct {
	Phase     ProgressPhase
	Completed int64
	Total     int64
	Failed    int64
	Complete  bool
}

type progressObserverKey struct{}

// WithProgressObserver binds a synchronous observer to this invocation. A nil
// observer masks an inherited one. Observers must not change operation policy.
func WithProgressObserver(ctx context.Context, observer func(Progress)) context.Context {
	return context.WithValue(ctx, progressObserverKey{}, observer)
}

// HasProgressObserver permits producers to skip optional counting overhead.
func HasProgressObserver(ctx context.Context) bool {
	observer, _ := ctx.Value(progressObserverKey{}).(func(Progress))
	return observer != nil
}

// ObserveProgress publishes a fact without introducing another execution path.
func ObserveProgress(ctx context.Context, progress Progress) {
	if observer, _ := ctx.Value(progressObserverKey{}).(func(Progress)); observer != nil {
		observer(progress)
	}
}
