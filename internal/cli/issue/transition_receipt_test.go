package issue

import (
	"bytes"
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
	"github.com/matcra587/jira-cli/internal/output"
)

type lateTransitionReceiptClient struct{ *fakeTransitionClient }

func (f *lateTransitionReceiptClient) DoTransition(ctx context.Context, key string, req jira.TransitionRequest) (core.TransitionWrite, error) {
	<-ctx.Done()
	return f.fakeTransitionClient.DoTransition(ctx, key, req)
}

func TestTransitionDeadlineRetainsJoinedOutcomes(t *testing.T) {
	opts := transitionOpts(t, false)
	opts.parallelism = 1
	client := &lateTransitionReceiptClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	out, err := runTransition(ctx, Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(strings.ToLower(cli.MapError(err).Hint), "inspect") || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].Data == nil || out.Results[0].Data.Transition.ID != "31" || len(client.movedKeys()) != 1 {
		t.Fatal("deadline discarded the joined transition acknowledgement or changed the original run error")
	}
}

func TestTransitionTotalFailureRetainsResolvedTargets(t *testing.T) {
	opts := transitionOpts(t, false)
	client := &fakeTransitionClient{screens: map[string]bool{"KEY-1": false, "KEY-2": false}, transitionErr: map[string]error{
		"KEY-1": &adapterjira.APIError{StatusCode: http.StatusForbidden}, "KEY-2": &adapterjira.APIError{StatusCode: http.StatusNotFound},
	}}
	out, err := runTransition(t.Context(), transitionDeps(client), invocation(false, "KEY-1", "KEY-2", "Done"), opts)
	if cli.MapError(err).Code != "jira_forbidden" || len(out.Results) != 2 {
		t.Fatal("total transition failure discarded ordered outcomes or severity")
	}
	for index, key := range []string{"KEY-1", "KEY-2"} {
		row := out.Results[index]
		if row.OK || row.Issue.Key != key || row.Err == nil || row.Data == nil || row.Data.Transition == nil || row.Data.Transition.ID != "31" {
			t.Fatal("failed transition lost its resolved target or fabricated success")
		}
	}
}

type emptyTransitionAcknowledgementClient struct{ *fakeTransitionClient }

func (*emptyTransitionAcknowledgementClient) DoTransition(context.Context, string, jira.TransitionRequest) (core.TransitionWrite, error) {
	return core.TransitionWrite{}, nil
}

func TestTransitionRequiresExplicitAcknowledgementBeforeComment(t *testing.T) {
	opts := transitionOpts(t, true)
	client := &emptyTransitionAcknowledgementClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransition(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	if cli.MapError(err).Code != "transition_response_invalid" || len(client.commentedKeys()) != 0 || len(out.Results) != 1 || out.Results[0].OK {
		t.Fatal("unacknowledged transition claimed success or posted a follow-up comment")
	}
}

type emptyTransitionCommentClient struct{ *fakeTransitionClient }

func (*emptyTransitionCommentClient) AddComment(context.Context, string, jira.AddCommentRequest) (core.CommentWrite, error) {
	return core.CommentWrite{}, nil
}

func TestTransitionCommentRequiresReceiptAndInspectionBeforeRetry(t *testing.T) {
	opts := transitionOpts(t, true)
	client := &emptyTransitionCommentClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransition(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	warnings := drainWarnings(t)
	if err != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Data.CommentPosted != envelope.CommentPostedFailed || out.Results[0].Data.CommentID != "" || len(warnings) != 1 || !strings.Contains(strings.ToLower(warnings[0].Message), "inspect") {
		t.Fatal("uncertain transition comment claimed delivery or invited an unchecked duplicate")
	}
}

type erroredTransitionReceiptClient struct{ *fakeTransitionClient }

func (f *erroredTransitionReceiptClient) DoTransition(ctx context.Context, key string, req jira.TransitionRequest) (core.TransitionWrite, error) {
	write, err := f.fakeTransitionClient.DoTransition(ctx, key, req)
	return write, errors.Join(err, context.Canceled)
}

func TestTransitionJoinedErrorRetainsAtomicAcknowledgement(t *testing.T) {
	opts := transitionOpts(t, true)
	client := &erroredTransitionReceiptClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": true}}}
	out, err := runTransition(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	if !errors.Is(err, context.Canceled) || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data == nil || !out.Results[0].Data.Applied || !out.Results[0].Data.Attempted || out.Results[0].Data.CommentPosted != envelope.CommentPostedAtomic || len(client.movedKeys()) != 1 || len(client.commentedKeys()) != 0 {
		t.Fatal("joined transition error discarded an acknowledged atomic request or replayed its comment")
	}
}

type erroredTransitionCommentClient struct{ *fakeTransitionClient }

func (f *erroredTransitionCommentClient) AddComment(ctx context.Context, key string, req jira.AddCommentRequest) (core.CommentWrite, error) {
	write, err := f.fakeTransitionClient.AddComment(ctx, key, req)
	return write, errors.Join(err, context.Canceled)
}

func TestTransitionJoinedCommentErrorRetainsIdentityAndInspection(t *testing.T) {
	opts := transitionOpts(t, true)
	client := &erroredTransitionCommentClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransition(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	warnings := drainWarnings(t)
	if err != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil {
		t.Fatal("joined comment error discarded the acknowledged transition")
	}
	data := out.Results[0].Data
	if data.CommentID != "c-KEY-1" || data.CommentPosted != envelope.CommentPostedFailed || data.CommentErrorCode != cli.CodeCanceled || !strings.Contains(data.CommentErrorHint, "Inspect") || strings.Contains(data.CommentErrorHint, "run it again") || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "Inspect") || len(client.movedKeys()) != 1 || len(client.commentedKeys()) != 1 {
		t.Fatal("joined comment error lost a valid identity or invited a duplicate write")
	}
}

func TestTransitionJoinedErrorKeepsHumanRecoverySafe(t *testing.T) {
	opts := transitionOpts(t, false)
	client := &erroredTransitionReceiptClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransition(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, invocation(false, "KEY-1", "Done"), opts)
	if !errors.Is(err, context.Canceled) || len(out.Results) != 1 || out.Results[0].Data == nil || !out.Results[0].Data.Applied {
		t.Fatal("joined transition error lost its native acknowledgement")
	}
	var buffer bytes.Buffer
	renderTransitionResults(output.NewView(&buffer, clog.ColorNever), out)
	text := buffer.String()
	if !strings.Contains(text, "KEY-1: moved to Done") || !strings.Contains(text, "error afterwards") || !strings.Contains(strings.ToLower(text), "inspect") || strings.Contains(text, "run it again") || !strings.Contains(strings.ToLower(cli.MapError(err).Hint), "inspect") {
		t.Fatal("acknowledged transition lost its human receipt or invited an unchecked retry")
	}
}
