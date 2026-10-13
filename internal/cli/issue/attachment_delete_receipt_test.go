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

type receiptAttachmentDeleteClient struct {
	unusedJiraClient
	calls int
	late  bool
	cause error
}

func (f *receiptAttachmentDeleteClient) DeleteAttachment(ctx context.Context, _ string) (core.AttachmentDeleteWrite, error) {
	f.calls++
	if f.late {
		<-ctx.Done()
		return core.AttachmentDeleteWrite{StatusCode: http.StatusNoContent, Retry: core.RetryInfo{Attempts: 1}}, nil
	}
	return core.AttachmentDeleteWrite{}, f.cause
}

func TestAttachmentDeleteRequiresExplicitSeamAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{nil, context.Canceled} {
		t.Run(map[bool]string{false: "zero without error", true: "unacknowledged cancellation"}[cause != nil], func(t *testing.T) {
			t.Parallel()
			client := &receiptAttachmentDeleteClient{cause: cause}
			out, err := runAttachmentDelete(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "10042"), true)
			if out.AttachmentID != "" || err == nil || client.calls != 1 {
				t.Fatal("attachment deletion published an identity without an acknowledgement")
			}
			if cause != nil {
				if !errors.Is(err, cause) {
					t.Fatal("unacknowledged deletion replaced its original cancellation")
				}
			} else if !errors.Is(err, core.ErrAttachmentResponseInvalid) {
				t.Fatal("zero deletion receipt was not a classified protocol fault")
			}
		})
	}
}

func TestAttachmentDeleteDeadlinePublishesJoinedAcknowledgement(t *testing.T) {
	t.Parallel()
	client := &receiptAttachmentDeleteClient{late: true}
	cmd := newAttachmentDelete(Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }})
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"10042"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.attachment.delete" || !retained.Mutation || retained.Human == nil {
		t.Fatal("attachment delete constructor discarded the joined acknowledgement or changed its deadline")
	}
	out, ok := retained.Data.(envelope.IssueAttachmentDeleteOutput)
	if !ok || out.AttachmentID != "10042" || out.DryRun || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("attachment delete constructor changed its acknowledged identity or timeout classification")
	}
}
