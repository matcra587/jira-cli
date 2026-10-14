//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueInputPreparationFlagsControlAuthoring(t *testing.T) {
	t.Parallel()
	const definition = `{"key":"customfield_1","name":"Choice","required":false,"operations":["set"],"schema":{"type":"option","custom":"select"}}`
	options := mx4Opts{Files: map[string]string{
		"first.json":   `{"fields":{"customfield_1":"first"}}`,
		"second.json":  `{"fields":{"customfield_1":"second"}}`,
		"known.json":   `{"fields":{"customfield_1":` + definition + `}}`,
		"unknown.json": `{"fields":{"customfield_1":{"key":"customfield_1","name":"Choice","required":false,"operations":["set"],"schema":{"type":"option","custom":"fixture.vendor:select"}}}}`,
	}}
	for _, file := range []string{"first.json", "second.json"} {
		result := mx4Run(t, options, "issue", "input", "prepare", "--input", file, "--metadata", "known.json", "--output=json")
		var out envelope.IssueInputPreparationOutput
		mx4Data(t, mx4OK(t, result), &out)
		var payload struct {
			Fields map[string]struct {
				Value string `json:"value"`
			} `json:"fields"`
		}
		if json.Unmarshal(out.Payload, &payload) != nil || payload.Fields["customfield_1"].Value+".json" != file || len(out.Fields) != 1 || out.Fields[0].Handling != "encoded" {
			t.Fatalf("selected input did not control the prepared identity: %s", out.Payload)
		}
	}
	Prove(t, "jira issue input prepare", "input", "two independently selected whole-payload files produce their distinct scalar identities")
	result := mx4Run(t, options, "issue", "input", "prepare", "--input=first.json", "--metadata=unknown.json", "--output=json")
	var out envelope.IssueInputPreparationOutput
	mx4Data(t, mx4OK(t, result), &out)
	if string(out.Payload) != `{"fields":{"customfield_1":"first"}}` || len(out.Fields) != 1 || out.Fields[0].Handling != "unverified" {
		t.Fatal("unknown metadata plugin was coerced by its suffix")
	}
	Prove(t, "jira issue input prepare", "metadata", "known and vendor snapshots independently choose encoded versus unverified literal handling")
	createOptions := mx4Opts{Files: map[string]string{"input.json": `{"fields":{"project":"123","issuetype":"456"}}`, "metadata.json": `{"fields":{}}`}}
	refused := mx4Run(t, createOptions, "issue", "input", "prepare", "--input=input.json", "--metadata=metadata.json", "--output=json")
	frame := mx4Frame(t, refused.Stdout)
	if refused.Exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_field_preflight_failed" {
		t.Fatal("ordinary preparation silently exempted structural create fields")
	}
	result = mx4Run(t, createOptions, "issue", "input", "prepare", "--input=input.json", "--metadata=metadata.json", "--create", "--output=json")
	mx4Data(t, mx4OK(t, result), &out)
	if string(out.Payload) != `{"fields":{"issuetype":{"name":"456"},"project":{"key":"123"}}}` {
		t.Fatal("create exemption lost its explicit key/name meanings")
	}
	Prove(t, "jira issue input prepare", "create", "ordinary screen checking refuses missing structural fields while explicit create prepares fixed key/name objects")
}
