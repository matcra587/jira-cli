package guardrails

import (
	"slices"
	"testing"

	"github.com/gechr/clib/complete"
)

func TestCachedMetadataPredictorsReachSupportedFlagValues(t *testing.T) {
	gen := guardProgram(t).Generator()
	sites := declaredPredictors(gen)
	for _, path := range []string{"jira issue list", "jira issue mine", "jira jql build"} {
		for _, tc := range []struct{ flag, kind string }{
			{flag: "project", kind: "cacheproject"}, {flag: "epic", kind: "cacheepic"}, {flag: "status", kind: "cachestatus"}, {flag: "type", kind: "cacheissuetype"}, {flag: "label", kind: "cachelabel"}, {flag: "priority", kind: "cachepriority"}, {flag: "key", kind: "issuekey"},
		} {
			if !slices.Contains(sites[tc.kind], path+" --"+tc.flag) {
				t.Errorf("missing %s at %s --%s", tc.kind, path, tc.flag)
			}
		}
	}
	for _, path := range []string{"jira issue create", "jira issue edit", "jira issue clone"} {
		for _, tc := range []struct{ flag, kind string }{{flag: "type", kind: "cacheissuetype"}, {flag: "priority", kind: "cachepriority"}, {flag: "label", kind: "cachelabel"}, {flag: "parent", kind: "issuekey"}} {
			if !slices.Contains(sites[tc.kind], path+" --"+tc.flag) {
				t.Errorf("missing %s at %s --%s", tc.kind, path, tc.flag)
			}
		}
	}
	for _, tc := range []struct {
		kind  string
		paths []string
	}{
		{kind: "cacheproject", paths: []string{"jira issue create --project", "jira issue clone --project", "jira epic list --project", "jira epic board --project"}},
		{kind: "cachefield", paths: []string{"jira issue view --fields", "jira issue list --fields", "jira issue mine --fields"}},
		{kind: "cachelabel", paths: []string{"jira issue edit --remove-label"}},
		{kind: "cacheboard", paths: []string{"jira issue list --board", "jira issue mine --board", "jira jql build --board"}},
		{kind: "cacheboardid", paths: []string{"jira issue list --board-id", "jira issue mine --board-id", "jira jql build --board-id"}},
		{kind: "cachelinktype", paths: []string{"jira issue link create --type"}},
		{kind: "cacheresolution", paths: []string{"jira issue transition --resolution"}},
		{kind: "issuekey", paths: []string{"jira issue link create --to"}},
	} {
		for _, path := range tc.paths {
			if !slices.Contains(sites[tc.kind], path) {
				t.Errorf("missing %s at %s", tc.kind, path)
			}
		}
	}
	var walk func(string, []complete.SubSpec)
	walk = func(path string, subs []complete.SubSpec) {
		for _, sub := range subs {
			next := path + " " + sub.Name
			for _, spec := range sub.Specs {
				switch spec.LongFlag {
				case "label", "remove-label", "fields", "epic", "key":
					if spec.Dynamic != "" && !spec.CommaList {
						t.Errorf("CSV completion lost at %s --%s", next, spec.LongFlag)
					}
				case "project":
					if spec.Dynamic == "cacheproject" && spec.CommaList != slices.Contains([]string{"jira issue list", "jira issue mine", "jira jql build"}, next) {
						t.Errorf("project CSV/literal completion mismatches %s", next)
					}
				}
			}
			walk(next, sub.Subs)
		}
	}
	walk(gen.AppName, gen.Subs)
}
