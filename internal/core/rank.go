package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrRankResponseInvalid means a ranking acknowledgement cannot be attributed.
// Some issues may have moved; the caller must inspect before another write.
var ErrRankResponseInvalid = errors.New("invalid ranking response")

// MaxRankIssues is the Agile endpoint's maximum for one ordered request.
const MaxRankIssues = 50

// RankRequestError identifies the native input member that failed preflight.
type RankRequestError struct {
	Field   string
	Message string
}

func (e *RankRequestError) Error() string { return e.Message }

// ValidateRankRequest checks the known one-request constraints without reading
// metadata or resolving issues. Jira still owns permission and board validation.
func ValidateRankRequest(req jira.RankRequest) error {
	if len(req.Issues) == 0 || len(req.Issues) > MaxRankIssues {
		return &RankRequestError{Field: "issues", Message: fmt.Sprintf("rank requires between 1 and %d issues in one request", MaxRankIssues)}
	}
	if (req.RankBeforeIssue == "") == (req.RankAfterIssue == "") {
		return &RankRequestError{Field: "rankBeforeIssue", Message: "rank requires exactly one before or after anchor"}
	}
	anchor, field := req.RankBeforeIssue, "rankBeforeIssue"
	if req.RankAfterIssue != "" {
		anchor, field = req.RankAfterIssue, "rankAfterIssue"
	}
	seen := make(map[string]bool, len(req.Issues))
	for _, issue := range req.Issues {
		if issue == "" || seen[issue] {
			return &RankRequestError{Field: "issues", Message: "rank targets must be nonempty and distinct"}
		}
		if issue == anchor {
			return &RankRequestError{Field: field, Message: "the rank anchor must differ from every issue being moved"}
		}
		seen[issue] = true
	}
	return nil
}

// OrderedRankOutcomes validates the whole acknowledgement before publishing
// moves. Native entries may arrive out of order and identify issues by key or ID.
func OrderedRankOutcomes(issues []string, write RankWrite) ([]RankOutcome, error) {
	if write.StatusCode == http.StatusNoContent && len(write.Outcomes) == 0 {
		return nil, nil
	}
	if write.StatusCode != http.StatusMultiStatus || len(write.Outcomes) != len(issues) || len(issues) == 0 {
		return nil, fmt.Errorf("%w: the acknowledgement does not cover every requested issue", ErrRankResponseInvalid)
	}
	positions := make(map[string]int, len(issues))
	for i, issue := range issues {
		if _, exists := positions[issue]; exists || issue == "" {
			return nil, fmt.Errorf("%w: requested identities are empty or repeated", ErrRankResponseInvalid)
		}
		positions[issue] = i
	}
	ordered := make([]RankOutcome, len(issues))
	seen := make([]bool, len(issues))
	seenIDs := map[int64]bool{}
	for _, outcome := range write.Outcomes {
		if outcome.Status < http.StatusOK || outcome.Status > 599 || outcome.IssueID < 0 || outcome.IssueKey == "" && outcome.IssueID == 0 || outcome.Status < http.StatusMultipleChoices && len(outcome.Errors) != 0 {
			return nil, fmt.Errorf("%w: a native entry has unusable identity or status", ErrRankResponseInvalid)
		}
		if outcome.IssueID > 0 {
			if seenIDs[outcome.IssueID] {
				return nil, fmt.Errorf("%w: native issue IDs are repeated", ErrRankResponseInvalid)
			}
			seenIDs[outcome.IssueID] = true
		}
		keyPosition, keyMatches := positions[outcome.IssueKey]
		idPosition, idMatches := positions[strconv.FormatInt(outcome.IssueID, 10)]
		if outcome.IssueID == 0 {
			idMatches = false
		}
		if keyMatches && idMatches && keyPosition != idPosition || !keyMatches && !idMatches {
			return nil, fmt.Errorf("%w: a native entry has foreign or ambiguous attribution", ErrRankResponseInvalid)
		}
		position := keyPosition
		if !keyMatches {
			position = idPosition
		}
		if seen[position] {
			return nil, fmt.Errorf("%w: a requested issue has repeated outcomes", ErrRankResponseInvalid)
		}
		seen[position] = true
		ordered[position] = outcome
	}
	return ordered, nil
}
