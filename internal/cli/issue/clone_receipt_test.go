package issue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueCloneRetainsCreatedIdentityAndCause(t *testing.T) {
	t.Parallel()
	cause := context.Canceled
	client := &receiptIssueCreateClient{status: 201, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}, cause: cause}
	out, err := runClone(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1"), cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 1})
	if !errors.Is(err, cause) || client.calls != 1 || client.reads != 1 || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Data.Clone == nil || out.Results[0].Data.Clone.Key != "NEW-1" || out.Results[0].Err == nil || out.Results[0].Err.Code != cli.CodeCanceled {
		t.Fatal("clone discarded its returned identity or original creation cause")
	}
}

func TestIssueCloneFailedCreationRetainsReplacementFact(t *testing.T) {
	t.Parallel()
	cause := errors.New("fixture creation interrupted after replacement")
	client := &receiptIssueCreateClient{status: 201, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}, cause: cause}
	results, replaced, err := cloneFanOut(t.Context(), client, []string{"KEY-1"}, cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 1, replacements: []cloneReplacement{{old: "Original", new: "Changed"}}}, jira.IssueWriteRequest{})
	if !replaced || !errors.Is(err, cause) || len(results) != 1 || results[0].OK || results[0].Data == nil || results[0].Data.Clone == nil || results[0].Data.Clone.Key != "NEW-1" || client.calls != 1 {
		t.Fatal("failed clone lost its composed replacement fact or acknowledged identity")
	}
}

func TestIssueCloneDeadlinePublishesJoinedIdentity(t *testing.T) {
	t.Parallel()
	client := &receiptIssueCreateClient{status: 201, issue: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}, late: true}
	cmd := newClone(Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }})
	if err := cmd.ParseFlags([]string{"KEY-1", "--parallelism=1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := cmd.RunE(cmd, []string{"KEY-1"})
	retained, ok := errors.AsType[*cli.ResultError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || client.calls != 1 || client.reads != 1 || retained.Op != "issue.clone" || !retained.Mutation || retained.Human == nil {
		t.Fatalf("clone constructor discarded its joined identity or deadline: calls=%d err=%v", client.calls, err)
	}
	out, ok := retained.Data.(envelope.IssueCloneOutput)
	if !ok || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || !out.Results[0].Data.Applied || out.Results[0].Data.Clone == nil || out.Results[0].Data.Clone.Key != "NEW-1" || out.Results[0].Err != nil || cli.MapError(err).Exit != cli.ExitTimeout {
		t.Fatal("clone constructor changed its completed receipt or timeout classification")
	}
}

func TestIssueCloneTotalFailureRetainsSelectorAndOrderedOutcomes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte("KEY-2\nKEY-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("fixture create failed")
	client := &receiptIssueCreateClient{cause: cause}
	out, err := runClone(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false), cloneOptions{fields: &command.IssueFieldFlags{}, selector: command.SelectorFlags{FromFile: path}, force: true, parallelism: 1})
	if !errors.Is(err, cause) || client.calls != 2 || client.reads != 2 || out.Selector == nil || !out.Selector.Resolved || out.Selector.Kind != "from-file" || len(out.Results) != 2 {
		t.Fatal("clone discarded its selector, cause or ordered failures")
	}
	for i, key := range []string{"KEY-2", "KEY-1"} {
		row := out.Results[i]
		if row.OK || row.Issue.Key != key || row.Err == nil || row.Data != nil {
			t.Fatal("failed clone changed source order or invented a creation acknowledgement")
		}
	}
}
