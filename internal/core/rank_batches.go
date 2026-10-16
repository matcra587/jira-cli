package core

import (
	"slices"

	"github.com/matcra587/jira-cli/internal/jira"
)

// PlanRankRequests validates the complete ordered move before constructing
// independent native requests of at most MaxRankIssues each. Later requests
// follow the preceding batch's last issue. Callers must stop if a preceding
// batch is not completely acknowledged; a plan never authorizes a replay.
func PlanRankRequests(request jira.RankRequest) ([]jira.RankRequest, error) {
	if len(request.Issues) == 0 {
		return nil, ValidateRankRequest(request)
	}
	if err := validateRankIdentities(request); err != nil {
		return nil, err
	}
	requests := make([]jira.RankRequest, 0, (len(request.Issues)-1)/MaxRankIssues+1)
	var previous string
	for issues := range slices.Chunk(request.Issues, MaxRankIssues) {
		native := request
		native.Issues = slices.Clone(issues)
		if len(requests) != 0 {
			native.RankBeforeIssue, native.RankAfterIssue = "", previous
		}
		if err := ValidateRankRequest(native); err != nil {
			return nil, err
		}
		requests = append(requests, native)
		previous = issues[len(issues)-1]
	}
	return requests, nil
}
