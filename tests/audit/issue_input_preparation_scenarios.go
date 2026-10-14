//go:build outputaudit

package audit

func init() {
	const screen = `{"fields":{"customfield_1":{"key":"customfield_1","name":"Choice","required":false,"operations":["set"],"schema":{"type":"option","custom":"select"}},"customfield_2":{"key":"customfield_2","name":"Future","required":false,"operations":["set"],"schema":{"type":"future","custom":"fixture.vendor:select"}}}}`
	const payload = `{"fields":{"custom_fields":{"customfield_1":"123"},"customfield_2":{"futureCamel":{"nativeMember":"preserved native value","number":9007199254740993},"null":null,"empty":[]}}}`
	scenarios = append(scenarios,
		Scenario{ID: "issue-input-prepare-success", Op: "issue.input.prepare", Category: CategorySuccess, Args: []string{"issue", "input", "prepare", "--input=input.json", "--metadata=metadata.json"}, Files: map[string]string{"input.json": payload, "metadata.json": screen}},
		Scenario{ID: "issue-input-prepare-failure", Op: "issue.input.prepare", Category: CategoryFailure, Args: []string{"issue", "input", "prepare", "--input=input.json", "--metadata=metadata.json"}, Files: map[string]string{"input.json": `{"fields":{"customfield_1":42}}`, "metadata.json": screen}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "issue-input-prepare-help", Op: "issue.input.prepare", Category: CategoryHelp, Args: []string{"issue", "input", "prepare", "--help"}},
	)
	successDataShape["issue.input.prepare"] = struct{ required, optional []string }{required: []string{"payload", "fields", "diagnostics"}}
	stylingExpected["issue.input.prepare"] = true
	passthroughBoundary["issue.input.prepare"] = []string{"payload"}
	passthroughFidelity["issue.input.prepare"] = struct {
		path  []string
		value string
	}{path: []string{"payload", "fields", "customfield_2", "futureCamel", "nativeMember"}, value: "preserved native value"}
}

func checkPreparedIssueInput(data map[string]any) (bool, string) {
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		return false, "preparation lacks native payload"
	}
	fields, ok := payload["fields"].(map[string]any)
	if !ok || len(fields) != 2 {
		return false, "preparation dropped authored fields or retained its legacy wrapper"
	}
	choice, ok := fields["customfield_1"].(map[string]any)
	if !ok || choice["value"] != "123" || len(choice) != 1 {
		return false, "scalar selection lost its label meaning"
	}
	rows, ok := data["fields"].([]any)
	if !ok || len(rows) != 2 {
		return false, "preparation lacks ordered handling rows"
	}
	for i, want := range []struct{ field, handling string }{{"customfield_1", "encoded"}, {"customfield_2", "unverified"}} {
		row, ok := rows[i].(map[string]any)
		if !ok || row["field"] != want.field || row["handling"] != want.handling {
			return false, "preparation overclaimed unknown plugin validation or changed row order"
		}
	}
	diagnostics, ok := data["diagnostics"].([]any)
	if !ok || len(diagnostics) != 0 {
		return false, "preparation changed non-lossy fixture diagnostics"
	}
	return true, ""
}
