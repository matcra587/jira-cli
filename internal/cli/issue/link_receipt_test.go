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

type linkReceiptSeamClient struct {
	unusedJiraClient
	cause error
	late  bool
	calls atomic.Int64
}

func (*linkReceiptSeamClient) BaseURL() string { return "https://example.atlassian.net" }
func (*linkReceiptSeamClient) GetIssueLinkTypes(context.Context) (core.LinkTypeRead, error) {
	return core.LinkTypeRead{Types: &jira.IssueLinkTypes{IssueLinkTypes: jira.NullableOf([]jira.IssueLinkType{blockingLinkType().Value})}}, nil
}

func (f *linkReceiptSeamClient) GetIssue(ctx context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	f.calls.Add(1)
	if f.late {
		<-ctx.Done()
	}
	if f.cause != nil {
		return core.IssueRead{}, f.cause
	}
	return core.IssueRead{Issue: issueWithLinks(key)}, nil
}

func (f *linkReceiptSeamClient) LinkIssues(ctx context.Context, _ jira.LinkIssueRequest) (core.LinkWrite, error) {
	f.calls.Add(1)
	if f.late {
		<-ctx.Done()
	}
	return core.LinkWrite{}, f.cause
}

func TestLinkDeadlinePublishesJoinedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "create"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			client := &linkReceiptSeamClient{late: true}
			deps := Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}
			cmd := newLinkList(deps)
			if verb == "create" {
				cmd = newLinkCreate(deps)
				for name, value := range map[string]string{"to": "KEY-9", "type": "Blocks"} {
					if err := cmd.Flags().Set(name, value); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			cmd.SetContext(ctx)
			err := cmd.RunE(cmd, []string{"KEY-1"})
			retained, ok := errors.AsType[*cli.ResultError](err)
			if !ok || !errors.Is(err, context.DeadlineExceeded) || cli.MapError(err).Exit != cli.ExitTimeout || client.calls.Load() != 1 || retained.Op != "issue.link."+verb || retained.Human == nil || retained.Mutation != (verb == "create") {
				t.Fatal("link constructor lost its joined receipt or original deadline contract")
			}
			if verb == "list" {
				out, ok := retained.Data.(envelope.IssueLinkListOutput)
				if !ok || len(out.Results) != 1 || out.Results[0].Issue.Key != "KEY-1" || !out.Results[0].OK || out.Results[0].Data == nil || len(out.Results[0].Data.Links) != 0 {
					t.Fatal("late joined read lost its target or complete empty link observation")
				}
			} else {
				out, ok := retained.Data.(envelope.IssueLinkCreateOutput)
				if !ok || out.To.Key != "KEY-9" || !out.TypeResolved || out.Type == nil || out.Type.Name != "Blocks" || out.Relation != "blocks" || len(out.Results) != 1 || out.Results[0].Issue.Key != "KEY-1" || !out.Results[0].OK || out.Results[0].Data == nil {
					t.Fatal("late joined write lost its target, relationship or acknowledgement")
				}
			}
		})
	}
}

func TestLinkTotalFailurePreservesOriginalTransportCause(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "create"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
			client := &linkReceiptSeamClient{cause: cause}
			var err error
			if verb == "list" {
				out, _, failure := runLinkList(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, []string{"KEY-1", "KEY-2"}, 1)
				err = failure
				if len(out.Results) != 2 || out.Results[0].OK || out.Results[1].OK {
					t.Fatal("link read discarded total-failure outcomes")
				}
			} else {
				results, failure := linkCreateFanOut(t.Context(), client, []string{"KEY-1", "KEY-2"}, "KEY-9", resolvedLinkType{}, nil, 1)
				err = failure
				if len(results) != 2 || results[0].OK || results[1].OK {
					t.Fatal("link write discarded total-failure outcomes")
				}
			}
			actual, ok := errors.AsType[*adapterjira.APIError](err)
			class := cli.MapError(err)
			if !ok || actual != cause || class.Code != "jira_rate_limited" || class.RetryAfterSeconds != 7 || class.Attempts != 1 || class.Waited != time.Millisecond || client.calls.Load() != 2 {
				t.Fatal("link failure rebuilt its original cause or lost its transport facts")
			}
		})
	}
}
