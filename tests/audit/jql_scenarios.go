//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
)

func init() {
	stubServers["jql"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method + " " + r.URL.Path {
			case "POST /rest/api/3/jql/parse":
				var in struct {
					Queries []string `json:"queries"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Queries) != 2 {
					w.WriteHeader(400)
					return
				}
				queries := []map[string]any{{"query": in.Queries[0] + attackPayload, "errors": []string{}, "warnings": []string{"warning" + attackPayload}, "structure": map[string]any{"futureCamelField": "native syntax tree" + attackPayload}}, {"query": in.Queries[1], "errors": []string{"syntax error" + attackPayload}, "warnings": nil}}
				if _, err := w.Write(mustMarshal(map[string]any{"queries": queries, "futureCamelField": "native parser extension" + attackPayload})); err != nil {
					return
				}
			case "GET /rest/api/3/jql/autocompletedata":
				if _, err := w.Write(mustMarshal(map[string]any{"visibleFieldNames": []map[string]any{{"value": "custom" + attackPayload, "displayName": "Custom field" + attackPayload, "cfid": "cf[12345]" + attackPayload, "searchable": "false"}}, "visibleFunctionNames": []map[string]any{{"value": "currentUser()" + attackPayload, "displayName": "Current user" + attackPayload, "isList": "false"}}, "jqlReservedWords": []string{"and" + attackPayload}, "futureCamelField": "native reference extension" + attackPayload})); err != nil {
					return
				}
			default:
				w.WriteHeader(400)
			}
		})
	}
	auth := []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}
	scenarios = append(scenarios,
		Scenario{ID: "jql-build-success", Op: "jql.build", Category: CategorySuccess, Args: []string{"jql", "build", "--project=KEY", "--label=backend<&>"}},
		Scenario{ID: "jql-build-invalid-date", Op: "jql.build", Category: CategoryFailure, Args: []string{"jql", "build", "--updated=invalid"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "jql-build-help", Op: "jql.build", Category: CategoryHelp, Args: []string{"jql", "build", "--help"}},
		Scenario{ID: "jql-validate-success", Op: "jql.validate", Category: CategorySuccess, Args: []string{"jql", "validate", "project = KEY", "project = ("}, Stub: "jql", Env: auth},
		Scenario{ID: "jql-validate-invalid-mode", Op: "jql.validate", Category: CategoryFailure, Args: []string{"jql", "validate", "one", "--mode=invalid"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "jql-validate-help", Op: "jql.validate", Category: CategoryHelp, Args: []string{"jql", "validate", "--help"}},
		Scenario{ID: "jql-reference-success", Op: "jql.reference", Category: CategorySuccess, Args: []string{"jql", "reference"}, Stub: "jql", Env: auth},
		Scenario{ID: "jql-reference-invalid-argument", Op: "jql.reference", Category: CategoryFailure, Args: []string{"jql", "reference", "extra"}, ExpectExit: 3, ExpectCode: "arg_count_invalid"},
		Scenario{ID: "jql-reference-help", Op: "jql.reference", Category: CategoryHelp, Args: []string{"jql", "reference", "--help"}},
	)
	for _, verb := range []string{"validate", "reference"} {
		op := "jql." + verb
		boundary, value := "parsed", "native parser extension"+attackPayload
		if verb == "reference" {
			boundary, value = "reference", "native reference extension"+attackPayload
		}
		passthroughBoundary[op] = []string{boundary}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{[]string{boundary, "futureCamelField"}, value}
	}
	stylingExpected["jql.validate"] = false
	stylingExpected["jql.build"] = false
	successDataShape["jql.build"] = struct{ required, optional []string }{required: []string{"jql", "url", "precedence", "board_scope"}}
	stylingExpected["jql.reference"] = true
	successDataShape["jql.validate"] = struct{ required, optional []string }{required: []string{"mode", "submitted_queries", "queries", "parsed"}}
	successDataShape["jql.reference"] = struct{ required, optional []string }{required: []string{"fields", "functions", "reference"}, optional: []string{"reserved_words"}}
}
