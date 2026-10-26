package command

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestRawOverridesConfiguredBoardWithoutReleasingAccountOrOrdering(t *testing.T) {
	t.Parallel()
	for _, mine := range []bool{false, true} {
		t.Run(map[bool]string{true: "mine", false: "ordinary"}[mine], func(t *testing.T) {
			t.Parallel()
			query, err := ComposeJQL(JQLInput{JQL: "status = Done OR labels = urgent ORDER BY created DESC", BoardJQL: "project = ONE OR project = TWO ORDER BY Rank ASC", BoardFromDefault: true, AssignedToCurrentUser: mine})
			want := "status = Done OR labels = urgent ORDER BY created DESC"
			if mine {
				want = "assignee = currentUser() AND (status = Done OR labels = urgent) ORDER BY created DESC"
			}
			if err != nil || query != want {
				t.Fatal("configured board changed explicit query or account scope", query, err)
			}
			_, err = ComposeJQL(JQLInput{JQL: "status = Done", BoardJQL: "project = ONE", AssignedToCurrentUser: mine})
			if cli.MapError(err).Code != cli.CodeFlagNotApplicable {
				t.Fatal("explicit board allowed competing raw query", err)
			}
		})
	}
	for _, raw := range []string{"status = Done) OR project = OTHER OR (status = Open", "(status = Done", `summary ~ "unterminated`} {
		if _, err := ComposeJQL(JQLInput{JQL: raw, BoardJQL: "project = ONE", BoardFromDefault: true, AssignedToCurrentUser: true}); cli.MapError(err).Code != cli.CodeFlagValueInvalid {
			t.Fatal("raw conditions escaped required account scope", err)
		}
	}
	query, err := ComposeJQL(JQLInput{JQL: "status = Done", Project: []string{"FLAGGED"}, BoardJQL: "project = ONE", BoardFromDefault: true})
	if cli.MapError(err).Code != cli.CodeFlagNotApplicable || query != "" {
		t.Fatal("default scope relaxed explicit filter/raw refusal", err)
	}
	if query, err := ComposeJQL(JQLInput{JQL: "status = Done", BoardJQL: "ORDER BY Rank", BoardFromDefault: true}); err != nil || query != "status = Done" {
		t.Fatal("unused cached conditions blocked explicit query", query, err)
	}
}
