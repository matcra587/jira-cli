package core

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestPlanRankRequestsValidatesTheWholeMove(t *testing.T) {
	t.Parallel()
	keys := make([]string, 101)
	for i := range keys {
		keys[i] = fmt.Sprintf("KEY-%d", i+1)
	}
	for _, position := range []string{"before", "after"} {
		t.Run(position, func(t *testing.T) {
			input := jira.RankRequest{Issues: slices.Clone(keys), RankBeforeIssue: "KEY-999"}
			if position == "after" {
				input.RankBeforeIssue, input.RankAfterIssue = "", "KEY-999"
			}
			got, err := PlanRankRequests(input)
			want := []jira.RankRequest{
				{Issues: keys[:50], RankBeforeIssue: input.RankBeforeIssue, RankAfterIssue: input.RankAfterIssue},
				{Issues: keys[50:100], RankAfterIssue: "KEY-50"},
				{Issues: keys[100:], RankAfterIssue: "KEY-100"},
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("native request plan changed exact source order or chained anchors: %v", err)
			}
			if ValidateRankRequest(input) == nil {
				t.Fatal("CLI planning weakened the native one-request limit")
			}
			input.Issues[0] = "changed"
			if got[0].Issues[0] != "KEY-1" {
				t.Fatal("planned native inputs alias the caller's mutable keys")
			}
			got[1].Issues[0] = "changed-plan"
			if input.Issues[50] != "KEY-51" || keys[50] != "KEY-51" {
				t.Fatal("a mutable planned batch changed its original input")
			}
		})
	}
	for name, change := range map[string]func(*jira.RankRequest){
		"empty":                 func(r *jira.RankRequest) { r.Issues = nil },
		"missing-anchor":        func(r *jira.RankRequest) { r.RankBeforeIssue = "" },
		"two-anchors":           func(r *jira.RankRequest) { r.RankAfterIssue = "KEY-998" },
		"late-anchor":           func(r *jira.RankRequest) { r.Issues[100] = "KEY-999" },
		"late-empty":            func(r *jira.RankRequest) { r.Issues[100] = "" },
		"cross-batch-duplicate": func(r *jira.RankRequest) { r.Issues[100] = "KEY-1" },
	} {
		t.Run(name, func(t *testing.T) {
			input := jira.RankRequest{Issues: slices.Clone(keys), RankBeforeIssue: "KEY-999"}
			change(&input)
			if requests, err := PlanRankRequests(input); err == nil || requests != nil {
				t.Fatal("late invalid input published a usable partial rank plan")
			}
		})
	}
}
