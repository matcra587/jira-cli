//go:build live

package live

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveMine uses two assigned fixtures and an explicitly unassigned control.
// Native identity and search reads establish the expected set independently of
// the CLI's currentUser composition. All writes target this run's owned issues.
func TestLiveMine(t *testing.T) {
	r := newLiveRun(t)
	self := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account jira.User
	if json.Unmarshal(self.Response.JSON, &account) != nil || !account.AccountID.Set || account.AccountID.Null || account.AccountID.Value == "" {
		t.Fatal("current-account selection requires an independently read account identity")
	}
	assigned := []string{r.create(t, "mine first"), r.create(t, "mine second")}
	control := r.create(t, "mine unassigned control")
	for _, key := range assigned {
		r.mutate(t, []string{key}, "issue", "edit", key, "--assignee=@me")
	}
	r.mutate(t, []string{control}, "issue", "edit", control, "--unassign")
	allKeys := append(slices.Clone(assigned), control)
	ids := make([]int64, 0, len(allKeys))
	for _, key := range allKeys {
		native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=assignee,labels")
		var stored struct {
			ID     string `json:"id"`
			Key    string `json:"key"`
			Fields struct {
				Assignee jira.Nullable[jira.User] `json:"assignee"`
				Labels   []string                 `json:"labels"`
			} `json:"fields"`
		}
		if json.Unmarshal(native.Response.JSON, &stored) != nil || stored.Key != key || !slices.Contains(stored.Fields.Labels, r.manifest.RunID) {
			t.Fatal("independent native read lost fixture identity or ownership label")
		}
		if !mineAssignmentMatches(stored.Fields.Assignee, account.AccountID.Value, key == control) {
			t.Fatal("native assignee observation does not establish the authored fixture state")
		}
		id, err := strconv.ParseInt(stored.ID, 10, 64)
		if err != nil || id <= 0 {
			t.Fatal("native fixture identity cannot be used for search reconciliation")
		}
		ids = append(ids, id)
	}
	accountValue, err := command.QuoteJQL(account.AccountID.Value)
	if err != nil {
		t.Fatal("native current account cannot be safely quoted")
	}
	nativeQuery := "key in (" + strings.Join(allKeys, ",") + ") AND assignee = " + accountValue + " ORDER BY key ASC"
	body, err := json.Marshal(struct {
		JQL             string   `json:"jql"`
		Fields          []string `json:"fields"`
		MaxResults      int      `json:"maxResults"`
		ReconcileIssues []int64  `json:"reconcileIssues"`
	}{nativeQuery, []string{"assignee"}, len(allKeys), ids})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(r.dir, "mine-native-search.json")
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal(err)
	}
	independent := rawRead(t, r, "api", "post", "/rest/api/3/search/jql", "--input", input)
	var nativePage struct {
		Issues []liveIssue `json:"issues"`
		IsLast *bool       `json:"isLast"`
	}
	if json.Unmarshal(independent.Response.JSON, &nativePage) != nil || nativePage.IsLast == nil || !*nativePage.IsLast || !mineMatches(nativePage.Issues, assigned, account.AccountID.Value) {
		t.Fatal("independent reconciled native search differs from the stored current-account fixture set")
	}
	args := []string{"--project=" + r.manifest.Project, "--label=" + r.manifest.RunID, "--order-by=key"}
	for _, verb := range []string{"mine", "list"} {
		query := slices.Clone(args)
		if verb == "list" {
			query = append(query, "--assignee=me")
		}
		frame := waitAssignedSearch(t, r, account.AccountID.Value, assigned, verb, query)
		if !frame.OK || frame.Data.Truncated || !frame.Data.Pagination.IsLast || frame.Data.Pagination.Returned != len(assigned) || !mineMatches(frame.Data.Issues, assigned, account.AccountID.Value) {
			t.Fatal("current-account domain search differs from the independent native result")
		}
	}
	raw := r.success(t, "issue", "mine", "--jql=key = "+assigned[0]+" OR key = "+control+" ORDER BY key ASC", "--all")
	if raw.Data.Truncated || !raw.Data.Pagination.IsLast || !mineMatches(raw.Data.Issues, assigned[:1], account.AccountID.Value) {
		t.Fatal("raw OR escaped the required current-account scope")
	}
	dir := filepath.Join(r.dir, "queries")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assigned.jql"), []byte("key = "+assigned[0]+" OR key = "+control+" ORDER BY key ASC"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "JIRA_QUERIES_PATH=" + dir}, "issue", "mine", "--saved=assigned", "--all")
	if exit != 0 || !saved.OK || saved.Data.Truncated || !saved.Data.Pagination.IsLast || !mineMatches(saved.Data.Issues, assigned[:1], account.AccountID.Value) {
		t.Fatal("saved OR escaped the independently established current-account scope")
	}
	malformed := "key = " + assigned[0] + ") OR key = " + control + " OR (key = " + assigned[0]
	refused, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "mine", "--jql="+malformed)
	if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "flag_value_invalid" {
		t.Fatal("unbalanced raw grouping was allowed to escape current-account scope")
	}
	refused, _, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "mine", "--jql=key = "+assigned[0], "--reverse")
	if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "flag_not_applicable" {
		t.Fatal("raw current-account search silently ignored reverse without an explicit sort")
	}
	firstArgs := append([]string{"issue", "mine"}, args...)
	first := r.success(t, append(firstArgs, "--limit=1")...)
	if len(first.Data.Issues) != 1 || first.Data.Pagination.Returned != 1 || first.Data.Pagination.IsLast || first.Data.Pagination.NextPageToken == "" || first.Data.Truncated {
		t.Fatal("bounded current-account search lost its honest resume cursor")
	}
	nextArgs := append([]string{"issue", "mine"}, args...)
	next := r.success(t, append(nextArgs, "--limit=1", "--next-page-token", first.Data.Pagination.NextPageToken)...)
	combined := append(slices.Clone(first.Data.Issues), next.Data.Issues...)
	if len(next.Data.Issues) != 1 || next.Data.Pagination.Returned != 1 || next.Data.Truncated || !mineMatches(combined, assigned, account.AccountID.Value) {
		t.Fatal("current-account cursor resume duplicated or lost an assigned fixture")
	}
}

