//go:build live

package live

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestLiveIssueFieldPreflight(t *testing.T) {
	r := newLiveRun(t)
	summary := r.manifest.RunID + " field preflight"
	args := []string{
		"issue", "create", "--project", r.manifest.Project, "--type", r.issueType,
		"--summary", summary, "--label", r.manifest.RunID, "--validate-remote",
	}
	preview, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append(slices.Clone(args), "--dry-run")...)
	var proposed struct {
		Data envelope.IssueCreateOutput `json:"data"`
	}
	if exit != 0 || !preview.OK || json.Unmarshal(stdout, &proposed) != nil || !proposed.Data.DryRun || proposed.Data.Attempted || proposed.Data.Applied || proposed.Data.FieldPreflight == nil || !slices.Equal(proposed.Data.FieldPreflight.Fields, []string{"labels", "summary"}) {
		t.Fatal("read-only create preview did not establish the requested field preflight")
	}
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	created, stdout, exit := r.invoke(t, t.Context(), nil, args...)
	if exit != 0 || !created.OK {
		t.Fatal("owned preflight creation failed; retain its attempt before repeating")
	}
	r.register(t, created.Data.Issue)
	var applied struct {
		Data envelope.IssueCreateOutput `json:"data"`
	}
	if json.Unmarshal(stdout, &applied) != nil || !applied.Data.Applied || applied.Data.FieldPreflight == nil || string(applied.Data.Payload) != string(proposed.Data.Payload) {
		t.Fatal("owned create changed the preflight payload or lost its receipt")
	}
	key := created.Data.Issue.Key
	editArgs := []string{"issue", "edit", key, "--summary", summary + " edited", "--label", r.manifest.RunID + "-added", "--validate-remote"}
	if err := r.checkMutation([]string{key}, editArgs); err != nil {
		t.Fatal(err)
	}
	changed, stdout, exit := r.invoke(t, t.Context(), nil, editArgs...)
	var edited struct {
		Data envelope.IssueEditOutput `json:"data"`
	}
	if exit != 0 || !changed.OK || json.Unmarshal(stdout, &edited) != nil || len(edited.Data.Results) != 1 || !edited.Data.Results[0].OK || edited.Data.Results[0].Data == nil || edited.Data.Results[0].Data.FieldPreflight == nil || !slices.Equal(edited.Data.Results[0].Data.FieldPreflight.Fields, []string{"labels", "summary"}) {
		t.Fatal("owned edit did not retain its issue-bound field preflight")
	}
	input := r.nativeMissingFieldInput(t, key)
	refusalArgs := []string{"issue", "edit", key, "--json-input", input, "--validate-remote"}
	if err := r.checkMutation([]string{key}, refusalArgs); err != nil {
		t.Fatal(err)
	}
	refused, _, exit := r.invoke(t, t.Context(), nil, refusalArgs...)
	if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "issue_field_preflight_failed" {
		t.Fatal("unavailable owned field was not refused by metadata preflight")
	}
	stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,labels")
	var native struct {
		Fields struct {
			Summary string   `json:"summary"`
			Labels  []string `json:"labels"`
		} `json:"fields"`
	}
	if json.Unmarshal(stored.Response.JSON, &native) != nil || native.Fields.Summary != summary+" edited" || !slices.Contains(native.Fields.Labels, r.manifest.RunID) || !slices.Contains(native.Fields.Labels, r.manifest.RunID+"-added") {
		t.Fatal("independent state read disagrees with admitted writes or lost ownership")
	}
}
