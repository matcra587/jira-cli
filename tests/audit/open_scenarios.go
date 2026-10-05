//go:build outputaudit

package audit

func init() {
	scenarios = append(scenarios,
		Scenario{ID: "open-success", Op: "open", Category: CategorySuccess, Args: []string{"open", "KEY-1"}, Env: []string{"JIRA_BASE_URL=https://example.invalid", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "BROWSER=/does/not/exist"}},
		Scenario{ID: "open-many-targets", Op: "open", Category: CategoryFailure, Args: []string{"open", "KEY-1:2"}, ExpectExit: 3, ExpectCode: "arg_value_invalid"},
		Scenario{ID: "open-help", Op: "open", Category: CategoryHelp, Args: []string{"open", "--help"}},
	)
	identityRefField["open"] = "issue"
	stylingExpected["open"] = false
	successDataShape["open"] = struct{ required, optional []string }{required: []string{"issue", "web_url", "opened"}}
}
