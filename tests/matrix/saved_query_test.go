//go:build flagmatrix

package matrix

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestSavedQueryFlagsChangeTheActualLocalQuery(t *testing.T) {
	for _, verb := range []string{"list", "mine"} {
		t.Run(verb, func(t *testing.T) {
			res := mx4Run(t, mx4Opts{Files: map[string]string{"settings.toml": "queries_path = '.'\nkeyring = false\n", "inbox.jql": "project = ABC ORDER BY key ASC"}}, "issue", verb, "--saved=inbox", "--as-jql", "--config=settings.toml", "--output=json")
			var out envelope.IssueListOutput
			mx4Data(t, mx4OK(t, res), &out)
			want := "project = ABC ORDER BY key ASC"
			if verb == "mine" {
				want = "assignee = currentUser() AND (project = ABC) ORDER BY key ASC"
			}
			if out.JQL != want || out.SavedQuery == nil || out.SavedQuery.Key != "inbox" || out.SavedQuery.JQL != "project = ABC ORDER BY key ASC" {
				t.Fatal("saved flag was ignored or its literal query lost")
			}
			Prove(t, "jira issue "+verb, "saved", "the real binary loads authored local JQL, publishes its filename stem and retains the mandatory current-account scope on mine")
		})
	}
}
