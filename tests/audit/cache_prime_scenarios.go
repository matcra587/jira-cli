//go:build outputaudit

package audit

import "net/http"

func init() {
	scenarios = append(scenarios,
		Scenario{ID: "cache.refresh-success", Op: "cache.refresh", Category: CategorySuccess, Args: []string{"cache", "refresh", "fields", "labels", "--parallelism=2"}, Stub: "cache-prime", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
		Scenario{ID: "cache.refresh-preview", Op: "cache.refresh", Category: CategoryDryRun, Args: []string{"cache", "refresh", "fields", "labels", "--dry-run"}, Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_BASE_URL=https://example.com"}},
		Scenario{ID: "cache.refresh-failure", Op: "cache.refresh", Category: CategoryFailure, Args: []string{"cache", "refresh", "--parallelism=17"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "cache.refresh-help", Op: "cache.refresh", Category: CategoryHelp, Args: []string{"cache", "refresh", "--help"}},
	)
	stylingExpected["cache.refresh"] = false
	successDataShape["cache.refresh"] = struct{ required, optional []string }{required: []string{"dry_run", "parallelism", "results"}}
	dryRunTense["cache.refresh"] = struct {
		wantProspective string
		forbidCompleted []string
	}{wantProspective: "would fetch", forbidCompleted: []string{"fetched  ", "reused  "}}
	dryRunPreview["cache.refresh"] = func(data map[string]any) (bool, string) {
		rows, ok := data["results"].([]any)
		if !ok || len(rows) != 2 || data["dry_run"] != true {
			return false, "metadata group preview lost selected resource plans"
		}
		for _, value := range rows {
			row, _ := value.(map[string]any)
			item, _ := row["data"].(map[string]any)
			if row["ok"] != true || item["dry_run"] != true || item["would_fetch"] != true || item["attempted"] != false || item["saved"] != false {
				return false, "metadata group preview fabricated collection or storage"
			}
		}
		return true, ""
	}
	passthroughBoundary["cache.refresh"] = []string{"results.data.items", "results.data.payload"}
	passthroughFidelity["cache.refresh"] = struct {
		path  []string
		value string
	}{path: []string{"results", "0", "data", "items", "0", "futureField"}, value: "native cache value"}
	stubServers["cache-prime"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var data any
			native := map[string]any{"id": "1", "name": "Example" + attackPayload, "futureField": "native cache value", "key": "KEY", "inward": "inside", "outward": "outside"}
			switch r.URL.Path {
			case "/rest/api/3/label":
				data = map[string]any{"startAt": 0, "maxResults": 100, "isLast": true, "values": []string{"native cache value"}}
			case "/rest/api/3/project/search", "/rest/api/3/priority/search", "/rest/api/3/resolution/search":
				data = map[string]any{"startAt": 0, "maxResults": 100, "isLast": true, "values": []any{native}}
			case "/rest/api/3/issueLinkType":
				data = map[string]any{"issueLinkTypes": []any{native}}
			case "/rest/api/3/field", "/rest/api/3/issuetype", "/rest/api/3/status":
				data = []any{native}
			case "/rest/api/3/search/jql":
				data = map[string]any{"isLast": true, "issues": []any{map[string]any{"id": "1", "key": "KEY-1", "fields": map[string]any{"summary": "Epic" + attackPayload, "futureField": "native cache value"}}}}
			case "/rest/agile/1.0/board":
				data = map[string]any{"startAt": 0, "maxResults": 50, "isLast": true, "values": []any{map[string]any{"id": 42, "name": "native cache value", "type": "scrum"}}}
			case "/rest/agile/1.0/board/42/project":
				data = map[string]any{"startAt": 0, "maxResults": 50, "isLast": true, "values": []any{map[string]any{"id": "1", "key": "KEY"}}}
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if _, err := w.Write(mustMarshal(data)); err != nil {
				return
			}
		})
	}
	for _, name := range []string{"labels", "projects", "epics", "fields", "issuetypes", "linktypes", "boards", "statuses", "priorities", "resolutions"} {
		op := "cache." + name
		scenarios = append(scenarios,
			Scenario{ID: op + "-success", Op: op, Category: CategorySuccess, Args: []string{"cache", name}, Stub: "cache-prime", Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}},
			Scenario{ID: op + "-preview", Op: op, Category: CategoryDryRun, Args: []string{"cache", name, "--dry-run"}, Env: []string{"JIRA_EMAIL=" + stubEmail, "JIRA_BASE_URL=https://example.com"}},
			Scenario{ID: op + "-failure", Op: op, Category: CategoryFailure, Args: []string{"cache", name, "--unexpected"}, ExpectExit: 3, ExpectCode: "flag_unknown"},
			Scenario{ID: op + "-help", Op: op, Category: CategoryHelp, Args: []string{"cache", name, "--help"}},
		)
		stylingExpected[op] = false
		successDataShape[op] = struct{ required, optional []string }{required: []string{"dry_run", "items", "count", "pages", "complete", "resource", "ttl_minutes", "force", "unbounded", "would_fetch", "attempted", "from_cache", "cache_state", "saved", "age_seconds"}, optional: []string{"payload", "truncated_reason", "next_start", "next_page_token", "written_at"}}
		dryRunTense[op] = struct {
			wantProspective string
			forbidCompleted []string
		}{wantProspective: "would fetch", forbidCompleted: []string{"fetched  ", "reused  "}}
		dryRunPreview[op] = func(data map[string]any) (bool, string) {
			items, ok := data["items"].([]any)
			return ok && len(items) == 0 && data["dry_run"] == true && data["would_fetch"] == true && data["attempted"] == false && data["saved"] == false, "cold metadata preview fabricated data, collection or storage"
		}
		passthroughBoundary[op] = []string{"items", "payload"}
		path := []string{"items", "0", "futureField"}
		switch name {
		case "labels":
			path = []string{"items", "0"}
		case "epics":
			path = []string{"items", "0", "fields", "futureField"}
		case "boards":
			path = []string{"payload", "boards", "0", "name"}
		}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: path, value: "native cache value"}
	}
}
