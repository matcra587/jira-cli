//go:build live

package live

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestLiveCSVInputFidelity(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "csv source")
	before := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=summary,labels")
	for _, tc := range []struct {
		name     string
		args     []string
		mutation bool
	}{
		{name: "clone replacement", args: []string{"issue", "clone", key, "--replace", "source=changed\nignored=discarded", "--dry-run"}, mutation: true},
		{name: "field mutation", args: []string{"issue", "edit", key, "--label", "first\nignored", "--dry-run"}, mutation: true},
		{name: "hidden alias", args: []string{"issue", "edit", key, "--labels", "first\nignored", "--dry-run"}, mutation: true},
		{name: "field selector", args: []string{"issue", "view", key, "--fields", "summary\nignored"}, mutation: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutation {
				if err := r.checkMutation([]string{key}, tc.args); err != nil {
					t.Fatal(err)
				}
			}
			frame, _, exit := r.invoke(t, t.Context(), nil, tc.args...)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" {
				t.Fatal("changed CSV was not refused as an invalid flag")
			}
		})
	}
	after := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=summary,labels")
	var initial, current struct {
		Fields json.RawMessage `json:"fields"`
	}
	if json.Unmarshal(before.Response.JSON, &initial) != nil || json.Unmarshal(after.Response.JSON, &current) != nil || string(initial.Fields) != string(current.Fields) {
		t.Fatal("CSV refusals changed the owned source")
	}
	summary := r.manifest.RunID + " csv, verified"
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	frame := r.mutate(t, []string{key}, "issue", "clone", key, "--replace", `"csv source=csv, copied"`, "--replace", "copied=verified")
	if len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK {
		t.Fatal("CSV clone did not report its owned source")
	}
	var clone struct {
		Clone   liveIssue `json:"clone"`
		Applied bool      `json:"applied"`
	}
	if json.Unmarshal(frame.Data.Results[0].Data, &clone) != nil {
		t.Fatal("clone did not retain a native identity")
	}
	r.register(t, clone.Clone)
	if !clone.Applied || clone.Clone.Key == key {
		t.Fatal("clone did not acknowledge a distinct owned issue")
	}
	stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(clone.Clone.Key), "--query=fields=summary")
	var native struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	if json.Unmarshal(stored.Response.JSON, &native) != nil || native.Key != clone.Clone.Key || native.Fields.Summary != summary {
		t.Fatal("quoted comma or ordered repeated replacements differed from native storage")
	}
}
