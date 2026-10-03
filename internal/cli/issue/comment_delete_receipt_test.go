package issue

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type lateCommentDeleteClient struct {
	unusedJiraClient
	calls int
}

type unacknowledgedCommentDeleteClient struct {
	unusedJiraClient
	cause error
}

func (f *unacknowledgedCommentDeleteClient) DeleteComment(context.Context, string, string) (core.CommentDeleteWrite, error) {
	return core.CommentDeleteWrite{}, f.cause
}

func TestCommentDeleteRequiresExplicitSeamAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{nil, context.Canceled} {
		t.Run(map[bool]string{false: "zero without error", true: "unacknowledged cancellation"}[cause != nil], func(t *testing.T) {
			t.Parallel()
			client := &unacknowledgedCommentDeleteClient{cause: cause}
			out, err := runCommentDelete(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "10042"), true)
			if out.CommentID != "" || out.Issue.Key != "" || err == nil {
				t.Fatal("comment deletion published an identity without an acknowledgement")
			}
			if cause != nil {
				if !errors.Is(err, cause) {
					t.Fatal("unacknowledged deletion replaced its original cancellation")
				}
			} else if !errors.Is(err, core.ErrCommentResponseInvalid) {
				t.Fatal("zero deletion receipt was not a classified protocol fault")
			}
		})
	}
}

func (f *lateCommentDeleteClient) DeleteComment(ctx context.Context, _, _ string) (core.CommentDeleteWrite, error) {
	f.calls++
	<-ctx.Done()
	return core.CommentDeleteWrite{StatusCode: http.StatusNoContent, Retry: core.RetryInfo{Attempts: 1}}, nil
}

func TestCommentDeleteDeadlinePublishesJoinedAcknowledgement(t *testing.T) {
	t.Parallel()
	client := &lateCommentDeleteClient{}
	cmd := newCommentDelete(Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }})
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1", "10042"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.comment.delete" || !retained.Mutation || retained.Human == nil {
		t.Fatal("comment delete constructor discarded the joined acknowledgement or changed its deadline")
	}
	out, ok := retained.Data.(envelope.IssueCommentDeleteOutput)
	if !ok || out.Issue.Key != "KEY-1" || out.CommentID != "10042" || out.DryRun || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("comment delete constructor changed its acknowledged identity or timeout classification")
	}
}
