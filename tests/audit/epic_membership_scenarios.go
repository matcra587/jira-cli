//go:build outputaudit

package audit

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

func init() {
	stubServers["epic-membership"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				if strings.HasSuffix(r.URL.Path, "KEY-100") {
					if _, err := w.Write([]byte(`{"fields":{"issuetype":{"name":"Epic","subtask":false,"hierarchyLevel":1}}}`)); err != nil {
						return
					}
				} else {
					if _, err := w.Write([]byte(`{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)); err != nil {
						return
					}
				}
				return
			}
			if r.Method == "PUT" {
				w.WriteHeader(204)
				return
			}
			w.WriteHeader(400)
		})
	}
	for _, verb := range []string{"add", "remove"} {
		op := "epic." + verb
		args := []string{"epic", verb, "KEY-1", "KEY-2"}
		if verb == "add" {
			args = append(args, "KEY-100")
		}
		scenarios = append(scenarios,
			Scenario{ID: "epic-" + verb + "-success", Op: op, Category: CategorySuccess, Args: args, Stub: "epic-membership", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
			Scenario{ID: "epic-" + verb + "-preview", Op: op, Category: CategoryDryRun, Args: append(append([]string{}, args...), "--dry-run")},
			Scenario{ID: "epic-" + verb + "-invalid", Op: op, Category: CategoryFailure, Args: []string{"epic", verb, "INVALID", "KEY-100", "--dry-run"}, ExpectExit: 3, ExpectCode: "arg_value_invalid"},
			Scenario{ID: "epic-" + verb + "-help", Op: op, Category: CategoryHelp, Args: []string{"epic", verb, "--help"}},
		)
		identityRefField[op] = "results"
		keyedResultKeys[op] = []string{"KEY-1", "KEY-2"}
		stylingExpected[op] = true
		prospective, completed := "Would remove ", "Removed "
		if verb == "add" {
			prospective, completed = "Would assign ", "Assigned "
		}
		dryRunTense[op] = struct {
			wantProspective string
			forbidCompleted []string
		}{prospective, []string{completed}}
		dryRunPreview[op] = func(data map[string]any) (bool, string) {
			var parent any
			if verb == "add" {
				parent = map[string]any{"key": "KEY-100"}
			}
			want := map[string]any{"fields": map[string]any{"parent": parent}}
			if !reflect.DeepEqual(data["payload"], want) {
				return false, "preview changed the fixed parent payload"
			}
			rows, ok := data["results"].([]any)
			if !ok || len(rows) != 2 {
				return false, "preview lost explicit child results"
			}
			for i, raw := range rows {
				row, ok := raw.(map[string]any)
				if !ok || row["ok"] != true {
					return false, "preview child did not succeed"
				}
				child, ok := row["data"].(map[string]any)
				if !ok || child["applied"] != false || child["remote_validated"] != false || child["verify_failed"] != false || child["parent"] != nil {
					return false, fmt.Sprintf("preview child %d invented a remote outcome", i)
				}
			}
			return true, ""
		}
		successDataShape[op] = struct{ required, optional []string }{
			required: []string{"dry_run", "notify", "payload", "results"}, optional: []string{"epic"},
		}
	}
}
