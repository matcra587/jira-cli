package issue

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type rankReceiptClient struct {
	unusedJiraClient
	late  bool
	cause error
	calls atomic.Int64
}

type rankOutcomeClient struct {
	unusedJiraClient
	write core.RankWrite
	calls atomic.Int64
}

func (f *rankOutcomeClient) RankIssues(context.Context, jira.RankRequest) (core.RankWrite, error) {
	f.calls.Add(1)
	return f.write, nil
}

func TestRankAlternateClientCannotInventAcknowledgements(t *testing.T) {
	t.Parallel()
	for name, write := range map[string]core.RankWrite{
		"unacknowledged":    {},
		"missing-entries":   {StatusCode: http.StatusMultiStatus},
		"foreign-entries":   {StatusCode: http.StatusMultiStatus, Outcomes: []core.RankOutcome{{IssueKey: "KEY-3", Status: 200}, {IssueKey: "KEY-2", Status: 200}}},
		"contradictory-204": {StatusCode: http.StatusNoContent, Outcomes: []core.RankOutcome{{IssueKey: "KEY-1", Status: 403}}},
	} {
		t.Run(name, func(t *testing.T) {
			client := &rankOutcomeClient{write: write}
			out, err := runRank(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "KEY-2"), "KEY-9", "")
			if !errors.Is(err, core.ErrRankResponseInvalid) || cli.MapError(err).Exit != cli.ExitServer || client.calls.Load() != 1 || out.Anchor != "KEY-9" || out.Position != "before" || len(out.Results) != 2 {
				t.Fatal("alternate rank client fabricated a batch acknowledgement")
			}
			for i, row := range out.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeRankResponseInvalid {
					t.Fatal("alternate rank client lost authored targets or invented moves")
				}
			}
		})
	}
}

func (f *rankReceiptClient) RankIssues(ctx context.Context, _ jira.RankRequest) (core.RankWrite, error) {
	f.calls.Add(1)
	if f.late {
		<-ctx.Done()
	}
	if f.cause != nil {
		return core.RankWrite{}, f.cause
	}
	return core.RankWrite{StatusCode: http.StatusNoContent}, nil
}

func TestRankDeadlinePreservesJoinedAcknowledgement(t *testing.T) {
	t.Parallel()
	client := &rankReceiptClient{late: true}
	cmd := newRank(Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }})
	if err := cmd.Flags().Set("before", "KEY-9"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1", "KEY-2"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || cli.MapError(err).Exit != cli.ExitTimeout || client.calls.Load() != 1 || retained.Op != "issue.rank" || !retained.Mutation || retained.Human == nil {
		t.Fatal("ranking lost its joined acknowledgement or original deadline")
	}
	out, ok := retained.Data.(envelope.IssueRankOutput)
	if !ok || out.Anchor != "KEY-9" || out.Position != "before" || len(out.Results) != 2 {
		t.Fatal("ranking deadline discarded the composed move")
	}
	for i, row := range out.Results {
		if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || !row.OK || row.Data == nil || row.Err != nil {
			t.Fatal("ranking deadline erased a joined native acknowledgement")
		}
	}
}

func TestRankTotalFailurePreservesOriginalTransportCause(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
	client := &rankReceiptClient{cause: cause}
	out, err := runRank(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "KEY-2"), "KEY-9", "")
	actual, ok := errors.AsType[*adapterjira.APIError](err)
	class := cli.MapError(err)
	if !ok || actual != cause || class.Code != "jira_rate_limited" || class.RetryAfterSeconds != 7 || class.Attempts != 1 || class.Waited != time.Millisecond || client.calls.Load() != 1 || out.Anchor != "KEY-9" || len(out.Results) != 2 {
		t.Fatal("rank failure lost original cause, transport facts or authored targets")
	}
	for _, row := range out.Results {
		if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "jira_rate_limited" {
			t.Fatal("failed batch ranking fabricated a move")
		}
	}
}
