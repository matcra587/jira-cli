package issue

import (
	"context"
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

type lateAttachmentReadClient struct {
	unusedJiraClient
	calls int
	empty bool
}

type observedAttachmentReadClient struct {
	unusedJiraClient
	issue *jira.IssueBean
	cause error
}

func (f *observedAttachmentReadClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	return core.IssueRead{Issue: f.issue}, f.cause
}

func TestAttachmentListJoinedObservationPreservesOriginalCause(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusForbidden, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1}}
	client := &observedAttachmentReadClient{issue: attachmentBean("NEW-2", wireAttachment("10042", "report.txt", "text/plain", 3)), cause: cause}
	out, err := runAttachmentList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"OLD-1"}, 1)
	actual, ok := errors.AsType[*adapterjira.APIError](err)
	if !ok || actual != cause || len(out.Results) != 1 {
		t.Fatal("attachment list rebuilt its original joined read cause")
	}
	row := out.Results[0]
	if row.OK || row.Err == nil || row.Err.Code != "jira_forbidden" || row.Issue.Key != "NEW-2" || row.Data == nil || len(row.Data.Attachments) != 1 || row.Data.Attachments[0].ID != "10042" || row.Data.Attachments[0].Filename != "report.txt" || row.Data.Attachments[0].Size != 3 {
		t.Fatal("attachment list discarded usable joined data or claimed uninterrupted success")
	}
}

func TestAttachmentListAlternateSeamRefusesUnusableObservation(t *testing.T) {
	t.Parallel()
	client := &observedAttachmentReadClient{issue: &jira.IssueBean{}}
	out, err := runAttachmentList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1"}, 1)
	if !errors.Is(err, core.ErrAttachmentResponseInvalid) || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data != nil || out.Results[0].Err == nil || out.Results[0].Err.Code != cli.CodeAttachmentResponseInvalid {
		t.Fatal("alternate client bypassed attachment observation validation")
	}
}

func TestAttachmentListHumanRetainsJoinedObservation(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "attachment", true: "empty array"}[empty], func(t *testing.T) {
			attachments := []envelope.AttachmentView{}
			if !empty {
				attachments = append(attachments, envelope.AttachmentView{ID: "10042", Filename: "report.txt\x1b[31m\a", Size: 3})
			}
			out := envelope.IssueAttachmentListOutput{}
			out.Results = []envelope.KeyedResult[envelope.IssueAttachmentListData]{{
				Issue: envelope.IssueRef{Key: "KEY-1"}, Data: &envelope.IssueAttachmentListData{Attachments: attachments},
				Err: &envelope.KeyError{Code: "jira_forbidden", Message: "read incomplete", Hint: "Inspect retained observations."},
			}}
			human := renderAttachments(t, out, clog.ColorNever)
			if !strings.Contains(human, "read incomplete") || strings.Contains(human, "No attachments.") || strings.ContainsAny(human, "\x1b\a") || !empty && (!strings.Contains(human, "report.txt") || !strings.Contains(human, "10042")) {
				t.Fatal("incomplete human attachment read hid its observation or claimed an empty issue")
			}
		})
	}
}

func (f *lateAttachmentReadClient) GetIssue(ctx context.Context, key string, fields []string, _ ...string) (core.IssueRead, error) {
	f.calls++
	if len(fields) != 1 || fields[0] != "attachment" {
		return core.IssueRead{}, errors.New("attachment read changed its requested fields")
	}
	<-ctx.Done()
	issue := attachmentBean(key)
	if !f.empty {
		issue = attachmentBean(key, wireAttachment("10042", "report.txt", "text/plain", 3))
	}
	return core.IssueRead{Issue: issue}, nil
}

// This seam returns an observed issue after interruption. It proves owner
// retention without claiming a live server returned a late response.
func TestAttachmentListDeadlinePublishesJoinedObservation(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "attachment", true: "empty array"}[empty], func(t *testing.T) {
			t.Parallel()
			client := &lateAttachmentReadClient{empty: empty}
			cmd := newAttachmentList(Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }})
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			cmd.SetContext(ctx)
			err := cmd.RunE(cmd, []string{"KEY-1"})
			retained, ok := errors.AsType[*cli.ResultError](err)
			if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.attachment.list" || retained.Mutation || retained.Human == nil {
				t.Fatal("attachment list constructor discarded joined observations or changed interruption")
			}
			out, ok := retained.Data.(envelope.IssueAttachmentListOutput)
			if !ok || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].Data == nil || out.Results[0].Data.Attachments == nil || cli.MapError(err).Exit != cli.ExitTimeout {
				t.Fatal("attachment list changed its observed issue or original timeout")
			}
			attachments := out.Results[0].Data.Attachments
			if empty {
				if len(attachments) != 0 {
					t.Fatal("empty observation invented an attachment")
				}
			} else if len(attachments) != 1 || attachments[0].ID != "10042" || attachments[0].Filename != "report.txt" || attachments[0].Size != 3 {
				t.Fatal("joined observation lost native attachment fields")
			}
		})
	}
}

func TestAttachmentListTotalFailurePreservesOriginalCause(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusForbidden, Messages: []string{"permission refused"}}
	client := &fakeLinkClient{failures: map[string]error{
		"KEY-1": &adapterjira.APIError{StatusCode: http.StatusNotFound},
		"KEY-2": cause,
	}}
	out, err := runAttachmentList(t.Context(), linkDeps(client), []string{"KEY-1", "KEY-2"}, 1)
	actual, ok := errors.AsType[*adapterjira.APIError](err)
	if !ok || actual != cause || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-1" || out.Results[1].Issue.Key != "KEY-2" || out.Results[0].Err == nil || out.Results[0].Err.Code != "jira_not_found" || out.Results[1].Err == nil || out.Results[1].Err.Code != "jira_forbidden" {
		t.Fatal("attachment list discarded ordered failures or rebuilt the original transport cause")
	}
}
