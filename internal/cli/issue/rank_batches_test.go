package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type rankBatchClient struct {
	unusedJiraClient
	requests []jira.RankRequest
	writes   []core.RankWrite
	errors   []error
	onCall   func(int)
}

func (f *rankBatchClient) RankIssues(_ context.Context, request jira.RankRequest) (core.RankWrite, error) {
	request.Issues = slices.Clone(request.Issues)
	f.requests = append(f.requests, request)
	i := len(f.requests) - 1
	if f.onCall != nil {
		f.onCall(i)
	}
	write := core.RankWrite{StatusCode: http.StatusNoContent}
	if i < len(f.writes) {
		write = f.writes[i]
	}
	var err error
	if i < len(f.errors) {
		err = f.errors[i]
	}
	return write, err
}

func rankBatchKeys(count int) []string {
	keys := make([]string, count)
	for i := range keys {
		keys[i] = fmt.Sprintf("KEY-%d", i+1)
	}
	return keys
}

func TestRankBatchesRetainSourceOrder(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, 50, 51, 100, 101} {
		for _, position := range []string{"before", "after"} {
			t.Run(fmt.Sprintf("%s/%d", position, count), func(t *testing.T) {
				keys := rankBatchKeys(count)
				client := &rankBatchClient{}
				before, after := "KEY-999", ""
				if position == "after" {
					before, after = "", "KEY-999"
				}
				out, err := runRank(t.Context(), Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, keys...), before, after)
				if err != nil || len(client.requests) != (count+49)/50 || out.Anchor != "KEY-999" || out.Position != position || len(out.Results) != count {
					t.Fatalf("ordered ranking did not retain the complete authored batch: requests%d rows%d err%v", len(client.requests), len(out.Results), err)
				}
				var observed []string
				for i, request := range client.requests {
					if len(request.Issues) == 0 || len(request.Issues) > 50 {
						t.Fatal("CLI batching exceeded a native request boundary")
					}
					if i == 0 {
						if request.RankBeforeIssue != before || request.RankAfterIssue != after {
							t.Fatal("first batch changed the authored anchor")
						}
					} else if request.RankBeforeIssue != "" || request.RankAfterIssue != observed[len(observed)-1] {
						t.Fatal("later batch did not follow its acknowledged predecessor")
					}
					observed = append(observed, request.Issues...)
				}
				if !slices.Equal(observed, keys) {
					t.Fatal("native requests lost or reordered authored sources")
				}
				for i, row := range out.Results {
					if row.Issue.Key != keys[i] || !row.OK || row.Data == nil || row.Err != nil {
						t.Fatal("joined batch acknowledgement lost a source or invented failure")
					}
				}
			})
		}
	}
}

func TestRankBatchFailureRetainsAcknowledgedAndUnstartedMoves(t *testing.T) {
	t.Parallel()
	keys := rankBatchKeys(101)
	cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1}}
	for _, failed := range []int{0, 1} {
		t.Run(fmt.Sprintf("batch-%d", failed+1), func(t *testing.T) {
			client := &rankBatchClient{writes: []core.RankWrite{{StatusCode: http.StatusNoContent}, {}}, errors: []error{nil, cause}}
			if failed == 0 {
				client.writes, client.errors = client.writes[1:], client.errors[1:]
			}
			out, err := runRank(t.Context(), Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, keys...), "KEY-999", "")
			actual, ok := errors.AsType[*adapterjira.APIError](err)
			if !ok || actual != cause || cli.MapError(err).RetryAfterSeconds != 7 || len(client.requests) != failed+1 || len(out.Results) != 101 {
				t.Fatalf("rank batching lost the original transport cause or replayed a request: %v", err)
			}
			for i, row := range out.Results {
				if row.Issue.Key != keys[i] {
					t.Fatal("failure discarded the complete authored source order")
				}
				if i < failed*50 {
					if !row.OK || row.Data == nil || row.Err != nil {
						t.Fatal("later failure erased an earlier native acknowledgement")
					}
					continue
				}
				code := cli.CodeRankNotAttempted
				if i < (failed+1)*50 {
					code = "jira_rate_limited"
				}
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatalf("move %d lost its entered versus unstarted outcome: %+v", i, row)
				}
			}
		})
	}
}

