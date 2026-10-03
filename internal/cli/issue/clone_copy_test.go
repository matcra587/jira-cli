package issue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func cloneNativeSource(t *testing.T, fields string) *jira.IssueBean {
	t.Helper()
	var source jira.IssueBean
	if err := json.Unmarshal([]byte(`{"id":"7001","key":"KEY-1","fields":`+fields+`}`), &source); err != nil {
		t.Fatal(err)
	}
	return &source
}

type cloneCopyClient struct {
	fakeLifecycleClient
	metadata      *core.APIResponse
	metadataError error
	metadataReads atomic.Int32
	allFieldsRead atomic.Int32
}

func (f *cloneCopyClient) GetIssue(ctx context.Context, key string, fields []string, _ ...string) (core.IssueRead, error) {
	if slices.Equal(fields, []string{"*all"}) {
		f.allFieldsRead.Add(1)
	}
	return f.fakeLifecycleClient.GetIssue(ctx, key, fields)
}

func (f *cloneCopyClient) DoAPI(_ context.Context, request core.APIRequest) (*core.APIResponse, error) {
	f.metadataReads.Add(1)
	if request.Method != http.MethodGet || request.Endpoint != "/rest/api/3/field" || request.MaxResponseBytes != core.DefaultMaxAPIResponseBytes || request.Body != nil {
		return nil, errors.New("unexpected clone field metadata request")
	}
	return f.metadata, f.metadataError
}

func TestCloneMetadataSharedAcrossFanOut(t *testing.T) {
	t.Parallel()
	fake := &cloneCopyClient{
		fakeLifecycleClient: fakeLifecycleClient{source: cloneNativeSource(t, `{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"},"customfield_10011":"0|abcdef:12","customfield_10012":"not-shaped-like-rank"}`)},
		metadata: &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{
			"id":"customfield_10011","schema":{"custom":"example:text"}
		},{"id":"customfield_10012","schema":{"custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}}]`)},
	}
	keys := []string{"KEY-1", "KEY-2", "KEY-3", "KEY-4", "KEY-5", "KEY-6", "KEY-7", "KEY-8"}
	out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, keys...), cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 4})
	if err != nil || len(out.Results) != len(keys) || len(fake.created) != len(keys) || fake.metadataReads.Load() != 1 || fake.allFieldsRead.Load() != int32(len(keys)) {
		t.Fatalf("clone lost complete sources or shared metadata: err=%v sources=%d metadata=%d writes=%d", err, fake.allFieldsRead.Load(), fake.metadataReads.Load(), len(fake.created))
	}
	for _, request := range fake.created {
		if got := string(request.Fields["customfield_10011"]); got != `"0|abcdef:12"` {
			t.Errorf("ordinary rank-shaped text lost: %s", got)
		}
		if _, copied := request.Fields["customfield_10012"]; copied {
			t.Error("native rank field copied because its value did not look like rank")
		}
	}
}

func TestCloneRefusesUnusableMetadataBeforeCreation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *core.APIResponse
	}{
		{"nil receipt", nil},
		{"wrong success status", &core.APIResponse{StatusCode: http.StatusCreated, Body: []byte(`[{"id":"customfield_10011","schema":{"custom":"example:text"}}]`)}},
		{"truncated", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011","schema":{"custom":"example:text"}}]`), Truncated: true}},
		{"object", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`{}`)}},
		{"null", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`null`)}},
		{"trailing document", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[] {}`)}},
		{"null definition", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[null]`)}},
		{"blank identity", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":" "}]`)}},
		{"duplicate identity", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011","schema":{"custom":"example:text"}},{"id":"customfield_10011","schema":{"custom":"example:text"}}]`)}},
		{"missing source field", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[]`)}},
		{"missing schema", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011"}]`)}},
		{"missing custom type", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011","schema":{"type":"string"}}]`)}},
		{"blank custom type", &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011","schema":{"custom":" \t"}}]`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &cloneCopyClient{
				fakeLifecycleClient: fakeLifecycleClient{source: cloneNativeSource(t, `{"summary":"Source","customfield_10011":null}`)},
				metadata:            tc.response,
			}
			out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1", "KEY-2"), cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 2})
			if err == nil || cli.MapError(err).Code != cli.CodeIssueCloneSourceInvalid || len(out.Results) != 2 || len(fake.created) != 0 || fake.metadataReads.Load() != 1 {
				t.Fatalf("unusable metadata did not stop creation: err=%v metadata=%d writes=%d results=%+v", err, fake.metadataReads.Load(), len(fake.created), out.Results)
			}
			for i, row := range out.Results {
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeIssueCloneSourceInvalid || row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] {
					t.Fatal("metadata failure lost ordered source outcomes or invented a creation")
				}
			}
		})
	}
}

func TestCloneMetadataPreservesOriginalReadCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("native metadata read failed")
	joined := errors.Join(cause, context.DeadlineExceeded)
	fake := &cloneCopyClient{
		fakeLifecycleClient: fakeLifecycleClient{source: cloneNativeSource(t, `{"summary":"Source","customfield_10011":false}`)},
		metadata:            &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(`[{"id":"customfield_10011","schema":{"custom":"example:text"}}]`)},
		metadataError:       joined,
	}
	out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1", "KEY-2"), cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 2})
	if !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) || len(out.Results) != 2 || len(fake.created) != 0 || fake.metadataReads.Load() != 1 {
		t.Fatalf("joined metadata failure lost its cause or performed a write: err=%v writes=%d", err, len(fake.created))
	}
	for _, row := range out.Results {
		if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "timeout" {
			t.Fatal("joined metadata deadline claimed a clone or lost its classification")
		}
	}
}

func TestCloneRefusesAbsentOrNullSourceFields(t *testing.T) {
	t.Parallel()
	for _, fields := range []jira.Nullable[jira.IssueFields]{{}, jira.NullableNull[jira.IssueFields]()} {
		fake := &fakeLifecycleClient{source: &jira.IssueBean{Fields: fields}}
		out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), cloneOptions{fields: &command.IssueFieldFlags{}, parallelism: 1})
		if err == nil || cli.MapError(err).Code != cli.CodeIssueCloneSourceInvalid || len(fake.created) != 0 || len(out.Results) != 1 || out.Results[0].OK {
			t.Fatalf("absent/null source fields reached creation: err=%v writes=%d", err, len(fake.created))
		}
	}
}

