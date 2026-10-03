//go:build outputaudit

package audit

import (
	"net/http"
)

func init() {
	stubServers["users"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" {
				w.WriteHeader(404)
				return
			}
			if _, err := w.Write(mustMarshal([]map[string]any{{"accountId": "fixture-id" + attackPayload, "displayName": "Sam" + attackPayload, "emailAddress": "sam@example.com" + attackPayload, "active": true}})); err != nil {
				return
			}
		})
	}
	scenarios = append(scenarios,
		Scenario{ID: "user-search-success", Op: "user.search", Category: CategorySuccess, Args: []string{"user", "search", "sam"}, Stub: "users", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
		Scenario{ID: "user-search-failure", Op: "user.search", Category: CategoryFailure, Args: []string{"user", "search", "sam", "--limit=0"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "user-search-help", Op: "user.search", Category: CategoryHelp, Args: []string{"user", "search", "--help"}},
	)
	identityRefField["user.search"] = "users"
	stylingExpected["user.search"] = true
	successDataShape["user.search"] = struct{ required, optional []string }{required: []string{"query", "users", "count", "skipped", "pagination", "truncated"}, optional: []string{"truncated_reason"}}
}
