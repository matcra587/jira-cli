package core

import (
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestRankOutcomesCorrelateNumericNativeIdentities(t *testing.T) {
	t.Parallel()
	for _, keys := range []bool{false, true} {
		outcomes := []RankOutcome{{IssueID: 10002, Status: 403, Errors: []string{"fixture denied"}}, {IssueID: 10001, Status: 200}}
		if keys {
			outcomes[0].IssueKey, outcomes[1].IssueKey = "KEY-2", "KEY-1"
		}
		ordered, err := OrderedRankOutcomes([]string{"10001", "10002"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: outcomes})
		if err != nil || len(ordered) != 2 || ordered[0].IssueID != 10001 || ordered[0].Status != 200 || ordered[1].IssueID != 10002 || ordered[1].Status != 403 || !slices.Equal(ordered[1].Errors, []string{"fixture denied"}) {
			t.Fatal("numeric ranking attribution lost native order, identity or failure")
		}
		if outcomes[0].IssueID != 10002 || outcomes[1].IssueID != 10001 {
			t.Fatal("ranking correlation mutated its native receipt")
		}
	}
}

func TestRankOutcomesRejectAmbiguousAndUnusableAcknowledgements(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		issues []string
		write  RankWrite
	}{
		"no-acknowledgement":   {[]string{"KEY-1"}, RankWrite{}},
		"unexpected-success":   {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusAccepted}},
		"contradictory-204":    {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusNoContent, Outcomes: []RankOutcome{{IssueKey: "KEY-1", Status: 403}}}},
		"ambiguous-alias":      {[]string{"KEY-1", "10001"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueKey: "KEY-1", IssueID: 10001, Status: 200}, {IssueID: 10001, Status: 403}}}},
		"id-only-key-input":    {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueID: 10001, Status: 200}}}},
		"foreign-id":           {[]string{"10001"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueID: 10002, Status: 200}}}},
		"informational-status": {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueKey: "KEY-1", Status: 199}}}},
		"unknown-status":       {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueKey: "KEY-1", Status: 600}}}},
		"negative-id":          {[]string{"KEY-1"}, RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: []RankOutcome{{IssueKey: "KEY-1", IssueID: -1, Status: 200}}}},
	} {
		t.Run(name, func(t *testing.T) {
			ordered, err := OrderedRankOutcomes(test.issues, test.write)
			if ordered != nil || !errors.Is(err, ErrRankResponseInvalid) {
				t.Fatal("unusable native rank acknowledgement fabricated ordered moves")
			}
		})
	}
}
