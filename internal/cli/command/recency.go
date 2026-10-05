package command

import (
	"context"

	"github.com/matcra587/jira-cli/internal/cli"
)

type issueKeyRecorderKey struct{}

// WithIssueKeyRecorder attaches the invocation's best-effort local observer.
// It observes typed outcomes after joined work, before rendering can fail. It
// never changes an operation's result, and previews never invoke the observer.
func WithIssueKeyRecorder(ctx context.Context, record func([]string)) context.Context {
	return context.WithValue(ctx, issueKeyRecorderKey{}, record)
}

func recordIssueKeys(ctx context.Context, data any, dryRun bool, positions ArgSpec, args []string, runErr error) {
	if dryRun {
		return
	}
	// Some outputs retain requested-key plans before client setup. Their typed
	// read fact distinguishes those plans from native work, including a native
	// validation failure whose exit category also describes local refusals.
	read, hasReadFact := data.(interface{ IssueReadStarted() bool })
	if hasReadFact && !read.IssueReadStarted() {
		return
	}
	// Ordinary typed outcomes retain the original transport cause. An actual
	// Jira refusal still observes those outcomes. Local validation and read-only
	// transport refusals without prior native observations do not become history.
	if !hasReadFact && runErr != nil && cli.MapError(runErr).Exit == cli.ExitValidation {
		response := cli.HasJiraResponse(runErr)
		if joined, ok := data.(interface{ HasJiraResponse() bool }); ok {
			response = response || joined.HasJiraResponse()
		}
		if !response {
			return
		}
	}
	record, ok := ctx.Value(issueKeyRecorderKey{}).(func([]string))
	if !ok || record == nil {
		return
	}
	var observedKeys []string
	if observed, ok := data.(interface{ RecentIssueKeys() []string }); ok {
		for _, key := range observed.RecentIssueKeys() {
			if normalized, err := ParseIssueKey(key); err == nil {
				observedKeys = append(observedKeys, normalized)
			}
		}
	}
	// A failed constructor has no observed outcome. Retained native failure
	// and cancellation rows still identify work and remain eligible below.
	if runErr != nil && len(observedKeys) == 0 {
		return
	}
	var keys []string
	for i, arg := range args {
		if len(positions.Positions) == 0 {
			break
		}
		position := i
		if position >= len(positions.Positions) {
			if positions.Max != AnyArgs {
				break
			}
			position = len(positions.Positions) - 1
		}
		if positions.Positions[position].Predictor == "issuekey" {
			if parsed, err := ParseIssueKeys([]string{arg}); err == nil {
				keys = append(keys, parsed...)
			}
		}
	}
	keys = append(keys, observedKeys...)
	if len(keys) != 0 {
		record(keys)
	}
}
