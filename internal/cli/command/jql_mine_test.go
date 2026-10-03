package command

import (
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestComposeCurrentAccountScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   JQLInput
		want string
	}{
		{"required account", JQLInput{}, `assignee = currentUser()`},
		{"or cannot widen", JQLInput{JQL: `assignee is EMPTY OR project = ABC ORDER BY key ASC`}, `assignee = currentUser() AND (assignee is EMPTY OR project = ABC) ORDER BY key ASC`},
		{"structured and raw", JQLInput{Project: []string{"ABC"}, JQL: `status = Open OR status = Done`}, `project in ("ABC") AND assignee = currentUser() AND (status = Open OR status = Done)`},
		{"board narrows account", JQLInput{BoardJQL: `project = ABC OR project = DEF ORDER BY Rank ASC`}, `(project = ABC OR project = DEF) AND assignee = currentUser() ORDER BY Rank ASC`},
		{"raw order wins", JQLInput{JQL: `project = ABC ORDER BY key ASC`, OrderBy: "updated"}, `assignee = currentUser() AND (project = ABC) ORDER BY key ASC`},
		{"raw order only", JQLInput{JQL: `ORDER BY key ASC`}, `assignee = currentUser() ORDER BY key ASC`},
		{"raw explicit fallback sort", JQLInput{JQL: `project = ABC`, OrderBy: "created"}, `assignee = currentUser() AND (project = ABC) ORDER BY created DESC`},
		{"escaped literals and nested groups", JQLInput{JQL: `summary ~ "a\" ) OR ( order by key" AND (status = Open OR status = Done) ORDER BY key ASC`}, `assignee = currentUser() AND (summary ~ "a\" ) OR ( order by key" AND (status = Open OR status = Done)) ORDER BY key ASC`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.AssignedToCurrentUser = true
			got, err := ComposeJQL(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("current-account query=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestComposeCurrentAccountScopeRejectsUnbalancedBoard(t *testing.T) {
	for _, board := range []string{`project = ABC) OR project = DEF OR (project = ABC`, `(project = ABC`, `summary ~ "unclosed`} {
		t.Run(board, func(t *testing.T) {
			_, err := ComposeJQL(JQLInput{AssignedToCurrentUser: true, BoardJQL: board})
			var input *cli.InputError
			if !errors.As(err, &input) || input.Code != cli.CodeFlagValueInvalid || input.Field != "board" {
				t.Fatalf("unbalanced board escaped scope or lost classification: %v", err)
			}
		})
	}
}

func TestComposeCurrentAccountScopeRefusesCompetingAssignee(t *testing.T) {
	_, err := ComposeJQL(JQLInput{AssignedToCurrentUser: true, Assignee: []string{"none"}, JQL: "project = ABC"})
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeFlagConflict || input.Field != "assignee" {
		t.Fatalf("competing account filter accepted or misclassified: %v", err)
	}
}

func TestComposeCurrentAccountScopeRetainsRawBoardRefusal(t *testing.T) {
	_, err := ComposeJQL(JQLInput{AssignedToCurrentUser: true, BoardJQL: "project = ABC", JQL: "project = DEF"})
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeFlagNotApplicable || input.Field != "board" {
		t.Fatalf("raw query competed with board scope: %v", err)
	}
}
