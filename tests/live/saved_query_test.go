//go:build live

package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveSavedQueries uses only reads of the dedicated project. Independently
// drained native pages establish the exact key order and summaries. Authored
// local query files exercise both metadata formats and honest cursor resume.
func TestLiveSavedQueries(t *testing.T) {
	r := newLiveRun(t)
	project, err := command.QuoteJQL(r.manifest.Project)
	if err != nil {
		t.Fatal(err)
	}
	query := "project = " + project + " ORDER BY key ASC"
	var expected []jira.IssueBean
	token := ""
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages >= command.MaxSearchPages {
			t.Fatal("independent saved-query search exceeded its page bound")
		}
		args := []string{"api", "get", "/rest/api/3/search/jql", "--query=jql=" + query, "--query=fields=summary", "--query=maxResults=50"}
		if token != "" {
			args = append(args, "--query=nextPageToken="+token)
		}
		native := rawRead(t, r, args...)
		var page jira.SearchResults
		if json.Unmarshal(native.Response.JSON, &page) != nil || page.ValidatePage(50) != nil {
			t.Fatal("independent saved-query search returned an invalid native page")
		}
		for _, issue := range page.Issues.Value {
			if !strings.HasPrefix(issue.Key.Value, r.manifest.Project+"-") || seen[issue.Key.Value] {
				t.Fatal("independent saved-query search returned a foreign or duplicate issue")
			}
			seen[issue.Key.Value] = true
			expected = append(expected, issue)
		}
		if page.IsLast.Value {
			break
		}
		if page.NextPageToken.Value == "" || page.NextPageToken.Value == token || cursors[page.NextPageToken.Value] {
			t.Fatal("independent saved-query search lost cursor progress")
		}
		token = page.NextPageToken.Value
		cursors[token] = true
	}
	if len(expected) < 2 || !seen[requiredEnv(t, "JIRA_LIVE_READ_KEY")] {
		t.Fatal("native saved-query fixture requires two visible project issues and the designated read fixture")
	}
	dir := filepath.Join(r.dir, "queries")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body, display string }{
		{"plain", query, "plain"},
		{"yaml", "---\nname: Owned read query\ndescription: Native search comparison\nproject: metadata-only\n---\n" + query, "Owned read query"},
		{"toml", "+++\nname = 'Owned read query'\ndescription = 'Native search comparison'\nproject = 'metadata-only'\n+++\n" + query, "Owned read query"},
	} {
		if err := os.WriteFile(filepath.Join(dir, tc.name+".jql"), []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		env := []string{"JIRA_READ_ONLY=true", "JIRA_QUERIES_PATH=" + dir}
		frame, stdout, exit := r.invoke(t, t.Context(), env, "issue", "list", "--saved="+tc.name, "--all", "--fields=summary")
		var receipt struct {
			Data struct {
				SavedQuery *envelope.SavedQueryView `json:"saved_query"`
			}
		}
		if exit != 0 || !frame.OK || frame.Data.Truncated || !frame.Data.Pagination.IsLast || frame.Data.Pagination.Returned != len(expected) || !savedNativeRowsMatch(frame.Data.Issues, expected) || json.Unmarshal(stdout, &receipt) != nil || receipt.Data.SavedQuery == nil {
			t.Fatal("saved-query results differ from the independently drained native search")
		}
		view := receipt.Data.SavedQuery
		if view.Key != tc.name || view.Name != tc.display || view.JQL != query {
			t.Fatal("saved-query native receipt lost its authored identity or JQL")
		}
		if tc.name != "plain" && (view.Description != "Native search comparison" || view.Project != "metadata-only") {
			t.Fatal("saved-query metadata was lost or silently added as a project filter")
		}
	}
	env := []string{"JIRA_READ_ONLY=true", "JIRA_QUERIES_PATH=" + dir}
	first, _, exit := r.invoke(t, t.Context(), env, "issue", "list", "--saved=yaml", "--fields=summary", "--limit=1")
	if exit != 0 || !first.OK || first.Data.Truncated || first.Data.Pagination.IsLast || first.Data.Pagination.NextPageToken == "" || !savedNativeRowsMatch(first.Data.Issues, expected[:1]) {
		t.Fatal("bounded saved-query search lost its first native row or resume cursor")
	}
	next, _, exit := r.invoke(t, t.Context(), env, "issue", "list", "--saved=yaml", "--fields=summary", "--limit=1", "--next-page-token", first.Data.Pagination.NextPageToken)
	if exit != 0 || !next.OK || next.Data.Truncated || !savedNativeRowsMatch(next.Data.Issues, expected[1:2]) {
		t.Fatal("saved-query cursor resume duplicated or changed native rows")
	}
	missing, _, exit := r.invoke(t, t.Context(), env, "issue", "list", "--saved=missing")
	if exit != 3 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "saved_query_unknown" {
		t.Fatal("missing local query was accepted or classified as a Jira resource")
	}
}

func savedNativeRowsMatch(actual []liveIssue, expected []jira.IssueBean) bool {
	if actual == nil || len(actual) != len(expected) {
		return false
	}
	for index, issue := range actual {
		var fields struct {
			Summary jira.Nullable[string] `json:"summary"`
		}
		if !expected[index].Fields.Set || expected[index].Fields.Null || issue.Key != expected[index].Key.Value || json.Unmarshal(issue.Fields, &fields) != nil || fields.Summary != expected[index].Fields.Value.Summary {
			return false
		}
	}
	return true
}
