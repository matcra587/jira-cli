package issue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type screenActionClient struct {
	unusedJiraClient
	required, invalidComment bool
	transitionErr            error
	reads, writes            int
	account                  string
	request                  jira.IssueWriteRequest
}

func (c *screenActionClient) GetTransitions(context.Context, string) (core.TransitionRead, error) {
	c.reads++
	transition := jira.IssueTransition{ID: jira.NullableOf("31"), Name: jira.NullableOf("Finish"), HasScreen: jira.NullableOf(c.required)}
	if c.required {
		transition.Fields = jira.NullableOf(map[string]json.RawMessage{"resolution": json.RawMessage(`{"required":true}`)})
	}
	return core.TransitionRead{Transitions: &jira.Transitions{Transitions: jira.NullableOf([]jira.IssueTransition{transition})}}, nil
}

func (c *screenActionClient) DoTransition(context.Context, string, jira.TransitionRequest) (core.TransitionWrite, error) {
	c.writes++
	return core.TransitionWrite{StatusCode: http.StatusNoContent}, c.transitionErr
}

func (c *screenActionClient) GetUser(_ context.Context, id string) (core.UserRead, error) {
	c.reads++
	return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf(c.account), Active: jira.NullableOf(true)}}, nil
}

func (c *screenActionClient) EditIssue(_ context.Context, _ string, request jira.IssueWriteRequest, _ bool) (core.IssueEditWrite, error) {
	c.writes++
	c.request = request
	return core.IssueEditWrite{}, nil
}

func (c *screenActionClient) AddComment(context.Context, string, jira.AddCommentRequest) (core.CommentWrite, error) {
	c.writes++
	if c.invalidComment {
		return core.CommentWrite{}, nil
	}
	return core.CommentWrite{Comment: &jira.Comment{ID: jira.NullableOf("comment-7")}}, nil
}

func TestScreenTransitionAlwaysRechecksWorkflowAndKeepsAcknowledgement(t *testing.T) {
	c := &screenActionClient{required: true}
	receipt, err := ScreenTransition(t.Context(), c, "DEMO-1", "31")
	if err == nil || c.reads != 1 || c.writes != 0 || receipt.Attempted {
		t.Fatal("numeric screen target bypassed current required fields")
	}
	c.required = false
	c.transitionErr = context.Canceled
	receipt, err = ScreenTransition(t.Context(), c, "DEMO-1", "31")
	if !errors.Is(err, context.Canceled) || !receipt.Attempted || !receipt.Applied || c.writes != 1 {
		t.Fatal("joined acknowledgement was lost after interruption")
	}
}

func TestScreenAssignRequiresExactActiveIdentityAndUsesNativeNull(t *testing.T) {
	c := &screenActionClient{account: "different-account"}
	if receipt, err := ScreenAssign(t.Context(), c, "DEMO-1", "account-7"); err == nil || receipt.Attempted || c.writes != 0 {
		t.Fatal("mismatched account identity authorized assignment")
	}
	c.account = "account-7"
	receipt, err := ScreenAssign(t.Context(), c, "DEMO-1", "account-7")
	if err != nil || !receipt.Applied || string(c.request.Fields["assignee"]) != `{"accountId":"account-7"}` {
		t.Fatalf("assignment lost exact native identity: %v %s", err, c.request.Fields["assignee"])
	}
	receipt, err = ScreenAssign(t.Context(), c, "DEMO-1", "")
	if err != nil || !receipt.Applied || string(c.request.Fields["assignee"]) != "null" {
		t.Fatal("unassignment did not send native null")
	}
}

func TestScreenCommentRequiresValidReceiptAndDoesNotRetry(t *testing.T) {
	c := &screenActionClient{invalidComment: true}
	receipt, err := ScreenComment(t.Context(), c, "DEMO-1", "A new comment")
	if err == nil || !receipt.Attempted || receipt.Applied || c.writes != 1 {
		t.Fatal("unusable comment acknowledgement was accepted or retried")
	}
	c.invalidComment = false
	receipt, err = ScreenComment(t.Context(), c, "DEMO-1", "A new comment")
	if err != nil || !receipt.Applied || receipt.CommentID != "comment-7" || c.writes != 2 {
		t.Fatal("created comment identity was lost")
	}
}

func TestCanceledScreenActionsNeverEnterMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := &screenActionClient{}
	for _, run := range []func() (core.IssueActionReceipt, error){
		func() (core.IssueActionReceipt, error) { return ScreenAssign(ctx, c, "DEMO-1", "") },
		func() (core.IssueActionReceipt, error) { return ScreenTransition(ctx, c, "DEMO-1", "31") },
		func() (core.IssueActionReceipt, error) { return ScreenComment(ctx, c, "DEMO-1", "Draft") },
	} {
		receipt, err := run()
		if !errors.Is(err, context.Canceled) || receipt.Attempted {
			t.Fatal("canceled action entered mutation")
		}
	}
	if c.reads != 0 || c.writes != 0 {
		t.Fatal("canceled action contacted Jira")
	}
}
