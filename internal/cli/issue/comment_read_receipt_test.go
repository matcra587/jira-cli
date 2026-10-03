package issue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clog"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type lateCommentReadClient struct {
	unusedJiraClient
	calls int
	empty bool
}

func (f *lateCommentReadClient) GetComments(ctx context.Context, _ string, start, limit int, _ core.CommentOrder) (core.CommentRead, error) {
	f.calls++
	<-ctx.Done()
	if f.empty {
		return core.CommentRead{Page: &jira.CommentPage{StartAt: start, MaxResults: limit, Comments: []jira.Comment{}}}, nil
	}
	return core.CommentRead{Page: &jira.CommentPage{
		StartAt: start, MaxResults: limit, Total: 1,
		Comments: []jira.Comment{{ID: jira.NullableOf("10042"), Body: jira.NullableOf(json.RawMessage(adfBody))}},
	}}, nil
}

func TestCommentListDeadlineRetainsObservedEmptyPage(t *testing.T) {
	client := &lateCommentReadClient{empty: true}
	cmd := newCommentList(Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("comment read discarded the joined empty page or its interruption")
	}
	out, ok := retained.Data.(envelope.IssueCommentListOutput)
	if !ok || out.Comments == nil || len(out.Comments) != 0 || !out.Truncated || out.TruncatedReason != "read_failed" || out.Pagination.Total != 0 {
		t.Fatal("joined empty page fabricated data or claimed uninterrupted completion")
	}
	human := renderComments(t, out, clog.ColorNever)
	if !strings.Contains(human, "incomplete") || strings.Contains(human, "No comments.") || strings.Contains(human, "safety cap") {
		t.Fatal("interrupted empty observation became a successful empty thread or safety cap")
	}
}

// The joined seam supplies a usable page after interruption. This is owner
// evidence, not a claim that a live server returned a late response.
func TestCommentListDeadlinePublishesJoinedPage(t *testing.T) {
	t.Parallel()
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "page", true: "walk"}[all], func(t *testing.T) {
			t.Parallel()
			client := &lateCommentReadClient{}
			cmd := newCommentList(Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }})
			if all {
				if err := cmd.Flags().Set("all", "true"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			cmd.SetContext(ctx)
			err := cmd.RunE(cmd, []string{"KEY-1"})
			retained, ok := errors.AsType[*cli.ResultError](err)
			if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.comment.list" || retained.Mutation || retained.Human == nil {
				t.Fatal("comment read constructor discarded the joined page or changed its deadline")
			}
			out, ok := retained.Data.(envelope.IssueCommentListOutput)
			if !ok || out.Issue.Key != "KEY-1" || len(out.Comments) != 1 || out.Comments[0].ID != "10042" || string(out.Comments[0].Body) != adfBody || out.Pagination.Returned != 1 || !out.Truncated || out.TruncatedReason != "read_failed" || cli.MapError(err).Exit != cli.ExitTimeout {
				t.Fatal("comment read constructor changed native data or claimed uninterrupted completion")
			}
		})
	}
}

func TestCommentListJoinedPagePreservesOriginalReadCause(t *testing.T) {
	t.Parallel()
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "page", true: "walk"}[all], func(t *testing.T) {
			t.Parallel()
			cause := &adapterjira.APIError{StatusCode: http.StatusForbidden, Retry: core.RetryInfo{Attempts: 1}}
			client := &fakeCommentClient{read: core.CommentRead{Page: &jira.CommentPage{
				StartAt: 0, MaxResults: 50, Total: 1, Comments: []jira.Comment{{ID: jira.NullableOf("10042")}},
			}}, err: cause}
			deps := Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}
			var out envelope.IssueCommentListOutput
			var err error
			if all {
				out, err = drainCommentList(t.Context(), deps, "KEY-1", core.CommentOrderDefault)
			} else {
				out, err = runCommentList(t.Context(), deps, "KEY-1", 0, 50, core.CommentOrderDefault)
			}
			actual, ok := errors.AsType[*adapterjira.APIError](err)
			if !ok || actual != cause || len(out.Comments) != 1 || out.Comments[0].ID != "10042" || !out.Truncated || cli.MapError(err).Code != "jira_forbidden" {
				t.Fatal("comment read discarded usable joined data or rebuilt its original transport cause")
			}
		})
	}
}
