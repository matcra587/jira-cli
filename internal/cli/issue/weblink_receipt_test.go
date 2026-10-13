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

type weblinkReceiptSeamClient struct {
	unusedJiraClient
	cause error
	late  bool
	link  *jira.RemoteIssueLink
	calls atomic.Int64
}

func (f *weblinkReceiptSeamClient) GetRemoteLinks(ctx context.Context, _ string) (core.RemoteLinkRead, error) {
	f.calls.Add(1)
	if f.late {
		<-ctx.Done()
	}
	if f.cause != nil {
		return core.RemoteLinkRead{}, f.cause
	}
	return core.RemoteLinkRead{Links: []jira.RemoteIssueLink{{ID: jira.NullableOf(int64(10042)), Object: jira.NullableOf(jira.RemoteObject{URL: jira.NullableOf("https://example.com/runbook"), Title: jira.NullableOf("Runbook")})}}}, nil
}

func (f *weblinkReceiptSeamClient) AddRemoteLink(ctx context.Context, _ string, _ jira.RemoteLinkRequest) (core.RemoteLinkWrite, error) {
	f.calls.Add(1)
	if f.late {
		<-ctx.Done()
	}
	return core.RemoteLinkWrite{Link: f.link, Created: true}, f.cause
}

func TestWeblinkDeadlinePublishesJoinedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			client := &weblinkReceiptSeamClient{late: true, link: &jira.RemoteIssueLink{ID: jira.NullableOf(int64(10042))}}
			deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}
			cmd := newWeblinkList(deps)
			if verb == "add" {
				cmd = newWeblinkAdd(deps)
				for name, value := range map[string]string{"url": "https://example.com/runbook", "title": "Runbook"} {
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
			if !ok || !errors.Is(err, context.DeadlineExceeded) || cli.MapError(err).Exit != cli.ExitTimeout || client.calls.Load() != 1 || retained.Op != "issue.weblink."+verb || retained.Human == nil || retained.Mutation != (verb == "add") {
				t.Fatal("remote-link constructor lost its joined receipt or original deadline contract")
			}
			if verb == "list" {
				out, ok := retained.Data.(envelope.IssueWeblinkListOutput)
				if !ok || len(out.Results) != 1 || out.Results[0].Issue.Key != "KEY-1" || !out.Results[0].OK || out.Results[0].Data == nil || len(out.Results[0].Data.Weblinks) != 1 || out.Results[0].Data.Weblinks[0].ID != "10042" {
					t.Fatal("late joined remote-link read lost its target or identity")
				}
			} else {
				out, ok := retained.Data.(envelope.IssueWeblinkAddOutput)
				if !ok || out.URL != "https://example.com/runbook" || out.Title != "Runbook" || len(out.Results) != 1 || out.Results[0].Issue.Key != "KEY-1" || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Data.Weblink == nil || out.Results[0].Data.Weblink.ID != "10042" || out.Results[0].Data.Created == nil || !*out.Results[0].Data.Created {
					t.Fatal("late joined remote-link write lost request, identity or creation verdict")
				}
			}
		})
	}
}

func TestWeblinkTotalFailurePreservesOriginalTransportCause(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
			client := &weblinkReceiptSeamClient{cause: cause}
			var err error
			if verb == "list" {
				out, failed := runWeblinkList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1", "KEY-2"}, 1)
				err = failed
				if len(out.Results) != 2 || out.Results[0].OK || out.Results[1].OK {
					t.Fatal("remote-link read discarded total-failure outcomes")
				}
			} else {
				results, failed := weblinkAddFanOut(t.Context(), client, []string{"KEY-1", "KEY-2"}, jira.RemoteLinkRequest{}, 1)
				err = failed
				if len(results) != 2 || results[0].OK || results[1].OK {
					t.Fatal("remote-link write discarded total-failure outcomes")
				}
			}
			actual, ok := errors.AsType[*adapterjira.APIError](err)
			class := cli.MapError(err)
			if !ok || actual != cause || class.Code != "jira_rate_limited" || class.RetryAfterSeconds != 7 || class.Attempts != 1 || class.Waited != time.Millisecond || client.calls.Load() != 2 {
				t.Fatal("remote-link failure rebuilt original cause or lost its transport facts")
			}
		})
	}
}

func TestWeblinkRejectsIdentitylessClientAcknowledgements(t *testing.T) {
	t.Parallel()
	for name, link := range map[string]*jira.RemoteIssueLink{"nil": nil, "missing": {}, "null": {ID: jira.NullableNull[int64]()}, "zero": {ID: jira.NullableOf(int64(0))}, "negative": {ID: jira.NullableOf(int64(-1))}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &weblinkReceiptSeamClient{link: link}
			outcome := attachWeblink(t.Context(), client, "KEY-1", jira.RemoteLinkRequest{})
			if outcome.result.OK || outcome.result.Data != nil || outcome.result.Err == nil || outcome.result.Err.Code != "weblink_response_invalid" || client.calls.Load() != 1 {
				t.Fatal("alternate client seam fabricated a remote-link identity or replayed the write")
			}
		})
	}
}
