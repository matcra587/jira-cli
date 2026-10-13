package command

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestConfiguredBoardNarrowsRawWithoutReleasingAccountOrOrdering(t *testing.T) {
	t.Parallel()
	for _, mine := range []bool{false, true} {
		t.Run(map[bool]string{true: "mine", false: "ordinary"}[mine], func(t *testing.T) {
			t.Parallel()
			query, err := ComposeJQL(JQLInput{JQL: "status = Done OR labels = urgent ORDER BY created DESC", BoardJQL: "project = ONE OR project = TWO ORDER BY Rank ASC", BoardFromDefault: true, AssignedToCurrentUser: mine})
			want := "(project = ONE OR project = TWO) AND "
			if mine {
				want += "assignee = currentUser() AND "
			}
			want += "(status = Done OR labels = urgent) ORDER BY created DESC"
			if err != nil || query != want {
				t.Fatal("configured board released scope or ordering", query, err)
			}
			_, err = ComposeJQL(JQLInput{JQL: "status = Done", BoardJQL: "project = ONE", AssignedToCurrentUser: mine})
			if cli.MapError(err).Code != cli.CodeFlagNotApplicable {
				t.Fatal("explicit board allowed competing raw query", err)
			}
		})
	}
	for _, raw := range []string{"status = Done) OR project = OTHER OR (status = Open", "(status = Done", `summary ~ "unterminated`} {
		if _, err := ComposeJQL(JQLInput{JQL: raw, BoardJQL: "project = ONE", BoardFromDefault: true}); cli.MapError(err).Code != cli.CodeFlagValueInvalid {
			t.Fatal("raw conditions escaped configured scope", err)
		}
	}
	query, err := ComposeJQL(JQLInput{JQL: "status = Done", Project: []string{"FLAGGED"}, BoardJQL: "project = ONE", BoardFromDefault: true})
	if cli.MapError(err).Code != cli.CodeFlagNotApplicable || query != "" {
		t.Fatal("default scope relaxed explicit filter/raw refusal", err)
	}
	if _, err := ComposeJQL(JQLInput{JQL: "status = Done", BoardJQL: "ORDER BY Rank", BoardFromDefault: true}); err == nil || strings.Contains(err.Error(), "token") {
		t.Fatal("empty cached conditions established scope", err)
	}
}
