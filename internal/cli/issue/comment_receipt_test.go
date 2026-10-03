package issue

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type lateCommentReceiptClient struct {
	unusedJiraClient
	calls int
}

func (f *lateCommentReceiptClient) AddComment(ctx context.Context, _ string, req jira.AddCommentRequest) (core.CommentWrite, error) {
	f.calls++
	<-ctx.Done()
	return core.CommentWrite{Comment: &jira.Comment{ID: jira.NullableOf("10042"), Body: jira.NullableOf(req.Body)}}, nil
}

func (f *lateCommentReceiptClient) UpdateComment(ctx context.Context, _, id string, req jira.UpdateCommentRequest, _ bool) (core.CommentWrite, error) {
	f.calls++
	<-ctx.Done()
	return core.CommentWrite{Comment: &jira.Comment{ID: jira.NullableOf(id), Body: jira.NullableOf(req.Body)}}, nil
}

// A joined client seam can return an acknowledgement after its deadline.
// This proves receipt retention, not a late acknowledgement from live Jira.
func TestCommentDeadlineRetainsJoinedReceipts(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "edit"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			body := bodyFromADF(t, adfBody)
			client := &lateCommentReceiptClient{}
			deps := Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if verb == "add" {
				out, err := runCommentAdd(ctx, deps, invocation(false, "KEY-1"), commentAddOptions{body: body, parallelism: 1})
				if !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Data.Comment == nil || out.Results[0].Data.Comment.ID != "10042" {
					t.Fatal("deadline discarded the joined created-comment receipt or changed the run error")
				}
			} else {
				out, err := runCommentEdit(ctx, deps, invocation(false, "KEY-1", "10042"), commentEditOptions{body: body, notify: true})
				if !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || out.Comment == nil || out.Comment.ID != "10042" || out.CommentID != "10042" || out.Issue.Key != "KEY-1" {
					t.Fatal("deadline discarded the joined edited-comment receipt or changed the run error")
				}
			}
		})
	}
}

func TestCommentEditDeadlinePublishesJoinedReceipt(t *testing.T) {
	t.Parallel()
	body := bodyFromADF(t, adfBody)
	client := &lateCommentReceiptClient{}
	cmd := newCommentEdit(Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }})
	if err := cmd.Flags().Set("json-input", body.JSONInput); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1", "10042"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.comment.edit" || !retained.Mutation || retained.Human == nil {
		t.Fatal("comment edit constructor discarded the joined receipt or its interruption contract")
	}
	out, ok := retained.Data.(envelope.IssueCommentEditOutput)
	if !ok || out.Comment == nil || out.Comment.ID != "10042" || out.Issue.Key != "KEY-1" || out.CommentID != "10042" || string(out.Body) != adfBody || out.BodyRoute != "adf" || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("comment edit constructor changed the retained identity, native body or timeout taxonomy")
	}
}

type invalidCommentReceiptClient struct {
	unusedJiraClient
	id jira.Nullable[string]
}

func (f *invalidCommentReceiptClient) AddComment(context.Context, string, jira.AddCommentRequest) (core.CommentWrite, error) {
	return core.CommentWrite{Comment: &jira.Comment{ID: f.id}}, nil
}

func (f *invalidCommentReceiptClient) UpdateComment(context.Context, string, string, jira.UpdateCommentRequest, bool) (core.CommentWrite, error) {
	return core.CommentWrite{Comment: &jira.Comment{ID: f.id}}, nil
}

func TestCommentWritesValidateSeamIdentity(t *testing.T) {
	t.Parallel()
	var absent jira.Nullable[string]
	for name, id := range map[string]jira.Nullable[string]{
		"absent": absent, "null": jira.NullableNull[string](), "empty": jira.NullableOf(""), "blank": jira.NullableOf(" \t"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &invalidCommentReceiptClient{id: id}
			added := postComment(t.Context(), client, "KEY-1", jira.AddCommentRequest{})
			if added.err == nil || added.result.OK || added.result.Data != nil || cli.MapError(added.err).Code != "comment_response_invalid" {
				t.Fatal("created-comment seam published a missing identity")
			}
			out, err := runCommentEdit(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "10042"), commentEditOptions{body: bodyFromADF(t, adfBody), notify: true})
			if err == nil || out.Comment != nil || cli.MapError(err).Code != "comment_response_invalid" {
				t.Fatal("edited-comment seam published a missing identity")
			}
		})
	}
	t.Run("wrong edited identity", func(t *testing.T) {
		t.Parallel()
		client := &invalidCommentReceiptClient{id: jira.NullableOf("10043")}
		out, err := runCommentEdit(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "10042"), commentEditOptions{body: bodyFromADF(t, adfBody), notify: true})
		if err == nil || out.Comment != nil || cli.MapError(err).Code != "comment_response_invalid" {
			t.Fatal("edited-comment seam published another comment's receipt")
		}
	})
}

func TestCommentAddTotalFailurePreservesOriginalTransportCause(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
	client := &fakeCommentAddClient{failures: map[string]error{"KEY-1": cause, "KEY-2": cause}}
	results, err := commentAddFanOut(t.Context(), client, []string{"KEY-1", "KEY-2"}, jira.AddCommentRequest{}, 1)
	actual, ok := errors.AsType[*adapterjira.APIError](err)
	class := cli.MapError(err)
	if !ok || actual != cause || len(results) != 2 || results[0].OK || results[1].OK || class.Code != "jira_rate_limited" || class.RetryAfterSeconds != 7 || class.Attempts != 1 || class.Waited != time.Millisecond {
		t.Fatal("comment total failure rebuilt its original transport cause or discarded its facts")
	}
}
