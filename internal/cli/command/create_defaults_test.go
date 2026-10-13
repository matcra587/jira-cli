package command

import (
	"bytes"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestCreateDefaultsPreserveNativeFieldAndOperationOwnership(t *testing.T) {
	t.Parallel()
	for _, native := range []string{
		`{"fields":{"summary":"Draft","project":null,"issuetype":{}}}`,
		`{"fields":{"summary":"Draft","project":{"id":"10401","future":"\u003c&>"},"issuetype":{"id":"10044"}},"future":{"retained":true}}`,
		`{"fields":{"summary":"Draft"},"update":{"project":[],"issuetype":[{"set":null}]}}`,
		`{"fields":{"project":"","issuetype":0,"description":{"type":"doc","version":1,"content":[],"future":"\u2028"}}}`,
	} {
		t.Run(native, func(t *testing.T) {
			t.Parallel()
			var request jira.IssueWriteRequest
			if err := request.UnmarshalJSON([]byte(native)); err != nil {
				t.Fatal(err)
			}
			before, err := request.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyCreateDefaults(&request, core.IssuePreferences{DefaultProject: "DEFAULT", DefaultIssueType: "Task"}, false, false); err != nil {
				t.Fatal(err)
			}
			after, err := request.MarshalJSON()
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("defaults rewrote authored native context", err)
			}
		})
	}
}

func TestCreateDefaultsFillOnlyMissingAndHonorExplicitClears(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                              string
		projectSuppressed, typeSuppressed bool
		wantProject, wantType             bool
	}{
		{name: "both absent", wantProject: true, wantType: true},
		{name: "project cleared", projectSuppressed: true, wantType: true},
		{name: "type cleared", typeSuppressed: true, wantProject: true},
		{name: "both cleared", projectSuppressed: true, typeSuppressed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var request jira.IssueWriteRequest
			if err := ApplyCreateDefaults(&request, core.IssuePreferences{DefaultProject: " DEFAULT ", DefaultIssueType: " Task "}, test.projectSuppressed, test.typeSuppressed); err != nil {
				t.Fatal(err)
			}
			if _, present := request.Fields["project"]; present != test.wantProject {
				t.Fatal("project clear revived configured context")
			}
			if _, present := request.Fields["issuetype"]; present != test.wantType {
				t.Fatal("type clear revived configured context")
			}
		})
	}
}
