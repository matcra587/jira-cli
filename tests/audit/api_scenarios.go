//go:build outputaudit

package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func init() {
	stubServers["raw-api"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/rest/api/3/fixture" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"errorMessages":["raw fixture absent"]}`) //nolint:errcheck // a disconnected corpus reader ends the fixture response
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"unknownField": "raw custom value", "hostile": attackPayload}) //nolint:errcheck // a disconnected corpus reader ends the fixture response
		})
	}
	for _, method := range []string{"get", "head", "options", "post", "put", "patch", "delete"} {
		op := "api." + method
		mutation := method == "post" || method == "put" || method == "patch" || method == "delete"
		args := []string{"api", method, "/rest/api/3/fixture"}
		if mutation {
			args = append(args, "--force")
		}
		scenarios = append(scenarios,
			Scenario{ID: op + "-success", Op: op, Category: CategorySuccess, Args: args, Stub: "raw-api", Env: []string{"JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=corpus-token"}},
			Scenario{ID: op + "-failure", Op: op, Category: CategoryFailure, Args: []string{"api", method, "/rest/api/3/missing", "--input", "-"}, Stub: "raw-api", Env: []string{"JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=corpus-token"}, ExpectExit: 2, ExpectCode: "jira_not_found"},
			Scenario{ID: op + "-help", Op: op, Category: CategoryHelp, Args: []string{"api", method, "--help"}},
		)
		// A mutation needs acknowledgement before a server can refuse it.
		if mutation {
			scenarios[len(scenarios)-2].Args = append(scenarios[len(scenarios)-2].Args, "--force")
			scenarios = append(scenarios, Scenario{
				ID: op + "-preview", Op: op, Category: CategoryDryRun,
				Args: []string{"api", method, "/rest/api/3/fixture", "--input", "-", "--query=field=a,b", "--dry-run"}, Stdin: "raw request body",
			})
			dryRunPreview[op] = func(data map[string]any) (bool, string) {
				request, ok := data["request"].(map[string]any)
				if !ok || request["method"] != strings.ToUpper(method) || request["endpoint"] != "/rest/api/3/fixture" || request["body_base64"] != "cmF3IHJlcXVlc3QgYm9keQ==" {
					return false, "raw preview lost its exact method, endpoint or body"
				}
				if _, exists := data["response"]; exists {
					return false, "local raw preview fabricated an HTTP response"
				}
				return true, ""
			}
			dryRunTense[op] = struct {
				wantProspective string
				forbidCompleted []string
			}{"Would send " + strings.ToUpper(method), []string{"HTTP 200"}}
		}
		stylingExpected[op] = false
		if method != "head" {
			passthroughBoundary[op] = []string{"response.json"}
			passthroughFidelity[op] = struct {
				path  []string
				value string
			}{[]string{"response", "json", "unknownField"}, "raw custom value"}
		}
		required := []string{"request", "attempted", "response"}
		if mutation {
			required = append(required, "dry_run")
		}
		successDataShape[op] = struct{ required, optional []string }{required: required}
	}
}
