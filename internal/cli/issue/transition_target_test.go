package issue

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

// splitTransitionTarget reads a trailing status name/id off the argument list
// when --transition is not used, but leaves an all-key list alone (so bulk
// listing still works) and an explicit flag wins.
func TestSplitTransitionTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		flag       string
		wantTarget string
		wantKeys   []string
	}{
		{name: "key plus status name", args: []string{"JCT-1", "In Progress"}, wantTarget: "In Progress", wantKeys: []string{"JCT-1"}},
		{name: "unquoted multi-word status", args: []string{"JCT-1", "In", "Progress"}, wantTarget: "In Progress", wantKeys: []string{"JCT-1"}},
		{name: "bulk keys plus unquoted multi-word", args: []string{"JCT-1", "JCT-2", "Code", "Review"}, wantTarget: "Code Review", wantKeys: []string{"JCT-1", "JCT-2"}},
		{name: "key plus numeric id", args: []string{"JCT-1", "31"}, wantTarget: "31", wantKeys: []string{"JCT-1"}},
		{name: "bulk keys plus status", args: []string{"JCT-1", "JCT-2", "Done"}, wantTarget: "Done", wantKeys: []string{"JCT-1", "JCT-2"}},
		{name: "all keys list mode", args: []string{"JCT-1", "JCT-2"}, wantTarget: "", wantKeys: []string{"JCT-1", "JCT-2"}},
		{name: "single key list mode", args: []string{"JCT-1"}, wantTarget: "", wantKeys: []string{"JCT-1"}},
		{name: "explicit flag wins, all args are keys", args: []string{"JCT-1", "JCT-2"}, flag: "41", wantTarget: "41", wantKeys: []string{"JCT-1", "JCT-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, keyArgs := splitTransitionTarget(tc.args, tc.flag)
			if target != tc.wantTarget {
				t.Fatalf("target = %q, want %q", target, tc.wantTarget)
			}
			if len(keyArgs) != len(tc.wantKeys) {
				t.Fatalf("keyArgs = %v, want %v", keyArgs, tc.wantKeys)
			}
			for i := range keyArgs {
				if keyArgs[i] != tc.wantKeys[i] {
					t.Fatalf("keyArgs = %v, want %v", keyArgs, tc.wantKeys)
				}
			}
		})
	}
}

// matchTransition resolves a status name (case-insensitive) or an id to the
// transition id, preferring a name match, and errors when nothing matches.
func TestMatchTransition(t *testing.T) {
	transitions := []*jira.Transition{
		{ID: new("21"), Name: new("In Progress")},
		{ID: new("31"), Name: new("Code Review")},
	}
	for _, tc := range []struct {
		name   string
		target string
		wantID string
		wantOK bool
	}{
		{name: "exact name", target: "Code Review", wantID: "31", wantOK: true},
		{name: "case-insensitive name", target: "in progress", wantID: "21", wantOK: true},
		{name: "id falls through to id match", target: "31", wantID: "31", wantOK: true},
		{name: "unknown name errors", target: "Nope", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := matchTransition(transitions, tc.target, "JCT-1")
			if tc.wantOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if id != tc.wantID {
					t.Fatalf("id = %q, want %q", id, tc.wantID)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestIsAllDigits(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"31", true},
		{"0", true},
		{"", false},
		{"3a", false},
		{"-5", false},
		{"In Progress", false},
	} {
		if got := isAllDigits(tc.in); got != tc.want {
			t.Errorf("isAllDigits(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
