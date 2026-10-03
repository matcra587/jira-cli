package root

import "testing"

func TestReleaseNotesPathsNeverConstructJiraClient(t *testing.T) {
	for _, args := range [][]string{
		{"release-notes", "--output=json"},
		{"rn", "0.7.7", "--output=human"},
		{"release-notes", "--latest"},
		{"release-notes", "missing"},
		{"release-notes", "--help"},
	} {
		exit, constructions := runQuiet(t, args)
		if constructions != 0 || (args[len(args)-1] != "missing" && exit != 0) {
			t.Fatalf("local release notes built a Jira client or failed: %v exit%d constructions%d", args, exit, constructions)
		}
	}
}