func TestRankPartialAndInvalidBatchesStopTheirSuccessors(t *testing.T) {
	t.Parallel()
	keys := rankBatchKeys(101)
	partial := core.RankWrite{StatusCode: http.StatusMultiStatus}
	for i, key := range keys[:50] {
		entry := core.RankOutcome{IssueKey: key, Status: http.StatusOK}
		if i == 17 {
			entry.Status, entry.Errors = http.StatusForbidden, []string{"fixture denied"}
		}
		partial.Outcomes = append(partial.Outcomes, entry)
	}
	for name, write := range map[string]core.RankWrite{"partial": partial, "unacknowledged": {}, "malformed": {StatusCode: http.StatusMultiStatus}} {
		t.Run(name, func(t *testing.T) {
			client := &rankBatchClient{writes: []core.RankWrite{write}}
			out, err := runRank(t.Context(), Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, keys...), "KEY-999", "")
			if len(client.requests) != 1 || len(out.Results) != 101 || name == "partial" && err != nil || name != "partial" && !errors.Is(err, core.ErrRankResponseInvalid) {
				t.Fatalf("rank batching continued after incomplete acknowledgement: %v", err)
			}
			for i, row := range out.Results {
				if i >= 50 {
					if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeRankNotAttempted {
						t.Fatal("an unstarted move was reported as entered or acknowledged")
					}
				} else if name == "partial" {
					if i == 17 {
						if row.OK || row.Err == nil || row.Err.Code != "jira_forbidden" || row.Err.Message != "fixture denied" {
							t.Fatal("native per-issue refusal lost its exact identity or reason")
						}
					} else if !row.OK || row.Data == nil || row.Err != nil {
						t.Fatal("native partial acknowledgement erased an actual successful move")
					}
				} else if row.OK || row.Err == nil || row.Err.Code != cli.CodeRankResponseInvalid {
					t.Fatal("unusable batch acknowledgement invented successful moves")
				}
			}
		})
	}
}

func TestRankBatchCancellationRetainsTheJoinedBoundary(t *testing.T) {
	t.Parallel()
	for _, lateError := range []bool{false, true} {
		t.Run(fmt.Sprintf("returned-error-%t", lateError), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &rankBatchClient{onCall: func(int) { cancel() }}
			if lateError {
				client.errors = []error{context.Canceled}
			}
			out, err := runRank(ctx, Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, rankBatchKeys(51)...), "KEY-999", "")
			if !errors.Is(err, context.Canceled) || len(client.requests) != 1 || len(out.Results) != 51 {
				t.Fatal("cancellation entered a successor or discarded the authored batch")
			}
			for _, row := range out.Results[:50] {
				if !row.OK || row.Data == nil || row.Err != nil {
					t.Fatal("late cancellation erased acknowledged native moves")
				}
			}
			last := out.Results[50]
			if last.OK || last.Data != nil || last.Err == nil || last.Err.Code != cli.CodeRankNotAttempted {
				t.Fatal("cancellation fabricated an attempt of the successor")
			}
		})
	}
}

func TestRankBatchPreflightAndPreviewRemainLazy(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"preview", "canceled", "late-duplicate", "late-anchor"} {
		t.Run(scenario, func(t *testing.T) {
			keys := rankBatchKeys(101)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "canceled":
				cancel()
			case "late-duplicate":
				keys[100] = keys[0]
			case "late-anchor":
				keys[100] = "KEY-999"
			}
			calls := 0
			out, err := runRank(ctx, Deps{JiraClient: func(context.Context) (core.JiraClient, error) { calls++; return &rankBatchClient{}, nil }}, invocation(scenario == "preview", keys...), "KEY-999", "")
			if calls != 0 || scenario == "preview" && (err != nil || !out.DryRun || len(out.Results) != 101) || scenario != "preview" && err == nil {
				t.Fatal("preview or whole-input preflight entered a client or dropped valid authored moves")
			}
		})
	}
}

type rankMutatingClient struct{ unusedJiraClient }

func (*rankMutatingClient) RankIssues(_ context.Context, request jira.RankRequest) (core.RankWrite, error) {
	request.Issues[0] = "FOREIGN-1"
	return core.RankWrite{StatusCode: http.StatusNoContent}, nil
}

func TestRankBatchClientCannotRewriteAuthoredIdentity(t *testing.T) {
	t.Parallel()
	keys := rankBatchKeys(51)
	out, err := runRank(t.Context(), Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return &rankMutatingClient{}, nil }}, invocation(false, keys...), "KEY-999", "")
	if err != nil || len(out.Results) != 51 || !slices.Equal(keys, rankBatchKeys(51)) {
		t.Fatal("native client mutated the caller's authored rank input")
	}
	for i, row := range out.Results {
		if row.Issue.Key != keys[i] || !row.OK || row.Data == nil || row.Err != nil {
			t.Fatal("native client rewrote the identity of an acknowledged move")
		}
	}
}
