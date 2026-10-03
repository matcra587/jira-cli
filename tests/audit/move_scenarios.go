//go:build outputaudit

package audit

import (
	"net/http"
	"strings"
)

const auditMovePayload = `{"sendBulkNotification":false,"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1","KEY-2"],"inferFieldDefaults":false}},"futureCamelField":"native move extension"}`

func init() {
	stubServers["issue-move"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
				key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
				id := "101"
				if key == "KEY-2" {
					id = "102"
				}
				if _, err := w.Write(mustMarshal(map[string]any{"id": id, "key": key})); err != nil {
					return
				}
			case r.Method == "POST" && r.URL.Path == "/rest/api/3/bulk/issues/move":
				w.WriteHeader(201)
				if _, err := w.Write([]byte(`{"taskId":"100","futureCamelField":"native move receipt"}`)); err != nil {
					return
				}
			case r.Method == "GET" && r.URL.Path == "/rest/api/3/bulk/queue/100":
				if _, err := w.Write(mustMarshal(map[string]any{"taskId": "100", "status": "COMPLETE", "progressPercent": 100, "totalIssueCount": 2, "invalidOrInaccessibleIssueCount": 0, "processedAccessibleIssues": []int{101, 102}, "created": 1700000000123, "futureCamelField": "native move progress" + attackPayload})); err != nil {
					return
				}
			default:
				w.WriteHeader(400)
			}
		})
	}
	auth := []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}
	scenarios = append(scenarios,
		Scenario{ID: "issue-move-success", Op: "issue.move", Category: CategorySuccess, Args: []string{"issue", "move", "KEY-1", "KEY-2", "--json-input=-", "--force", "--wait"}, Stdin: auditMovePayload, Stub: "issue-move", Env: auth},
		Scenario{ID: "issue-move-preview", Op: "issue.move", Category: CategoryDryRun, Args: []string{"issue", "move", "KEY-1", "KEY-2", "--json-input=-", "--dry-run"}, Stdin: auditMovePayload},
		Scenario{ID: "issue-move-invalid", Op: "issue.move", Category: CategoryFailure, Args: []string{"issue", "move", "KEY-1", "--json-input=-", "--dry-run"}, Stdin: `{}`, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "issue-move-help", Op: "issue.move", Category: CategoryHelp, Args: []string{"issue", "move", "--help"}},
		Scenario{ID: "issue-move-status-success", Op: "issue.move.status", Category: CategorySuccess, Args: []string{"issue", "move", "status", "100"}, Stub: "issue-move", Env: auth},
		Scenario{ID: "issue-move-status-invalid", Op: "issue.move.status", Category: CategoryFailure, Args: []string{"issue", "move", "status", ".."}, ExpectExit: 3, ExpectCode: "arg_value_invalid"},
		Scenario{ID: "issue-move-status-help", Op: "issue.move.status", Category: CategoryHelp, Args: []string{"issue", "move", "status", "--help"}},
	)
	identityRefField["issue.move"] = "results"
	identityRefField["issue.move.status"] = "task"
	keyedResultKeys["issue.move"] = []string{"KEY-1", "KEY-2"}
	passthroughBoundary["issue.move"] = []string{"payload", "receipt", "progress"}
	passthroughBoundary["issue.move.status"] = []string{"progress"}
	for _, op := range []string{"issue.move", "issue.move.status"} {
		stylingExpected[op] = true
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: []string{"progress", "futureCamelField"}, value: "native move progress" + attackPayload}
	}
	dryRunTense["issue.move"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"Would move ", []string{"Jira reports the move processed", "move job accepted"}}
	dryRunPreview["issue.move"] = func(data map[string]any) (bool, string) {
		if data["attempted"] != false || data["acknowledged"] != false || data["remote_validated"] != false || data["bindings_validated"] != true || data["task"] != nil || data["progress"] != nil || data["polls"] != float64(0) || data["terminal"] != false {
			return false, "local move preview invented a remote fact"
		}
		rows, ok := data["results"].([]any)
		if !ok || len(rows) != 2 {
			return false, "move preview lost ordered targets"
		}
		for _, row := range rows {
			child := row.(map[string]any)["data"].(map[string]any)
			if child["applied"] != false || child["outcome"] != "planned" || child["destination"] != "DEST,10001" {
				return false, "move preview invented application or lost destination"
			}
		}
		return true, ""
	}
	successDataShape["issue.move"] = struct{ required, optional []string }{
		required: []string{"dry_run", "payload", "targets", "bindings_validated", "remote_validated", "attempted", "acknowledged", "polls", "terminal", "poll_limit_reached", "results", "task", "receipt", "progress"}, optional: []string{"receipt_error"},
	}
	successDataShape["issue.move.status"] = struct{ required, optional []string }{required: []string{"task", "progress", "polls", "terminal", "poll_limit_reached"}}
}
