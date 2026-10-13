package issue

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type receiptIssueCreateClient struct {
	unusedJiraClient
	issue  *jira.CreatedIssue
	cause  error
	late   bool
	calls  int
	reads  int
	status int
}

func TestIssueCreateRequiresExplicitSeamAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, status := range []int{0, 200, 202, 204} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			client := &receiptIssueCreateClient{status: status, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}}
			in := writeFlagsFor(t, command.CreateIssueVerb, "--project=KEY", "--type=Task", "--summary=authored")
			out, err := runCreate(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false), in, createOptions{verify: true})
			if err == nil || cli.MapError(err).Code != "issue_create_response_invalid" || client.calls != 1 || client.reads != 0 || out.Issue != nil || out.Applied || !out.Attempted || len(out.Payload) == 0 {
				t.Fatal("creation claimed a seam identity without native201 or discarded an uncertain attempt")
			}
		})
	}
}

func (f *receiptIssueCreateClient) CreateIssue(ctx context.Context, _ jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	f.calls++
	if f.late {
		<-ctx.Done()
	}
	return core.IssueCreateWrite{StatusCode: f.status, Issue: f.issue, Retry: core.RetryInfo{Attempts: 1}}, f.cause
}

func (f *receiptIssueCreateClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	f.reads++
	return core.IssueRead{Issue: cloneSource()}, nil
}

func TestIssueCreateRetainsReturnedIdentityAndOriginalCause(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			client := &receiptIssueCreateClient{status: 201, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}, cause: cause}
			in := writeFlagsFor(t, command.CreateIssueVerb, "--project=KEY", "--type=Task", "--summary=authored")
			out, err := runCreate(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false), in, createOptions{verify: true})
			if !errors.Is(err, cause) || client.calls != 1 || client.reads != 0 || out.Issue == nil || out.Issue.Key != "NEW-1" || out.Issue.ID != "9001" || len(out.Payload) == 0 || out.Verified != nil || out.VerifyFailed {
				t.Fatal("creation discarded its returned identity or original cause, or read back after failure")
			}
		})
	}
}

func TestIssueCreateDeadlinePublishesJoinedIdentity(t *testing.T) {
	t.Parallel()
	client := &receiptIssueCreateClient{status: 201, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}, late: true}
	cmd := newCreate(Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }})
	if err := cmd.ParseFlags([]string{"--project=KEY", "--type=Task", "--summary=authored", "--verify"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, nil)
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || client.reads != 0 || retained.Op != "issue.create" || !retained.Mutation || retained.Human == nil {
		t.Fatalf("creation constructor discarded its joined identity or deadline: calls=%d err=%v", client.calls, err)
	}
	out, ok := retained.Data.(envelope.IssueCreateOutput)
	if !ok || out.Issue == nil || out.Issue.Key != "NEW-1" || len(out.Payload) == 0 || out.DryRun || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("creation constructor changed its identity, payload or timeout classification")
	}
}

func TestIssueCreateRefusesUnusableIdentity(t *testing.T) {
	t.Parallel()
	for name, identity := range map[string]*jira.CreatedIssue{
		"null":        nil,
		"empty":       {},
		"missing id":  {Key: "NEW-1"},
		"missing key": {ID: "9001"},
		"blank id":    {ID: " \t", Key: "NEW-1"},
		"blank key":   {ID: "9001", Key: " \n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &receiptIssueCreateClient{status: 201, issue: identity}
			in := writeFlagsFor(t, command.CreateIssueVerb, "--project=KEY", "--type=Task", "--summary=authored")
			out, err := runCreate(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false), in, createOptions{verify: true})
			if err == nil || cli.MapError(err).Code != "issue_create_response_invalid" || client.calls != 1 || client.reads != 0 || out.Issue != nil || len(out.Payload) == 0 {
				t.Fatal("creation accepted an unusable identity or discarded its attempted payload")
			}
		})
	}
}