func TestCloneCopiesAssigneeAndOpaqueNativeFields(t *testing.T) {
	for _, tc := range []struct {
		name, value string
	}{
		{"opaque object", `{"unknownType":{"nested":[null,false,0,"native <&>\\u2028"]}}`},
		{"array", `[{"id":"option-1","unknown":{"keep":true}}]`},
		{"false", `false`},
		{"zero", `0`},
		{"null", `null`},
		{"escaped line separator", `"native <&>\u2028"`},
		{"literal line separator", "\"native <&>\u2028\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := cloneNativeSource(t, `{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"},"assignee":{"accountId":"source-person","displayName":"Read-only display"},"customfield_10011":`+tc.value+`,"futureNativeField":`+tc.value+`}`)
			req, _, err := cloneRequest(source, cloneOptions{fields: &command.IssueFieldFlags{}}, jira.IssueWriteRequest{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(req.Fields["assignee"]); got != `{"accountId":"source-person"}` {
				t.Errorf("assignee = %s; the source account must carry without read-only user attributes", got)
			}
			for _, name := range []string{"customfield_10011", "futureNativeField"} {
				if got := string(req.Fields[name]); got != tc.value {
					t.Errorf("%s = %s, want native bytes %s", name, got, tc.value)
				}
			}
		})
	}
}

func TestCloneKeepsExplicitlyUnassignedSource(t *testing.T) {
	source := cloneNativeSource(t, `{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"},"assignee":null}`)
	req, _, err := cloneRequest(source, cloneOptions{fields: &command.IssueFieldFlags{}}, jira.IssueWriteRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(req.Fields["assignee"]); got != "null" {
		t.Fatalf("unassigned source = %s, want explicit null rather than the target default", got)
	}
}

func TestCloneMinimizesCopiedNativeReferences(t *testing.T) {
	source := cloneNativeSource(t, `{"summary":"Source","project":{"id":"7002","key":"KEY","name":"Read-only project","self":"https://example.test/project"},"issuetype":{"id":"7003","name":"Task","description":"Read-only type","self":"https://example.test/type"},"priority":{"id":"7004","name":"High","iconUrl":"https://example.test/icon","self":"https://example.test/priority"},"assignee":{"accountId":"source-person","displayName":"Read-only display","emailAddress":"person@example.test","active":true,"self":"https://example.test/person"}}`)
	req, _, err := cloneRequest(source, cloneOptions{fields: &command.IssueFieldFlags{}}, jira.IssueWriteRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"project":   `{"key":"KEY"}`,
		"issuetype": `{"name":"Task"}`,
		"priority":  `{"name":"High"}`,
		"assignee":  `{"accountId":"source-person"}`,
	} {
		if got := string(req.Fields[name]); got != want {
			t.Errorf("copied %s = %s, want minimal native reference %s", name, got, want)
		}
	}
}

func TestCloneExcludesServerFieldsAndNativeRank(t *testing.T) {
	fields := map[string]any{
		"summary": "Source", "project": map[string]string{"key": "KEY"}, "issuetype": map[string]string{"name": "Task"},
		"customfield_10011": "0|abcdef:12", "customfield_10012": "ordinary custom value",
	}
	for _, name := range []string{
		"id", "key", "self", "created", "updated", "creator", "reporter", "status", "resolution", "resolutiondate",
		"statusCategory", "statuscategorychangedate", "lastViewed", "issuerestriction", "timeestimate", "timespent",
		"timeoriginalestimate", "workratio", "progress", "timetracking", "rankBeforeIssue", "rankAfterIssue",
		"comment", "worklog", "subtasks", "attachment", "votes", "watches", "issuelinks", "worklogs", "aggregatetimeestimate",
	} {
		fields[name] = "server-owned value"
	}
	fields["status"] = map[string]string{"name": "Done"}
	fields["resolution"] = map[string]string{"name": "Fixed"}
	fields["reporter"] = map[string]string{"accountId": "reporter-person"}
	fields["attachment"] = []any{}
	fields["issuelinks"] = []any{}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	// Parse arbitrary server fields through the raw-capture model, rather than
	// inventing a typed response that could hide the same loss as production.
	var source jira.IssueBean
	source.Fields = jira.NullableOf(jira.IssueFields{Extra: map[string]json.RawMessage{}})
	if err := json.Unmarshal(encoded, &source.Fields.Value); err != nil {
		t.Fatal(err)
	}
	req, _, err := cloneRequest(&source, cloneOptions{fields: &command.IssueFieldFlags{}}, jira.IssueWriteRequest{}, map[string]bool{"customfield_10011": true})
	if err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		switch name {
		case "summary", "project", "issuetype", "customfield_10012":
			if _, present := req.Fields[name]; !present {
				t.Errorf("eligible %s disappeared", name)
			}
		default:
			if _, present := req.Fields[name]; present {
				t.Errorf("server-owned or rank field %s copied", name)
			}
		}
	}
}

func TestCloneCopyOverridesPreserveOtherNativeValues(t *testing.T) {
	source := cloneNativeSource(t, `{"summary":"Source","project":{"key":"KEY"},"issuetype":{"name":"Task"},"assignee":{"accountId":"source-person"},"customfield_10011":{"keep":true},"customfield_10012":0}`)
	overrides := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_10011": json.RawMessage(`{"replacement":false}`)}}
	req, _, err := cloneRequest(source, cloneOptions{fields: &command.IssueFieldFlags{Assignee: "override-person"}}, overrides, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(req.Fields["assignee"]) != `{"accountId":"override-person"}` || string(req.Fields["customfield_10011"]) != `{"replacement":false}` || string(req.Fields["customfield_10012"]) != "0" {
		t.Fatalf("overrides erased unrelated source values or lost precedence: %+v", req.Fields)
	}
	if got := string(source.Fields.Value.Extra["customfield_10011"]); got != `{"keep":true}` {
		t.Fatalf("copy mutated its source: %s", got)
	}
}
