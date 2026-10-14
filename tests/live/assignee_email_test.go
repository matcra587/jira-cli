//go:build live

package live

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestLiveAssigneeEmail(t *testing.T) {
	r := newLiveRun(t)
	selfRead := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var self jira.User
	if json.Unmarshal(selfRead.Response.JSON, &self) != nil || self.AccountID.Value == "" {
		t.Fatal("cannot establish independent authenticated account")
	}
	summary := r.manifest.RunID + " email assignee"
	args := []string{"issue", "create", "--project", r.manifest.Project, "--type", r.issueType, "--summary", summary, "--label", r.manifest.RunID, "--assignee", r.account}
	_, previewBytes, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append(slices.Clone(args), "--dry-run")...)
	var preview struct {
		Data     envelope.IssueCreateOutput `json:"data"`
		Warnings []envelope.Warning         `json:"warnings"`
	}
	if exit != 0 || json.Unmarshal(previewBytes, &preview) != nil || !preview.Data.DryRun || len(preview.Warnings) != 1 || preview.Warnings[0].Type != "account_email_unresolved" {
		t.Fatal("local email preview lost unresolved warning")
	}
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	created, _, exit := r.invoke(t, t.Context(), nil, args...)
	if exit != 0 || !created.OK {
		t.Fatal("owned email assignment creation failed; inspect receipt before repeating")
	}
	r.register(t, created.Data.Issue)
	key := created.Data.Issue.Key
	assertLiveAssignee(t, r, key, self.AccountID.Value)
	r.mutate(t, []string{key}, "issue", "edit", key, "--unassign")
	assertLiveAssignee(t, r, key, "")
	r.mutate(t, []string{key}, "issue", "edit", key, "--assignee", r.account)
	assertLiveAssignee(t, r, key, self.AccountID.Value)
}

func assertLiveAssignee(t *testing.T, r *liveRun, key, want string) {
	t.Helper()
	observed := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=assignee,labels")
	var issue struct {
		Fields struct {
			Assignee *jira.User `json:"assignee"`
			Labels   []string   `json:"labels"`
		} `json:"fields"`
	}
	if json.Unmarshal(observed.Response.JSON, &issue) != nil || !slices.Contains(issue.Fields.Labels, r.manifest.RunID) {
		t.Fatal("assignee read lost owned native issue state")
	}
	if want == "" {
		if issue.Fields.Assignee != nil {
			t.Fatal("independent read did not confirm unassignment")
		}
		return
	}
	if issue.Fields.Assignee == nil || issue.Fields.Assignee.AccountID.Value != want {
		t.Fatal("independent read disagrees with resolved account")
	}
}
