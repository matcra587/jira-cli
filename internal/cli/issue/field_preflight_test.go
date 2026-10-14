package issue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Only the existing account-resolution tests opt into this create metadata
// fixture. Unrelated write fakes keep failing on unexpected metadata reads.
type createPreflightFixtureClient struct {
	*fakeWriteClient
}

func writePreflightFixture(fake *fakeWriteClient) *createPreflightFixtureClient {
	return &createPreflightFixtureClient{fakeWriteClient: fake}
}

func (*createPreflightFixtureClient) GetCreateIssueProject(context.Context, string) (core.IssueCreateProjectRead, error) {
	return core.IssueCreateProjectRead{Native: json.RawMessage(`{"id":"10402","key":"ENG"}`)}, nil
}

func (*createPreflightFixtureClient) GetCreateIssueTypes(context.Context, string, int, int) (core.IssueMetadataRead, error) {
	return inputPage("issueTypes", 0, 1, `{"id":"10002","name":"Task"}`), nil
}

func (*createPreflightFixtureClient) GetCreateIssueFields(context.Context, string, string, int, int) (core.IssueMetadataRead, error) {
	return inputPage("fields", 0, 2,
		`{"fieldId":"summary","key":"summary","name":"Summary","schema":{"type":"string"},"required":true,"operations":["set"]}`,
		`{"fieldId":"assignee","key":"assignee","name":"Assignee","schema":{"type":"user"},"required":false,"operations":["set"]}`), nil
}

func (*createPreflightFixtureClient) GetEditIssueFields(context.Context, string) (core.IssueEditMetadataRead, error) {
	return core.IssueEditMetadataRead{}, unused("GetEditIssueFields")
}

func TestIssueFieldPreflightCreateContextKeepsIdentityAndRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field, alternative, value, update, want string
	}{
		{"project key", "project", "key", `{"key":"KEY","future":9007199254740993}`, "", "KEY"},
		{"project ID", "project", "key", `{"id":"10402"}`, "", "10402"},
		{"type name", "issuetype", "name", `{"name":"10402"}`, "", "10402"},
		{"last set", "project", "key", "", `[{"set":{"key":"OLD"}},{"set":{"key":"KEY"}}]`, "KEY"},
		{"ambiguous containers", "project", "key", `{"key":"KEY"}`, `[{"set":{"key":"OTHER"}}]`, ""},
		{"scalar context", "project", "key", `"KEY"`, "", ""},
		{"null identity", "project", "key", `{"id":null,"key":"KEY"}`, "", ""},
		{"blank identity", "issuetype", "name", `{"name":" "}`, "", ""},
		{"unknown spelling", "project", "key", `{"Key":"KEY"}`, "", ""},
		{"unsupported operation", "project", "key", "", `[{"add":{"key":"KEY"}}]`, ""},
		{"duplicate operation", "project", "key", "", `[{"set":{"key":"OLD"},"set":{"key":"KEY"}}]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{}, Update: map[string]json.RawMessage{}}
			if tc.value != "" {
				request.Fields[tc.field] = json.RawMessage(tc.value)
			}
			if tc.update != "" {
				request.Update[tc.field] = json.RawMessage(tc.update)
			}
			got, _, err := preflightCreateContext(request, tc.field, tc.alternative)
			if tc.want != "" {
				if err != nil || got != tc.want {
					t.Fatalf("native identity changed: got=%q error=%v", got, err)
				}
			} else {
				var refusal *core.IssueFieldPreflightError
				if !errors.As(err, &refusal) || refusal.Field != tc.field {
					t.Fatalf("ambiguous native context accepted: got=%q error=%v", got, err)
				}
			}
		})
	}
}

func TestIssueFieldPreflightCanceledReadNeverAdmitsWrite(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	client := &inputMetadataClient{edit: func(context.Context, string) (core.IssueEditMetadataRead, error) {
		cancel()
		return core.IssueEditMetadataRead{Native: inputEditDocument(`"customfield_14002":` + inputNativeField)}, nil
	}}
	request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_14002": json.RawMessage(`null`)}}
	if result, err := editFieldPreflight(ctx, client, "KEY-1", request); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled metadata supplied a receipt: result=%+v error=%v", result, err)
	}
}

func TestIssueFieldPreflightCreateRejectsConflictingResolvedProject(t *testing.T) {
	t.Parallel()
	for _, project := range []string{`{"id":"99999","key":"ENG"}`, `{"id":"10402","key":"OTHER"}`} {
		t.Run(project, func(t *testing.T) {
			t.Parallel()
			client := writePreflightFixture(&fakeWriteClient{})
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"project": json.RawMessage(project), "issuetype": json.RawMessage(`{"name":"Task"}`), "summary": json.RawMessage(`"Fixture"`)}}
			result, err := createFieldPreflight(t.Context(), client, request)
			var refusal *core.IssueFieldPreflightError
			if result != nil || !errors.As(err, &refusal) || refusal.Field != "project" {
				t.Fatalf("conflicting project established context: %+v %v", result, err)
			}
		})
	}
}
