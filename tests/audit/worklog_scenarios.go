//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
)

func init() {
	stubServers["worklogs"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/rest/api/3/issue/KEY-1/worklog" && r.URL.Path != "/rest/api/3/issue/KEY-2/worklog" {
				w.WriteHeader(404)
				return
			}
			record := map[string]any{"id": "100", "timeSpentSeconds": 60, "started": "2026-01-02T12:30:00.000+0000", "author": map[string]any{"displayName": "Worklog Author" + attackPayload}, "customUnmodeledField": "worklog custom value"}
			switch r.Method {
			case "GET":
				if _, err := w.Write(mustMarshal(map[string]any{"startAt": 0, "maxResults": 50, "total": 1, "worklogs": []any{record}})); err != nil {
					return
				}
			case "POST":
				var request map[string]any
				if json.NewDecoder(r.Body).Decode(&request) != nil || request["timeSpentSeconds"] != float64(60) || request["started"] != "2026-01-02T12:30:00.000+0000" {
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(201)
				if _, err := w.Write(mustMarshal(record)); err != nil {
					return
				}
			default:
				w.WriteHeader(400)
			}
		})
	}
	for _, verb := range []string{"list", "add"} {
		op := "worklog." + verb
		args := []string{"worklog", verb, "KEY-1", "KEY-2"}
		if verb == "add" {
			args = append(args, "--time-spent=1m", "--started=2026-01-02T12:30:00Z")
		}
		scenarios = append(scenarios,
			Scenario{ID: "worklog-" + verb + "-success", Op: op, Category: CategorySuccess, Args: args, Stub: "worklogs", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
			Scenario{ID: "worklog-" + verb + "-invalid", Op: op, Category: CategoryFailure, Args: []string{"worklog", verb, "INVALID"}, ExpectExit: 3, ExpectCode: "arg_value_invalid"},
			Scenario{ID: "worklog-" + verb + "-help", Op: op, Category: CategoryHelp, Args: []string{"worklog", verb, "--help"}},
		)
		identityRefField[op] = "results"
		keyedResultKeys[op] = []string{"KEY-1", "KEY-2"}
		stylingExpected[op] = true
		boundary := "results.data.worklogs.fields"
		path := []string{"results", "0", "data", "worklogs", "0", "fields", "customUnmodeledField"}
		if verb == "add" {
			boundary = "results.data.worklog.fields"
			path = []string{"results", "0", "data", "worklog", "fields", "customUnmodeledField"}
		}
		passthroughBoundary[op] = []string{boundary}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: path, value: "worklog custom value"}
		required := []string{"results"}
		if verb == "add" {
			passthroughBoundary[op] = append(passthroughBoundary[op], "payload")
			required = append(required, "dry_run", "payload", "notify", "workday_seconds")
			scenarios = append(scenarios, Scenario{ID: "worklog-add-preview", Op: op, Category: CategoryDryRun, Args: append(append([]string{}, args...), "--dry-run")})
			dryRunTense[op] = struct {
				wantProspective string
				forbidCompleted []string
			}{"Would add a worklog", []string{"worklog 100 created"}}
			dryRunPreview[op] = func(data map[string]any) (bool, string) {
				if !reflect.DeepEqual(data["payload"], map[string]any{"timeSpentSeconds": float64(60), "started": "2026-01-02T12:30:00.000+0000"}) {
					return false, "preview changed effort or start"
				}
				rows, ok := data["results"].([]any)
				if !ok || len(rows) != 2 {
					return false, "preview lost explicit ordered targets"
				}
				for i, raw := range rows {
					row, ok := raw.(map[string]any)
					if !ok || row["ok"] != true {
						return false, fmt.Sprintf("preview target %d lacks success", i)
					}
					child, ok := row["data"].(map[string]any)
					if !ok || child["applied"] != false || child["worklog"] != nil || child["receipt_error"] != nil {
						return false, "preview invented server state"
					}
				}
				return true, ""
			}
		}
		var optional []string
		if verb == "add" {
			optional = []string{"body_route"}
		}
		successDataShape[op] = struct{ required, optional []string }{required: required, optional: optional}
	}
}
