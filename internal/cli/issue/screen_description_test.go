package issue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

const screenNativeDescription = `{"type":"doc","version":1,"future":{"large":9007199254740993,"spelling":"\u003c\/"},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"raw":"<>&"}}]}`

type screenDescriptionClient struct {
	unusedJiraClient
	core.IssueMetadataClient
	scope                   core.JiraScope
	current, key            string
	reads, metadata, writes int
	writeID                 string
	request                 jira.IssueWriteRequest
	writeErr                error
}

func (c *screenDescriptionClient) JiraScope() core.JiraScope { return c.scope }
func (c *screenDescriptionClient) GetIssue(_ context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	if key != "DEMO-1" && key != "101" {
		return core.IssueRead{}, errors.New("wrong immutable description target")
	}
	c.reads++
	var issue jira.IssueBean
	if err := json.Unmarshal([]byte(`{"id":"101","key":`+quotedJSON(c.key)+`,"fields":{"description":`+c.current+`}}`), &issue); err != nil {
		return core.IssueRead{}, err
	}
	return core.IssueRead{Issue: &issue}, nil
}
func quotedJSON(value string) string { encoded, _ := json.Marshal(value); return string(encoded) }
func (c *screenDescriptionClient) GetEditIssueFields(_ context.Context, id string) (core.IssueEditMetadataRead, error) {
	if id != "101" {
		return core.IssueEditMetadataRead{}, errors.New("metadata target moved")
	}
	c.metadata++
	return core.IssueEditMetadataRead{Native: json.RawMessage(`{"fields":{"description":{"key":"description","name":"Description","required":false,"schema":{"type":"any"},"operations":["set"]}}}`)}, nil
}

func (c *screenDescriptionClient) EditIssue(_ context.Context, id string, req jira.IssueWriteRequest, _ bool) (core.IssueEditWrite, error) {
	c.writes++
	c.writeID = id
	c.request = req
	return core.IssueEditWrite{}, c.writeErr
}

func newScreenDescriptionClient() *screenDescriptionClient {
	return &screenDescriptionClient{scope: core.JiraScope{Site: "https://example.atlassian.net", Account: "person@example.test", APIBaseURL: "https://example.atlassian.net/"}, current: screenNativeDescription, key: "DEMO-1"}
}

func TestScreenDescriptionPreservesNativeContentAndBindsImmutableIssue(t *testing.T) {
	c := newScreenDescriptionClient()
	session, err := ScreenDescription(t.Context(), c, "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	markdown := strings.Replace(session.Markdown(), "alpha", "edited alpha", 1)
	c.key = "MOVED-1"
	review, err := ScreenReviewDescription(t.Context(), c, session, markdown)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Changed || c.writes != 0 {
		t.Fatal("review wrote or lost the edit")
	}
	receipt, key, err := ScreenReplaceDescription(t.Context(), c, session, markdown, review)
	if err != nil || !receipt.Applied || !receipt.Attempted || key != "MOVED-1" || c.writeID != "101" || c.writes != 1 || len(c.request.Fields) != 1 {
		t.Fatalf("native replacement lost binding/receipt: %+v %q %v", receipt, key, err)
	}
	raw := string(c.request.Fields["description"])
	if !strings.Contains(raw, `"future":{"large":9007199254740993,"spelling":"\u003c\/"}`) || !strings.Contains(raw, `{"type":"futureNode","attrs":{"raw":"<>&"}}`) || !strings.Contains(raw, "edited alpha") {
		t.Fatal("description write discarded native unknown data or authored text")
	}
	c.writeErr = context.Canceled
	receipt, _, err = ScreenReplaceDescription(t.Context(), c, session, markdown, review)
	if !errors.Is(err, context.Canceled) || !receipt.Attempted || receipt.Applied || c.writes != 2 {
		t.Fatal("uncertain replacement was hidden or replayed")
	}
}

func TestScreenDescriptionRefusesStaleReviewAndForeignScopeBeforeWriting(t *testing.T) {
	c := newScreenDescriptionClient()
	session, err := ScreenDescription(t.Context(), c, "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	markdown := strings.Replace(session.Markdown(), "alpha", "edited alpha", 1)
	review, err := ScreenReviewDescription(t.Context(), c, session, markdown)
	if err != nil {
		t.Fatal(err)
	}
	c.current = strings.Replace(c.current, `"raw":"<>&"`, `"raw":"remote change"`, 1)
	receipt, _, err := ScreenReplaceDescription(t.Context(), c, session, markdown, review)
	if err == nil || receipt.Attempted || c.writes != 0 {
		t.Fatal("reviewed candidate changed without a new review")
	}
	reads, metadata := c.reads, c.metadata
	c.scope.Account = "other@example.test"
	receipt, _, err = ScreenReplaceDescription(t.Context(), c, session, markdown, review)
	if !errors.Is(err, core.ErrIssueDescriptionScopeChanged) || receipt.Attempted || c.reads != reads || c.metadata != metadata {
		t.Fatal("foreign scope reached remote work")
	}
}

func TestScreenDescriptionValidatesAnchorsBeforeRemoteWorkAndKeepsNullNoop(t *testing.T) {
	c := newScreenDescriptionClient()
	session, err := ScreenDescription(t.Context(), c, "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	before := c.reads
	_, _, err = ScreenReplaceDescription(t.Context(), c, session, "", core.IssueDescriptionMerge{})
	var edit *adf.EditError
	if !errors.As(err, &edit) || c.reads != before || c.metadata != 0 || c.writes != 0 {
		t.Fatal("removed protected content reached metadata or writing")
	}
	c.current = "null"
	session, err = ScreenDescription(t.Context(), c, "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	review, err := ScreenReviewDescription(t.Context(), c, session, "")
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := ScreenReplaceDescription(t.Context(), c, session, "", review)
	if err != nil || receipt.Attempted || c.writes != 0 || string(review.Description) != "null" {
		t.Fatal("blank save changed explicit native null")
	}
}
