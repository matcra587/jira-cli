//go:build live

package live

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestLiveIssueWriteFieldVerification(t *testing.T) {
	r := newLiveRun(t)
	summary := r.manifest.RunID + " field verification"
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	created, stdout, exit := r.invoke(t, t.Context(), nil, "issue", "create", "--project", r.manifest.Project,
		"--type", r.issueType, "--summary", summary, "--label", r.manifest.RunID, "--verify")
	if exit != 0 || !created.OK {
		t.Fatal("owned verification creation failed; inspect the retained attempt before repeating it")
	}
	r.register(t, created.Data.Issue)
	var frame struct {
		Data envelope.IssueCreateOutput `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil || !frame.Data.Applied || frame.Data.VerifyFailed || frame.Data.Verified == nil {
		t.Fatal("creation lost its acknowledgement or requested-field verification")
	}
	assertLiveVerifiedFields(t, frame.Data.Verified, []string{"issuetype", "labels", "project", "summary"})
	key := created.Data.Issue.Key
	added := r.manifest.RunID + "-added"
	for _, tc := range []struct {
		name, flag string
		present    bool
	}{
		{"add", "--label", true},
		{"remove", "--remove-label", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := r.mutate(t, []string{key}, "issue", "edit", key, "--summary", summary+" edited", tc.flag, added, "--verify")
			var result envelope.IssueEditData
			if len(changed.Data.Results) != 1 || !changed.Data.Results[0].OK || json.Unmarshal(changed.Data.Results[0].Data, &result) != nil || result.VerifyFailed || result.Verified == nil {
				t.Fatal("owned edit lost requested-field readback")
			}
			assertLiveVerifiedFields(t, result.Verified, []string{"labels", "summary"})
			stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,labels")
			var native struct {
				Fields struct {
					Summary string   `json:"summary"`
					Labels  []string `json:"labels"`
				} `json:"fields"`
			}
			if json.Unmarshal(stored.Response.JSON, &native) != nil || native.Fields.Summary != summary+" edited" || !slices.Contains(native.Fields.Labels, r.manifest.RunID) || slices.Contains(native.Fields.Labels, added) != tc.present {
				t.Fatal("independent native read disagrees with verification or lost ownership")
			}
		})
	}
}

func assertLiveVerifiedFields(t *testing.T, verified *envelope.VerifiedIssue, expected []string) {
	t.Helper()
	var names []string
	for _, field := range verified.Fields {
		names = append(names, field.Field)
		if field.Outcome != "matches" || !field.Observed.Set || field.Observed.Null {
			t.Fatalf("real Jira requested field did not match its readback: field=%s outcome=%s reason=%s", field.Field, field.Outcome, field.Reason)
		}
	}
	if !slices.Equal(names, expected) {
		t.Fatalf("real verification omitted or broadened requested fields: got=%v want=%v", names, expected)
	}
}
