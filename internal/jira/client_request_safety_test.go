package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRESTPathEscapesDynamicSegments(t *testing.T) {
	got := RESTPath("issue", "PROJ-1/../../evil space")
	want := "rest/api/3/issue/PROJ-1%2F..%2F..%2Fevil%20space"
	if got != want {
		t.Fatalf("RESTPath() = %q, want %q", got, want)
	}

	if got := RESTPath("issue", ".."); got != "rest/api/3/issue/%2E%2E" {
		t.Fatalf("RESTPath(dot segment) = %q", got)
	}
}

func TestIssueServiceGetEscapesIssueKeyPathSegment(t *testing.T) {
	var requestURI string
	client := newHTTPHandlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"PROJ-1"}`))
	}))

	service := NewIssueService(client)
	if _, _, err := service.Get(context.Background(), "PROJ-1/../../evil space", nil); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	want := "/rest/api/3/issue/PROJ-1%2F..%2F..%2Fevil%20space"
	if requestURI != want {
		t.Fatalf("request URI = %q, want %q", requestURI, want)
	}
}

func TestIssueServiceGetEncodesFieldsAndExpandQuery(t *testing.T) {
	var requestURI string
	client := newHTTPHandlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"PROJ-1"}`))
	}))

	service := NewIssueService(client)
	opts := &IssueGetOptions{Fields: []string{"summary", "status"}, Expand: []string{"renderedFields"}}
	if _, _, err := service.Get(context.Background(), "PROJ-1", opts); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	want := "/rest/api/3/issue/PROJ-1?expand=renderedFields&fields=summary%2Cstatus"
	if requestURI != want {
		t.Fatalf("request URI = %q, want %q", requestURI, want)
	}
}

func TestIssueCreatePayloadDefensivelyCopiesFields(t *testing.T) {
	nested := map[string]any{"value": "before"}
	labels := []any{"one"}
	req := &IssueCreateRequest{
		Summary: "summary",
		Fields: map[string]any{
			"customfield_1": nested,
			"labels":        labels,
		},
	}

	payload := req.payload()
	nested["value"] = "after"
	labels[0] = "two"

	fields := payload["fields"].(map[string]any)
	gotNested := fields["customfield_1"].(map[string]any)
	if gotNested["value"] != "before" {
		t.Fatalf("nested field was aliased: %+v", gotNested)
	}
	gotLabels := fields["labels"].([]any)
	if gotLabels[0] != "one" {
		t.Fatalf("slice field was aliased: %+v", gotLabels)
	}
}

func TestMutationPayloadsDefensivelyCopyFields(t *testing.T) {
	updateNested := map[string]any{"value": "before"}
	typedMaps := []map[string]any{{"child": map[string]any{"name": "before"}}}
	update := (&IssueUpdateRequest{Fields: map[string]any{
		"customfield_1": updateNested,
		"customfield_2": typedMaps,
	}}).payload()
	updateNested["value"] = "after"
	typedMaps[0]["child"].(map[string]any)["name"] = "after"
	if got := update["fields"].(map[string]any)["customfield_1"].(map[string]any)["value"]; got != "before" {
		t.Fatalf("update payload nested value = %v, want before", got)
	}
	if got := update["fields"].(map[string]any)["customfield_2"].([]map[string]any)[0]["child"].(map[string]any)["name"]; got != "before" {
		t.Fatalf("update payload typed nested value = %v, want before", got)
	}

	moveLabels := []any{"one"}
	move := (&IssueMoveRequest{Fields: map[string]any{"labels": moveLabels}}).payload()
	moveLabels[0] = "two"
	if got := move["fields"].(map[string]any)["labels"].([]any)[0]; got != "one" {
		t.Fatalf("move payload label = %v, want one", got)
	}

	source := map[string]any{"summary": "copy", "customfield_1": map[string]any{"value": "source"}}
	override := map[string]any{"labels": []any{"override"}}
	cloned := fieldsToClone(source, override)
	source["customfield_1"].(map[string]any)["value"] = "changed"
	override["labels"].([]any)[0] = "changed"
	if got := cloned["customfield_1"].(map[string]any)["value"]; got != "source" {
		t.Fatalf("cloned source field = %v, want source", got)
	}
	if got := cloned["labels"].([]any)[0]; got != "override" {
		t.Fatalf("cloned override field = %v, want override", got)
	}
}

func TestIssueUnmarshalPromotesCopiedSliceContainers(t *testing.T) {
	var issue Issue
	raw := []byte(`{
		"fields": {
			"comment": {"comments": [{"id": "c1"}]},
			"worklog": {"worklogs": [{"id": "w1"}]},
			"subtasks": [{"key": "SUB-1"}]
		}
	}`)
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	issue.Comments[0] = &Comment{ID: new("changed")}
	issue.Worklogs[0] = &Worklog{ID: new("changed")}
	issue.Subtasks[0] = &Issue{Key: new("changed")}

	if got := *issue.Fields.Comment.Comments[0].ID; got != "c1" {
		t.Fatalf("nested comment id = %q, want c1", got)
	}
	if got := *issue.Fields.Worklog.Worklogs[0].ID; got != "w1" {
		t.Fatalf("nested worklog id = %q, want w1", got)
	}
	if got := *issue.Fields.Subtasks[0].Key; got != "SUB-1" {
		t.Fatalf("nested subtask key = %q, want SUB-1", got)
	}
}

func TestIssueListDoesNotReadExportedMutableDefaultFields(t *testing.T) {
	original := append([]string(nil), IssueListFields...)
	IssueListFields = []string{"evil"}
	defer func() { IssueListFields = original }()

	var fields []string
	client := newHTTPHandlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Fields []string `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		fields = payload.Fields
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issues":[],"isLast":true}`))
	}))

	service := NewIssueService(client)
	if _, _, err := service.List(context.Background(), &IssueListOptions{JQL: "project=JCT"}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if strings.Join(fields, ",") != strings.Join(defaultIssueListFields, ",") {
		t.Fatalf("search fields = %v, want defaults %v", fields, defaultIssueListFields)
	}
}
