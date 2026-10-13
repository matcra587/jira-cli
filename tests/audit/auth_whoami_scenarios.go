//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
)

func init() {
	passthroughBoundary["auth.whoami"] = []string{"user"}
	passthroughBoundary["auth.whoami.save"] = []string{"user"}
	passthroughBoundary["me"] = []string{"user"}
	passthroughFidelity["auth.whoami"] = struct {
		path  []string
		value string
	}{path: []string{"user", "futureCamelField"}, value: "native identity extension" + attackPayload}
	passthroughFidelity["me"] = passthroughFidelity["auth.whoami"]
	passthroughFidelity["auth.whoami.save"] = passthroughFidelity["auth.whoami"]
	stylingExpected["me"] = false
	stylingExpected["auth.whoami.save"] = false
	dryRunTense["auth.whoami.save"] = struct {
		wantProspective string
		forbidCompleted []string
	}{
		wantProspective: "would save verified cached identity",
		forbidCompleted: []string{"saved verified identity", "stored verified identity"},
	}
	successDataShape["me"] = successDataShape["auth.whoami"]
	successDataShape["auth.whoami.save"] = struct{ required, optional []string }{required: []string{"dry_run", "base_url", "user", "scope", "settings"}}
	dryRunPreview["auth.whoami.save"] = func(data map[string]any) (bool, string) {
		user, ok := data["user"].(map[string]any)
		if !ok || user["accountId"] != "native-id"+attackPayload {
			return false, "identity preview lacks independently verified native ID"
		}
		scope, ok := data["scope"].(map[string]any)
		if !ok || scope["base_url"] != data["base_url"] || scope["email"] != stubEmail || scope["cloud_id"] != "" {
			return false, "identity preview has incomplete public binding"
		}
		settings, ok := data["settings"].(map[string]any)
		if !ok || settings["would_change"] != true || settings["saved"] != false || settings["changed"] != false || settings["created"] != false || settings["status"] != "prepared" {
			return false, "identity preview fabricated publication or lost candidate"
		}
		if path, ok := settings["path"].(string); !ok || path == "" {
			return false, "identity preview names no target"
		}
		return true, ""
	}
	identityEnv := []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken}
	scenarios = append(scenarios, []Scenario{
		{ID: "auth-whoami-save-success", Op: "auth.whoami.save", Variant: "--save", Category: CategorySuccess, Args: []string{"auth", "whoami", "--save", "--config=settings.toml"}, Files: map[string]string{"settings.toml": "theme='dark'\n"}, Env: identityEnv, Stub: "auth-identity", ExpectExit: 0},
		{ID: "auth-whoami-save-preview", Op: "auth.whoami.save", Variant: "--save", Category: CategoryDryRun, Args: []string{"auth", "whoami", "--save", "--dry-run", "--config=settings.toml"}, Files: map[string]string{"settings.toml": "theme='dark'\n"}, Env: identityEnv, Stub: "auth-identity", ExpectExit: 0},
		{ID: "auth-whoami-save-unauthenticated", Op: "auth.whoami.save", Variant: "--save", Category: CategoryFailure, Args: []string{"auth", "whoami", "--save"}, ExpectExit: 1, ExpectCode: "auth_missing"},
		{ID: "auth-whoami-save-help", Op: "auth.whoami.save", Variant: "--save", Category: CategoryHelp, Args: []string{"auth", "whoami", "--save", "--help"}, ExpectExit: 0},
	}...)
	stubServers["auth-identity"] = func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, token, ok := r.BasicAuth()
			if !ok || user != stubEmail || token != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // a disconnected corpus reader ends the fixture response
				"accountId": "native-id" + attackPayload, "displayName": "Native Account" + attackPayload,
				"emailAddress": "native@example.invalid" + attackPayload, "accountType": "atlassian" + attackPayload,
				"timeZone": "UTC" + attackPayload, "active": false, "futureCamelField": "native identity extension" + attackPayload,
			})
		})
	}
}
