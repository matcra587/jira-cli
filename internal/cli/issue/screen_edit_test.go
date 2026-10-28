package issue

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type screenEditClient struct {
	*screenActionClient
	core.IssueMetadataClient
	issue                       *jira.IssueBean
	metadata                    json.RawMessage
	metadataTarget, writeTarget string
	writeErr                    error
}

func (c *screenEditClient) GetIssue(_ context.Context, key string, fields []string, _ ...string) (core.IssueRead, error) {
	if key != "DEMO-1" || !slices.Equal(fields, []string{"summary", "labels"}) {
		return core.IssueRead{}, errors.New("wrong editable read")
	}
	return core.IssueRead{Issue: c.issue}, nil
}

func (c *screenEditClient) GetEditIssueFields(_ context.Context, id string) (core.IssueEditMetadataRead, error) {
	c.metadataTarget = id
	return core.IssueEditMetadataRead{Native: c.metadata}, nil
}

func (c *screenEditClient) EditIssue(_ context.Context, id string, req jira.IssueWriteRequest, _ bool) (core.IssueEditWrite, error) {
	c.writeTarget = id
	c.writes++
	c.request = req
	return core.IssueEditWrite{}, c.writeErr
}

func TestScreenFieldEditsUseLiveMetadataLiteralValuesAndOneWrite(t *testing.T) {
	c := &screenEditClient{screenActionClient: &screenActionClient{}, metadata: json.RawMessage(`{"fields":{"summary":{"key":"summary","name":"Summary","required":true,"schema":{"type":"string"},"operations":["set"]},"labels":{"key":"labels","name":"Labels","required":false,"schema":{"type":"array","items":"string"},"operations":["set"]}}}`)}
	for _, tc := range []struct{ field, value, want string }{{"summary", "Literal, summary", "\"Literal, summary\""}, {"labels", `["literal,comma","quote\"and,comma"]`, `["literal,comma","quote\"and,comma"]`}, {"labels", "[]", "[]"}} {
		receipt, err := ScreenEditField(t.Context(), c, "101", tc.field, tc.value)
		if err != nil || !receipt.Attempted || !receipt.Applied || c.writeTarget != "101" || c.metadataTarget != "101" || string(c.request.Fields[tc.field]) != tc.want || len(c.request.Fields) != 1 {
			t.Fatalf("field edit lost identity/value/receipt: %+v %v %s", receipt, err, c.request.Fields[tc.field])
		}
	}
	if c.writes != 3 {
		t.Fatal("field writes replayed")
	}
	c.metadata = json.RawMessage(`{"fields":{"summary":{"key":"summary","name":"Summary","required":true,"schema":{"type":"string"},"operations":["add"]}}}`)
	receipt, err := ScreenEditField(t.Context(), c, "101", "summary", "new")
	if err == nil || receipt.Attempted || c.writes != 3 {
		t.Fatal("unavailable field operation reached edit writer")
	}
	c.metadata = json.RawMessage(`{"fields":{"summary":{"key":"summary","name":"Summary","required":true,"schema":{"type":"string"},"operations":["set"]}}}`)
	c.writeErr = context.Canceled
	receipt, err = ScreenEditField(t.Context(), c, "101", "summary", "new")
	if !errors.Is(err, context.Canceled) || !receipt.Attempted || receipt.Applied || c.writes != 4 {
		t.Fatal("uncertain edit was hidden or retried")
	}
	for _, value := range []string{"null", "{}", "[1]"} {
		receipt, err = ScreenEditField(t.Context(), c, "101", "labels", value)
		if err == nil || receipt.Attempted || c.writes != 4 {
			t.Fatal("non-array label input entered writer")
		}
	}
}

func TestScreenEditableRejectsIncompleteOrDifferentNativeIdentity(t *testing.T) {
	c := &screenEditClient{screenActionClient: &screenActionClient{}}
	for _, source := range []string{`null`, `{"key":"DEMO-1","fields":{}}`, `{"id":"101","key":"DEMO-2","fields":{}}`, `{"id":"101","key":"DEMO-1","fields":null}`} {
		if source == "null" {
			c.issue = nil
		} else {
			var issue jira.IssueBean
			if err := json.Unmarshal([]byte(source), &issue); err != nil {
				t.Fatal(err)
			}
			c.issue = &issue
		}
		_, err := ScreenEditable(t.Context(), c, "DEMO-1")
		if err == nil || cli.MapError(err).Code != cli.CodeIssueEditSourceInvalid {
			t.Fatal("invalid edit source was admitted or misclassified")
		}
	}
	var issue jira.IssueBean
	if err := json.Unmarshal([]byte(`{"id":"101","key":"DEMO-1","fields":{"summary":"Current","labels":null,"future":{"n":9007199254740993}}}`), &issue); err != nil {
		t.Fatal(err)
	}
	c.issue = &issue
	got, err := ScreenEditable(t.Context(), c, "DEMO-1")
	if err != nil || got != c.issue || !got.Fields.Value.Labels.Null {
		t.Fatal("valid native edit source was normalized")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ScreenEditable(ctx, c, "DEMO-1"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled editor contacted Jira")
	}
}
