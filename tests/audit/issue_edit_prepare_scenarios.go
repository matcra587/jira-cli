//go:build outputaudit

package audit

import "net/http"

func init() {
	stubServers["issue-edit-prepare"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/KEY-1" || r.URL.Query().Get("fields") != "description,updated" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, err := w.Write([]byte(`{"id":"123456","key":"KEY-1","fields":{"updated":null,"description":{"type":"doc","version":1,"futureCamel":{"nativeMember":"native source value","large":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"review content"}]}]}}}`)); err != nil {
				panic(err)
			}
		})
	}
	scenarios = append(scenarios,
		Scenario{ID: "issue-edit-prepare-success", Op: "issue.edit.prepare", Category: CategorySuccess, Args: []string{"issue", "edit", "prepare", "KEY-1"}, Stub: "issue-edit-prepare", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken, "JIRA_READ_ONLY=true"}},
		Scenario{ID: "issue-edit-prepare-failure", Op: "issue.edit.prepare", Category: CategoryFailure, Args: []string{"issue", "edit", "prepare", "KEY-1", "--web"}, ExpectExit: 3, ExpectCode: "flag_not_applicable"},
		Scenario{ID: "issue-edit-prepare-help", Op: "issue.edit.prepare", Category: CategoryHelp, Args: []string{"issue", "edit", "prepare", "--help"}},
	)
	successDataShape["issue.edit.prepare"] = struct{ required, optional []string }{required: []string{"issue", "snapshot", "markdown", "regions"}}
	identityRefField["issue.edit.prepare"] = "issue"
	stylingExpected["issue.edit.prepare"] = true
	passthroughBoundary["issue.edit.prepare"] = []string{"snapshot.description"}
	passthroughFidelity["issue.edit.prepare"] = struct {
		path  []string
		value string
	}{path: []string{"snapshot", "description", "futureCamel", "nativeMember"}, value: "native source value"}
}
