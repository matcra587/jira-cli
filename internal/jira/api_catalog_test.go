package jira_test

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestAPIOperationsSelectMethodsAndSearchText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, method, query, path string }{
		{name: "platform path", method: "get", query: "/rest/api/3/myself", path: "/rest/api/3/myself"},
		{name: "operation id", method: "GET", query: "getCurrentUser", path: "/rest/api/3/myself"},
		{name: "summary", method: "GET", query: "GET CURRENT USER", path: "/rest/api/3/myself"},
		{name: "agile path", method: "GET", query: "/rest/agile/1.0/board", path: "/rest/agile/1.0/board"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, value := range jira.APIOperations(tc.method, tc.query) {
				if value.Method != "GET" {
					t.Fatal("returned a different method", value)
				}
				found = found || value.Path == tc.path
			}
			if !found {
				t.Fatal("missing independently expected operation", tc.path)
			}
		})
	}
	if got := jira.APIOperations("", "no-such-catalog-operation"); got == nil || len(got) != 0 {
		t.Fatal("empty result must be an explicit empty collection", got)
	}
}

func TestAPICatalogSelectionsCannotMutateBundledState(t *testing.T) {
	t.Parallel()
	all := jira.APIOperations("", "")
	if len(all) == 0 {
		t.Fatal("empty bundled catalog")
	}
	for i, value := range all {
		if i > 0 && (all[i-1].Path > value.Path || (all[i-1].Path == value.Path && all[i-1].Method > value.Method)) {
			t.Fatal("catalog is not sorted by path and method")
		}
		if !strings.HasPrefix(value.Path, "/rest/api/") && !strings.HasPrefix(value.Path, "/rest/agile/") {
			t.Fatal("catalog advertises another REST family", value)
		}
	}
	first := all[0]
	all[0].Path = "changed"
	if jira.APIOperations("", "")[0] != first {
		t.Fatal("selection modified the bundled operation")
	}
	sources := jira.APICatalogSources()
	if len(sources) != 2 || len(sources[0].SHA256) != 64 || sources[0].Version == "" {
		t.Fatal("missing pinned source provenance")
	}
	sources[0].Version = "changed"
	if jira.APICatalogSources()[0].Version == "changed" {
		t.Fatal("caller modified bundled provenance")
	}
}
