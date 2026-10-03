//go:build outputaudit

package audit

func init() {
	// The endpoint, wire boundary and acknowledgement shape are the edit
	// contract. The independent recipes exercise the field route's own gate.
	scenarios = append(scenarios,
		Scenario{
			ID: "issue-move-fields-success", Op: "issue.move.fields", Category: CategorySuccess,
			Args: []string{"issue", "move", "fields", "KEY-2", "KEY-3", "--json-input=payload.json", "--force"}, Files: map[string]string{"payload.json": corpusEditPayload},
			Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}, Stub: "issue-edit",
		},
		Scenario{
			ID: "issue-move-fields-preview", Op: "issue.move.fields", Category: CategoryDryRun,
			Args: []string{"issue", "move", "fields", "KEY-2", "KEY-3", "--json-input=payload.json", "--dry-run"}, Files: map[string]string{"payload.json": corpusEditPayload},
		},
		Scenario{
			ID: "issue-move-fields-force", Op: "issue.move.fields", Category: CategoryFailure,
			Args: []string{"issue", "move", "fields", "KEY-2", "--json-input=payload.json", "--no-input"}, Files: map[string]string{"payload.json": corpusEditPayload}, ExpectExit: 3, ExpectCode: "flag_required",
		},
		Scenario{ID: "issue-move-fields-help", Op: "issue.move.fields", Category: CategoryHelp, Args: []string{"issue", "move", "fields", "--help"}},
	)
	identityRefField["issue.move.fields"] = "results"
	keyedResultKeys["issue.move.fields"] = []string{"KEY-2", "KEY-3"}
	passthroughBoundary["issue.move.fields"] = []string{"payload"}
	passthroughFidelity["issue.move.fields"] = passthroughFidelity["issue.edit"]
	dryRunPreview["issue.move.fields"] = dryRunPreview["issue.edit"]
	dryRunTense["issue.move.fields"] = dryRunTense["issue.edit"]
	stylingExpected["issue.move.fields"] = stylingExpected["issue.edit"]
	successDataShape["issue.move.fields"] = successDataShape["issue.edit"]
}
