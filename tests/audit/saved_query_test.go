//go:build outputaudit

package audit

import (
	"encoding/json"
	"testing"
)

func TestSavedQueriesRetainSourceAlongsideExistingAuditStimuli(t *testing.T) {
	captures, err := corpus()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"issue.list", "issue.mine"} {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			t.Run(op+"/"+string(mode), func(t *testing.T) {
				capture, ok := captureFor(captures, op, "", CategorySuccess, mode)
				if !ok || capture.ExitCode != 0 || capture.Stderr != "" {
					t.Fatal("saved-query success capture missing or unsuccessful")
				}
				var data map[string]json.RawMessage
				if json.Unmarshal([]byte(capture.Stdout), &data) != nil {
					t.Fatal("saved-query capture is not JSON")
				}
				if mode == ModeJSON && json.Unmarshal(data["data"], &data) != nil {
					t.Fatal("saved-query success frame lost its data")
				}
				var source struct{ Key, Name, Description, Project, JQL string }
				var issues, pages []json.RawMessage
				if json.Unmarshal(data["saved_query"], &source) != nil || source.Key != "audit" || source.Name != "Saved audit query" || source.Description != "Native paging comparison" || source.Project != "descriptive-only" || source.JQL == "" || json.Unmarshal(data["issues"], &issues) != nil || len(issues) != 2 || json.Unmarshal(data["search_pages"], &pages) != nil || len(pages) != 2 {
					t.Fatal("saved-query audit lost source metadata or the existing native two-page/detail stimulus")
				}
			})
		}
	}
}
