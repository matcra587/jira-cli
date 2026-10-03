//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
	"reflect"
)

const corpusTransitionPayload = `{"transition":{"name":"Done"},"fields":{},"update":{},"historyMetadata":{"extraData":{"sentinel":"native transition value"}},"futureCamelField":{"raw":"<>&\u2028","nullable":null,"large":9007199254740993},"comment":` + corpusCommentBody + `}`

// checkTransitionNativePayload pins the authored request and the convenience
// comment route independently of the generated output schema.
func checkTransitionNativePayload(raw any, targetMember, targetValue string, atomic bool) (bool, string) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return false, "native transition payload is absent or not an object"
	}
	target, ok := payload["transition"].(map[string]any)
	if !ok || target[targetMember] != targetValue {
		return false, "native transition payload lost its locally composed or per-key resolved target"
	}
	fields, ok := payload["fields"].(map[string]any)
	if !ok || len(fields) != 0 {
		return false, "native transition payload lost explicit empty fields"
	}
	update, ok := payload["update"].(map[string]any)
	if !ok {
		return false, "native transition payload lost its update container"
	}
	if atomic {
		comments, ok := update["comment"].([]any)
		if !ok || len(comments) != 1 {
			return false, "native transition payload lost the atomic convenience comment"
		}
		comment, ok := comments[0].(map[string]any)
		if !ok {
			return false, "native atomic comment is not an operation object"
		}
		add, ok := comment["add"].(map[string]any)
		var expected map[string]any
		if !ok || json.Unmarshal([]byte(corpusCommentBody), &expected) != nil || !reflect.DeepEqual(add["body"], expected) {
			return false, "native atomic comment changed its authored ADF body"
		}
	} else if len(update) != 0 {
		return false, "native screenless payload sent a convenience comment or lost its empty update container"
	}
	history, ok := payload["historyMetadata"].(map[string]any)
	if !ok {
		return false, "native transition payload lost historyMetadata"
	}
	extra, ok := history["extraData"].(map[string]any)
	if !ok || extra["sentinel"] != "native transition value" {
		return false, "native transition payload changed authored history metadata"
	}
	future, ok := payload["futureCamelField"].(map[string]any)
	if !ok || future["raw"] != "<>&\u2028" {
		return false, "native transition payload lost its extension or changed string content"
	}
	if value, present := future["nullable"]; !present || value != nil {
		return false, "native transition payload lost explicit extension null"
	}
	// The audit decoder uses float64. Check the semantic value here; exact
	// integer and raw string spelling are covered by binary RawMessage guards.
	if value, present := future["large"].(float64); !present || value != float64(9007199254740993) {
		return false, "native transition payload changed numeric extension"
	}
	return true, ""
}

func init() {
	passthroughBoundary["issue.transition"] = []string{"payload", "results.data.payload"}
	passthroughFidelity["issue.transition"] = struct {
		path  []string
		value string
	}{path: []string{"payload", "historyMetadata", "extraData", "sentinel"}, value: "native transition value"}
	stubServers["transition-discovery"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Query().Get("expand") != "transitions.fields" || len(r.URL.Query()) != 1 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch r.URL.Path {
			case "/rest/api/3/issue/KEY-1/transitions", "/rest/api/3/issue/KEY-2/transitions":
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			body := map[string]any{"expand": "transitions.fields", "futureCamelMeta": map[string]any{"sentinel": "workflow native value"}, "transitions": []any{
				map[string]any{"id": "31", "name": "Finish" + attackPayload, "to": map[string]any{"id": "42", "name": "Done" + attackPayload, "statusCategory": map[string]any{"key": "done"}}, "hasScreen": true, "fields": map[string]any{"resolution": map[string]any{"required": true, "name": "Resolution" + attackPayload, "schema": map[string]any{"type": "resolution"}, "allowedValues": []any{map[string]any{"id": "7", "name": "Fixed" + attackPayload}}, "defaultValue": nil}}},
				map[string]any{"id": "11", "name": "Restart" + attackPayload, "to": map[string]any{"id": "40", "name": "To Do" + attackPayload}, "hasScreen": false, "fields": map[string]any{}},
			}}
			if _, err := w.Write(mustMarshal(body)); err != nil {
				return
			}
		})
	}
	scenarios = append(scenarios,
		Scenario{ID: "transition-list-success", Op: "issue.transition.list", Category: CategorySuccess, Args: []string{"issue", "transition", "list", "KEY-1", "KEY-2", "--parallelism=1"}, Stub: "transition-discovery", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken, "JIRA_READ_ONLY=true"}},
		Scenario{ID: "transition-list-failure", Op: "issue.transition.list", Category: CategoryFailure, Args: []string{"issue", "transition", "list", "KEY-1", "--unexpected"}, ExpectExit: 3, ExpectCode: "flag_unknown"},
		Scenario{ID: "transition-list-help", Op: "issue.transition.list", Category: CategoryHelp, Args: []string{"issue", "transition", "list", "--help"}},
	)
	identityRefField["issue.transition.list"] = "results"
	keyedResultKeys["issue.transition.list"] = []string{"KEY-1", "KEY-2"}
	stylingExpected["issue.transition.list"] = true
	successDataShape["issue.transition.list"] = struct{ required, optional []string }{required: []string{"results"}}
	passthroughBoundary["issue.transition.list"] = []string{"results.data"}
	passthroughFidelity["issue.transition.list"] = struct {
		path  []string
		value string
	}{path: []string{"results", "0", "data", "futureCamelMeta", "sentinel"}, value: "workflow native value"}
}

// checkTransitionDiscoveryFields independently pins the native screen metadata
// supplied by the recipe, without consulting the generated output schema.
func checkTransitionDiscoveryFields(data map[string]any) (bool, string) {
	results, ok := data["results"].([]any)
	if !ok || len(results) != 2 {
		return false, "workflow discovery lost requested results"
	}
	for _, raw := range results {
		row, ok := raw.(map[string]any)
		if !ok || row["ok"] != true {
			return false, "workflow discovery lacks a successful keyed read"
		}
		child, ok := row["data"].(map[string]any)
		if !ok || child["expand"] != "transitions.fields" {
			return false, "workflow discovery lost native expansion metadata"
		}
		transitions, ok := child["transitions"].([]any)
		if !ok || len(transitions) != 2 {
			return false, "workflow discovery lost native transitions"
		}
		for index, raw := range transitions {
			transition, ok := raw.(map[string]any)
			if !ok || transition["id"] != []string{"31", "11"}[index] || transition["hasScreen"] != (index == 0) {
				return false, "workflow discovery changed identity or screen state"
			}
			fields, ok := transition["fields"].(map[string]any)
			if !ok {
				return false, "workflow discovery omitted native field metadata"
			}
			if index == 1 {
				if len(fields) != 0 {
					return false, "workflow discovery invented fields"
				}
				continue
			}
			resolution, ok := fields["resolution"].(map[string]any)
			if !ok || resolution["required"] != true {
				return false, "workflow discovery lost required field state"
			}
			schema, ok := resolution["schema"].(map[string]any)
			if !ok || schema["type"] != "resolution" {
				return false, "workflow discovery lost native field schema"
			}
			values, ok := resolution["allowedValues"].([]any)
			if !ok || len(values) != 1 {
				return false, "workflow discovery lost allowed values"
			}
			value, ok := values[0].(map[string]any)
			if !ok || value["id"] != "7" {
				return false, "workflow discovery changed allowed value identity"
			}
			if value, present := resolution["defaultValue"]; !present || value != nil {
				return false, "workflow discovery lost explicit null default"
			}
		}
	}
	return true, ""
}
