//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
)

func init() {
	stubServers["issue-input"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			field := map[string]any{
				"fieldId": "customfield_14002", "key": "displayKey", "name": attackPayload,
				"schema":   map[string]any{"type": "plugin", "custom": "fixture:choice"},
				"required": true, "operations": []string{"set"}, "allowedValues": []any{map[string]any{"id": "7001"}},
				"defaultValue": nil, "futureCamelMeta": map[string]any{"customMember": "native input value"},
			}
			var body any
			switch r.URL.Path {
			case "/rest/api/3/project/KEY":
				body = map[string]any{"id": "10402", "key": "KEY", "future": nil}
			case "/rest/api/3/issue/createmeta/10402/issuetypes":
				body = map[string]any{"startAt": 0, "maxResults": 50, "total": 1, "issueTypes": []any{map[string]any{"id": "10044", "name": "Task"}}}
			case "/rest/api/3/issue/createmeta/10402/issuetypes/10044":
				body = map[string]any{"startAt": 0, "maxResults": 50, "total": 1, "fields": []any{field}}
			case "/rest/api/3/issue/KEY-1/editmeta", "/rest/api/3/issue/KEY-2/editmeta":
				body = map[string]any{"fields": map[string]any{"customfield_14002": field}, "future": nil}
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if err := json.NewEncoder(w).Encode(body); err != nil {
				panic(err)
			}
		})
	}
	for _, verb := range []string{"create", "edit"} {
		op := "issue." + verb + ".input"
		args := []string{"issue", verb, "--generate-json"}
		if verb == "create" {
			args = append(args, "--project=KEY", "--type=Task")
		} else {
			args = append(args, "KEY-1", "KEY-2", "--parallelism=1")
		}
		scenarios = append(scenarios,
			Scenario{ID: op + "-success", Op: op, Variant: "--generate-json", Category: CategorySuccess, Args: args, Stub: "issue-input", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken, "JIRA_READ_ONLY=true"}},
			Scenario{ID: op + "-failure", Op: op, Variant: "--generate-json", Category: CategoryFailure, Args: append(append([]string(nil), args...), "--summary="), ExpectExit: 3, ExpectCode: "flag_not_applicable"},
			Scenario{ID: op + "-help", Op: op, Variant: "--generate-json", Category: CategoryHelp, Args: []string{"issue", verb, "--generate-json", "--help"}},
		)
		stylingExpected[op] = true // native skeleton and field sections use the shared View
	}
	successDataShape["issue.create.input"] = struct{ required, optional []string }{
		required: []string{"complete", "project", "issue_type", "fields", "type_pages", "field_pages", "payload"},
		optional: []string{"issue_types"},
	}
	passthroughBoundary["issue.create.input"] = []string{"project", "issue_type", "issue_types", "fields", "type_pages", "field_pages", "payload"}
	passthroughFidelity["issue.create.input"] = struct {
		path  []string
		value string
	}{path: []string{"fields", "customfield_14002", "futureCamelMeta", "customMember"}, value: "native input value"}
	successDataShape["issue.edit.input"] = struct{ required, optional []string }{required: []string{"results"}, optional: []string{"selector"}}
	identityRefField["issue.edit.input"] = "results"
	keyedResultKeys["issue.edit.input"] = []string{"KEY-1", "KEY-2"}
	passthroughBoundary["issue.edit.input"] = []string{"results.data.metadata", "results.data.fields", "results.data.payload"}
	passthroughFidelity["issue.edit.input"] = struct {
		path  []string
		value string
	}{path: []string{"results", "0", "data", "fields", "customfield_14002", "futureCamelMeta", "customMember"}, value: "native input value"}
}

func checkIssueInputFields(data map[string]any, create bool) (bool, string) {
	if create {
		if data["complete"] != true {
			return false, "create input did not complete the native metadata walk"
		}
		for _, fact := range []struct{ name, id string }{{"project", "10402"}, {"issue_type", "10044"}} {
			identity, ok := data[fact.name].(map[string]any)
			if !ok || identity["id"] != fact.id {
				return false, "create input changed native project/type identity"
			}
		}
		return checkIssueInputContext(data, true)
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 2 {
		return false, "edit input dropped a requested issue context"
	}
	for _, raw := range results {
		row, ok := raw.(map[string]any)
		if !ok || row["ok"] != true {
			return false, "edit input changed a successful native context"
		}
		context, ok := row["data"].(map[string]any)
		if !ok || context["metadata"] == nil {
			return false, "edit input dropped complete native metadata"
		}
		if ok, detail := checkIssueInputContext(context, false); !ok {
			return false, detail
		}
	}
	return true, ""
}

func checkIssueInputContext(data map[string]any, create bool) (bool, string) {
	fields, ok := data["fields"].(map[string]any)
	if !ok || len(fields) != 1 {
		return false, "input discovery omitted actual native field definitions"
	}
	field, ok := fields["customfield_14002"].(map[string]any)
	if !ok || field["required"] != true || field["name"] != attackPayload {
		return false, "input discovery changed field identity or requiredness"
	}
	if value, exists := field["defaultValue"]; !exists || value != nil {
		return false, "input discovery omitted explicit null default"
	}
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		return false, "input discovery omitted its native skeleton"
	}
	values, ok := payload["fields"].(map[string]any)
	if !ok {
		return false, "native skeleton omitted fields object"
	}
	if value, exists := values["customfield_14002"]; !exists || value != nil {
		return false, "native skeleton guessed a plugin value instead of a placeholder"
	}
	want := 1
	if create {
		want = 3
		for _, fact := range []struct{ field, id string }{{"project", "10402"}, {"issuetype", "10044"}} {
			identity, ok := values[fact.field].(map[string]any)
			if !ok || identity["id"] != fact.id {
				return false, "native skeleton guessed a project/type identity"
			}
		}
	}
	if len(values) != want || payload["update"] != nil {
		return false, "native skeleton invented field IDs or operations"
	}
	return true, ""
}
