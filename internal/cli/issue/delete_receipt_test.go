package issue

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clog"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestIssueDeleteHumanKeepsAcknowledgementAndFailure(t *testing.T) {
	withStyle(t, false)
	var buf bytes.Buffer
	out := envelope.IssueDeleteOutput{KeyedResults: envelope.KeyedResults[envelope.IssueDeleteData]{Results: []envelope.KeyedResult[envelope.IssueDeleteData]{
		{Issue: envelope.IssueRef{Key: "KEY-1"}, Data: &envelope.IssueDeleteData{}, Err: &envelope.KeyError{Code: cli.CodeCanceled, Message: "context canceled", Hint: "Inspect before retrying."}},
		{Issue: envelope.IssueRef{Key: "KEY-2"}, Err: &envelope.KeyError{Code: cli.CodeCanceled, Message: "context canceled", Hint: "Inspect before retrying."}},
	}}}
	if err := renderDelete(output.NewView(&buf, clog.ColorNever), envelope.Envelope{Data: out}); err != nil {
		t.Fatal(err)
	}
	human := buf.String()
	if !strings.Contains(human, "KEY-1: deleted.") || !strings.Contains(human, "KEY-1: context canceled") || !strings.Contains(human, "KEY-2: context canceled") || strings.Contains(human, "KEY-2: deleted.") || !strings.Contains(human, "Inspect before retrying.") {
		t.Fatal("human deletion output lost an observed acknowledgement or invented an unobserved deletion")
	}
}

type receiptIssueDeleteClient struct {
	unusedJiraClient
	calls int
	late  bool
	cause error
	ack   bool
}

func (f *receiptIssueDeleteClient) DeleteIssue(ctx context.Context, _ string, _ bool) (core.IssueDeleteWrite, error) {
	f.calls++
	if f.late {
		<-ctx.Done()
		return core.IssueDeleteWrite{StatusCode: http.StatusNoContent, Retry: core.RetryInfo{Attempts: 1}}, nil
	}
	write := core.IssueDeleteWrite{}
	if f.ack {
		write.StatusCode = http.StatusNoContent
	}
	return write, f.cause
}

func TestIssueDeleteAcknowledgementAlongsideCauseRetainsBoth(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			client := &receiptIssueDeleteClient{ack: true, cause: cause}
			out, err := runDelete(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1"), deleteOptions{force: true, parallelism: 1})
			if !errors.Is(err, cause) || client.calls != 1 || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].Err == nil || out.Results[0].Err.Code != cli.MapError(cause).Code {
				t.Fatal("issue deletion lost its joined acknowledgement or original cause")
			}
		})
	}
}

func TestIssueDeleteFailureRetainsSelectorAndOriginalCause(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte("KEY-2\nKEY-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := &adapterjira.APIError{StatusCode: 429, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1}}
	client := &receiptIssueDeleteClient{cause: cause}
	out, err := runDelete(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false), deleteOptions{selector: command.SelectorFlags{FromFile: path}, force: true, deleteSubtasks: true, parallelism: 1})
	if !errors.Is(err, cause) || client.calls != 2 || out.Selector == nil || !out.Selector.Resolved || out.Selector.Kind != "from-file" || !out.DeleteSubtasks || out.DryRun || len(out.Results) != 2 {
		t.Fatal("issue deletion discarded its selector, original cause or ordered outcomes")
	}
	for i, key := range []string{"KEY-2", "KEY-1"} {
		row := out.Results[i]
		if row.Issue.Key != key || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "jira_rate_limited" {
			t.Fatal("failed issue deletion fabricated an acknowledgement or reordered targets")
		}
	}
	class := cli.MapError(err)
	if class.RetryAfterSeconds != 7 || class.Attempts != 1 {
		t.Fatal("issue deletion replaced native retry facts")
	}
}

func TestIssueDeleteDeadlinePublishesJoinedAcknowledgement(t *testing.T) {
	t.Parallel()
	client := &receiptIssueDeleteClient{late: true}
	cmd := newDelete(Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }})
	if err := cmd.ParseFlags([]string{"KEY-1"}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"force", "delete-subtasks"} {
		if err := cmd.Flags().Set(flag, "true"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || retained.Op != "issue.delete" || !retained.Mutation || retained.Human == nil {
		t.Fatalf("issue delete constructor discarded its joined acknowledgement or deadline: calls=%d err=%v", client.calls, err)
	}
	out, ok := retained.Data.(envelope.IssueDeleteOutput)
	if !ok || !out.DeleteSubtasks || out.DryRun || len(out.Results) != 1 || out.Results[0].Issue.Key != "KEY-1" || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Err != nil || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("issue delete constructor changed its acknowledgement, cascade or timeout classification")
	}
}

func TestIssueDeleteRequiresExplicitSeamAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cause error
		code  string
	}{
		{name: "zero without error", code: "issue_delete_response_invalid"},
		{name: "unacknowledged cancellation", cause: context.Canceled, code: cli.CodeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &receiptIssueDeleteClient{cause: tc.cause}
			out, err := runDelete(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1"), deleteOptions{force: true, parallelism: 1})
			if err == nil || client.calls != 1 || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data != nil || out.Results[0].Err == nil || out.Results[0].Err.Code != tc.code {
				t.Fatal("issue deletion claimed deletion without an explicit acknowledgement or discarded its failure")
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatal("issue deletion replaced its original cancellation")
			}
		})
	}
}
