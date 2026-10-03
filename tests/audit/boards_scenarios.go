//go:build outputaudit

package audit

import (
	"net/http"
)

func init() {
	stubServers["boards"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(404)
				return
			}
			var data any
			switch r.URL.Path {
			case "/rest/agile/1.0/board":
				data = map[string]any{"isLast": true, "startAt": 0, "maxResults": 50, "values": []map[string]any{{"id": 42, "name": "Sprint" + attackPayload, "type": "scrum" + attackPayload}}}
			case "/rest/agile/1.0/board/42/project":
				data = map[string]any{"isLast": true, "startAt": 0, "maxResults": 50, "values": []map[string]any{{"id": "10042", "key": "KEY"}}}
			default:
				w.WriteHeader(404)
				return
			}
			if _, err := w.Write(mustMarshal(data)); err != nil {
				return
			}
		})
	}
	scenarios = append(scenarios,
		Scenario{ID: "boards-list-success", Op: "boards.list", Category: CategorySuccess, Args: []string{"boards", "list"}, Stub: "boards", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
		Scenario{ID: "boards-list-failure", Op: "boards.list", Category: CategoryFailure, Args: []string{"boards", "list", "--unexpected"}, ExpectExit: 3, ExpectCode: "flag_unknown"},
		Scenario{ID: "boards-list-help", Op: "boards.list", Category: CategoryHelp, Args: []string{"boards", "list", "--help"}},
	)
	identityRefField["boards.list"] = "boards"
	stylingExpected["boards.list"] = true
	successDataShape["boards.list"] = struct{ required, optional []string }{required: []string{"boards", "count", "pagination", "from_cache", "fetched_at", "cache_state", "cache_source_state", "cache_empty", "cache_saved", "pages_fetched", "truncated", "project_failures"}, optional: []string{"truncated_reason", "next_start", "retry_after_seconds"}}
}
