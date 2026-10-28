package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type screenCreateFixture struct {
	*createPreflightFixtureClient
	denied   bool
	status   int
	identity *jira.CreatedIssue
	cause    error
}

func (c *screenCreateFixture) GetCreateIssueFields(context.Context, string, string, int, int) (core.IssueMetadataRead, error) {
	fields := []string{}
	for _, name := range []string{"summary", "description", "labels", "assignee"} {
		operation := "set"
		if c.denied && name == "summary" {
			operation = "add"
		}
		fields = append(fields, fmt.Sprintf(`{"fieldId":%q,"key":%q,"name":%q,"schema":{"type":"string"},"required":false,"operations":[%q]}`, name, name, name, operation))
	}
	return inputPage("fields", 0, len(fields), fields...), nil
}

func (c *screenCreateFixture) CreateIssue(ctx context.Context, req jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	if _, err := c.fakeWriteClient.CreateIssue(ctx, req); err != nil {
		return core.IssueCreateWrite{}, err
	}
	return core.IssueCreateWrite{StatusCode: c.status, Issue: c.identity}, c.cause
}

func TestScreenCreationSharesNativeCompositionAndReceiptPolicy(t *testing.T) {
	fake := &fakeWriteClient{}
	c := &screenCreateFixture{createPreflightFixtureClient: writePreflightFixture(fake), status: 201, identity: &jira.CreatedIssue{ID: "9001", Key: "NEW-1"}}
	authored := core.IssueCreationInput{ProjectID: "10402", IssueTypeID: "10002", Summary: "New literal summary", Markdown: "New **native** body", Labels: jira.NullableOf([]string{"literal,comma", `quote"label`}), Assignee: jira.NullableNull[string]()}
	request, err := ScreenPrepareCreate(t.Context(), c, authored)
	if err != nil || len(fake.created) != 0 {
		t.Fatalf("preview submitted creation or refused valid metadata: %v", err)
	}
	authored.Labels.Value[0] = "mutated later"
	if string(request.Fields["labels"]) != `["literal,comma","quote\"label"]` || string(request.Fields["assignee"]) != "null" || !strings.Contains(string(request.Fields["description"]), `"type":"doc"`) {
		t.Fatalf("new native payload lost explicit values or ADF: %s %s %s", request.Fields["labels"], request.Fields["assignee"], request.Fields["description"])
	}
	result, err := ScreenCreate(t.Context(), c, request)
	if err != nil || !result.Attempted || !result.Applied || result.Issue == nil || result.Issue.ID != "9001" || len(fake.created) != 1 {
		t.Fatalf("screen lost native creation receipt: %+v %v", result, err)
	}
	c.identity = nil
	result, err = ScreenCreate(t.Context(), c, request)
	if !errors.Is(err, core.ErrIssueCreateResponseInvalid) || !result.Applied || !result.Attempted || result.Issue != nil || len(fake.created) != 2 {
		t.Fatal("malformed201 lost acknowledgement or triggered replay")
	}
	c.status = 0
	c.cause = context.Canceled
	result, err = ScreenCreate(t.Context(), c, request)
	if !errors.Is(err, context.Canceled) || !result.Attempted || result.Applied || len(fake.created) != 3 {
		t.Fatal("uncertain creation was hidden or repeated")
	}
	c.denied = true
	result, err = ScreenCreate(t.Context(), c, request)
	if err == nil || result.Attempted || len(fake.created) != 3 {
		t.Fatal("stale unavailable field metadata reached create writer")
	}
}

func TestScreenCreationKeepsAbsentDefaultsAndRefusesCanceledOrBlankInput(t *testing.T) {
	fake := &fakeWriteClient{}
	c := &screenCreateFixture{createPreflightFixtureClient: writePreflightFixture(fake)}
	input := core.IssueCreationInput{ProjectID: "10402", IssueTypeID: "10002", Summary: "New issue"}
	req, err := ScreenPrepareCreate(t.Context(), c, input)
	if err != nil || len(req.Fields) != 3 {
		t.Fatalf("absent optional fields became explicit values: %v %v", req.Keys(), err)
	}
	input.Labels = jira.NullableOf([]string{})
	req, err = ScreenPrepareCreate(t.Context(), c, input)
	if err != nil || string(req.Fields["labels"]) != "[]" {
		t.Fatal("explicit empty labels were lost")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ScreenPrepareCreate(ctx, c, input); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preview entered metadata work")
	}
	input.Summary = " "
	if _, err := ScreenPrepareCreate(t.Context(), c, input); err == nil {
		t.Fatal("blank summary reached creation")
	}
	if len(fake.created) != 0 {
		t.Fatal("local failures wrote Jira")
	}
}

func TestScreenCreationRefusesLossyMarkdownBeforeSubmission(t *testing.T) {
	fake := &fakeWriteClient{}
	client := &screenCreateFixture{createPreflightFixtureClient: writePreflightFixture(fake)}
	_, err := ScreenPrepareCreate(t.Context(), client, core.IssueCreationInput{ProjectID: "10402", IssueTypeID: "10002", Summary: "New issue", Markdown: "before <span>unsupported</span> after"})
	if err == nil || len(fake.created) != 0 {
		t.Fatal("lossy description was accepted or submitted")
	}
}
