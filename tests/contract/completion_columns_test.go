package contract

import (
	"slices"
	"testing"
)

func TestCompletionColumnsUseAcceptedTableFields(t *testing.T) {
	script, stderr, exit := runBinary(t, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate Bash exit=%d stderr=%q", exit, stderr)
	}
	for _, command := range []string{"list", "mine"} {
		t.Run(command, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				args []string
				want []string
			}{
				{name: "all columns", args: []string{"--columns", ""}, want: []string{"key", "status", "priority", "type", "summary", "assignee", "updated"}},
				{name: "prefix", args: []string{"--columns", "su"}, want: []string{"summary"}},
				{name: "comma continuation", args: []string{"--columns", "key,su"}, want: []string{"key,summary"}},
				{name: "no duplicate", args: []string{"--columns", "key,ke"}},
				{name: "no wire fields", args: []string{"--columns", "customfield"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					words := append([]string{"jira", "issue", command}, tc.args...)
					got := completionArgumentReplies(t, script, nil, words)
					if !slices.Equal(got, tc.want) {
						t.Fatalf("column candidates=%q, want %q", got, tc.want)
					}
				})
			}
		})
	}
}
