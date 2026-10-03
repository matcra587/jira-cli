//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/gechr/x/set"
)

// TestLiveSearchCursorWalk reads the dedicated project's actual sparse search
// index. It creates no fixtures and never uses a selector to mutate results.
func TestLiveSearchCursorWalk(t *testing.T) {
	r := newLiveRun(t)
	readKey := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	first := r.success(t, "issue", "list", "--project", r.manifest.Project, "--limit", "2")
	if first.Data.Truncated || len(first.Data.Issues) != 2 || first.Data.Pagination.Returned != 2 {
		t.Fatal("bounded live search did not collect two issues through sparse pages")
	}
	seen := set.New[string]()
	for _, issue := range first.Data.Issues {
		if issue.Key == "" || seen.Contains(issue.Key) {
			t.Fatal("bounded live search returned a missing or duplicate key")
		}
		seen.Add(issue.Key)
	}
	if !first.Data.Pagination.IsLast {
		if first.Data.Pagination.NextPageToken == "" {
			t.Fatal("bounded live search omitted its resume cursor")
		}
		next := r.success(t, "issue", "list", "--project", r.manifest.Project, "--limit", "1",
			"--next-page-token", first.Data.Pagination.NextPageToken)
		if next.Data.Truncated || len(next.Data.Issues) != 1 || seen.Contains(next.Data.Issues[0].Key) {
			t.Fatal("live cursor resume failed to return a new issue")
		}
	}
	all := r.success(t, "issue", "list", "--project", r.manifest.Project, "--all")
	if all.Data.Truncated || !all.Data.Pagination.IsLast || all.Data.Pagination.NextPageToken != "" || all.Data.Pagination.Returned != len(all.Data.Issues) {
		t.Fatal("live --all search did not reach its final page with accurate counts")
	}
	seen = set.New[string]()
	for _, issue := range all.Data.Issues {
		if !strings.HasPrefix(issue.Key, r.manifest.Project+"-") || seen.Contains(issue.Key) {
			t.Fatal("full live search returned a foreign or duplicate key")
		}
		seen.Add(issue.Key)
	}
	if !seen.Contains(readKey) {
		t.Fatal("full live search lost the designated read fixture")
	}
}
