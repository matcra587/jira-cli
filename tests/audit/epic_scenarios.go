//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
	"strings"
)

func init() {
	stubServers["epics"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				JQL string `json:"jql"`
			}
			if r.Method != "POST" || r.URL.Path != "/rest/api/3/search/jql" || json.NewDecoder(r.Body).Decode(&request) != nil {
				w.WriteHeader(400)
				return
			}
			key, status := "KEY-1", "In Progress"
			if strings.HasPrefix(request.JQL, "parent = ") {
				key, status = "KEY-2", "Review"+attackPayload
			}
			if _, err := w.Write(mustMarshal(map[string]any{"isLast": true, "issues": []map[string]any{{"key": key, "fields": map[string]any{"customUnmodeledField": "epic custom value", "summary": "Epic" + attackPayload, "status": map[string]any{"name": status}}}}})); err != nil {
				return
			}
		})
	}
	for _, verb := range []string{"list", "board"} {
		op := "epic." + verb
		scenarios = append(scenarios,
			Scenario{ID: "epic-" + verb + "-success", Op: op, Category: CategorySuccess, Args: []string{"epic", verb}, Stub: "epics", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
			Scenario{ID: "epic-" + verb + "-failure", Op: op, Category: CategoryFailure, Args: []string{"epic", verb, "--limit=0"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
			Scenario{ID: "epic-" + verb + "-help", Op: op, Category: CategoryHelp, Args: []string{"epic", verb, "--help"}},
		)
		identityRefField[op] = "epics"
		passthroughBoundary[op] = []string{"epics.fields"}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: []string{"epics", "0", "fields", "customUnmodeledField"}, value: "epic custom value"}
		stylingExpected[op] = true
		required := []string{"epics", "jql", "pagination", "truncated"}
		if verb == "board" {
			required = append(required, "totals", "unknown_status", "counts_complete")
		}
		successDataShape[op] = struct{ required, optional []string }{required: required, optional: []string{"truncated_reason"}}
	}
}