func mineMatches(issues []liveIssue, keys []string, accountID string) bool {
	if accountID == "" || len(issues) != len(keys) {
		return false
	}
	seen := make(map[string]bool, len(issues))
	for _, issue := range issues {
		var fields struct {
			Assignee jira.Nullable[jira.User] `json:"assignee"`
		}
		if !slices.Contains(keys, issue.Key) || seen[issue.Key] || json.Unmarshal(issue.Fields, &fields) != nil || !mineAssignmentMatches(fields.Assignee, accountID, false) {
			return false
		}
		seen[issue.Key] = true
	}
	return true
}

func mineAssignmentMatches(assignee jira.Nullable[jira.User], accountID string, unassigned bool) bool {
	if accountID == "" || !assignee.Set {
		return false
	}
	if unassigned {
		return assignee.Null
	}
	return assignee.Set && !assignee.Null && assignee.Value.AccountID.Set && !assignee.Value.AccountID.Null && assignee.Value.AccountID.Value == accountID
}

// Jira's enhanced search is eventually consistent. Only missing assigned
// fixtures may trigger another bounded read; a foreign or unassigned result is
// an immediate failure, and no mutation is retried by this index wait. Return
// the validated result itself: a later identical search may hit a stale index.
func waitAssignedSearch(t *testing.T, r *liveRun, accountID string, keys []string, verb string, args []string) liveFrame {
	t.Helper()
	if accountID == "" || len(keys) == 0 || (verb != "mine" && verb != "list") {
		t.Fatal("current-account index wait requires a known account, owned expected set and supported search route")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for {
		query := append([]string{"issue", verb}, args...)
		frame, _, exit := r.invoke(t, ctx, []string{"JIRA_READ_ONLY=true"}, append(query, "--all")...)
		if exit != 0 || !frame.OK || frame.Data.Issues == nil || frame.Data.Truncated || !frame.Data.Pagination.IsLast || frame.Data.Pagination.Returned != len(frame.Data.Issues) {
			t.Fatal("current-account index wait received a failed or incomplete read")
		}
		if mineMatches(frame.Data.Issues, keys, accountID) {
			return frame
		}
		if !mineMissingOnly(frame.Data.Issues, keys, accountID) {
			t.Fatal("current-account index wait observed a duplicate, unassigned, control or foreign fixture")
		}
		select {
		case <-ctx.Done():
			t.Fatal("assigned fixtures did not become visible within the bounded index wait")
		case <-time.After(time.Second):
		}
	}
}

// A short result may wait only when every observed row is a unique, explicitly
// assigned member of the expected owned set. Invalid results never get retried.
func mineMissingOnly(issues []liveIssue, keys []string, accountID string) bool {
	if accountID == "" || issues == nil || len(keys) == 0 || len(issues) >= len(keys) {
		return false
	}
	observed := make([]string, len(issues))
	for index, issue := range issues {
		if !slices.Contains(keys, issue.Key) {
			return false
		}
		observed[index] = issue.Key
	}
	return mineMatches(issues, observed, accountID)
}
