package envelope

import (
	"slices"
	"testing"
)

func TestRecentIssueKeysPreservesRequestedAndReturnedIdentities(t *testing.T) {
	t.Parallel()
	rows := KeyedResults[IssueCloneData]{Results: []KeyedResult[IssueCloneData]{
		{Issue: IssueRef{Key: "DEMO-2"}, OK: false, Data: &IssueCloneData{Clone: &IssueRef{Key: "DEMO-7"}}, Err: &KeyError{Code: "canceled"}},
		{Issue: IssueRef{Key: "DEMO-3"}, OK: false, Err: &KeyError{Code: "canceled"}},
	}}
	for _, tc := range []struct {
		name  string
		value interface{ RecentIssueKeys() []string }
		want  []string
	}{
		{name: "failed and queued sources", value: rows, want: []string{"DEMO-2", "DEMO-3"}},
		{name: "late clone identity", value: IssueCloneOutput{KeyedResults: rows}, want: []string{"DEMO-2", "DEMO-3", "DEMO-7"}},
		{name: "created identity", value: IssueCreateOutput{Issue: &IssueRef{Key: "DEMO-8"}}, want: []string{"DEMO-8"}},
		{name: "no created identity", value: IssueCreateOutput{}},
		{name: "server search order", value: IssueListOutput{Issues: []IssueView{{IssueRef: IssueRef{Key: "DEMO-4"}}, {IssueRef: IssueRef{Key: "DEMO-1"}}}}, want: []string{"DEMO-4", "DEMO-1"}},
		{name: "count only", value: IssueListOutput{Count: &IssueCountEstimate{Value: 100}}},
		{name: "rank anchor", value: IssueRankOutput{Anchor: "DEMO-6", KeyedResults: KeyedResults[IssueRankData]{Results: []KeyedResult[IssueRankData]{{Issue: IssueRef{Key: "DEMO-5"}}}}}, want: []string{"DEMO-5", "DEMO-6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.value.RecentIssueKeys(); !slices.Equal(got, tc.want) {
				t.Fatalf("recent identities=%v, want %v", got, tc.want)
			}
		})
	}
}
