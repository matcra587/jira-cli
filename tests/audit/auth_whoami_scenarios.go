//go:build outputaudit

package audit

import (
	"encoding/json"
	"net/http"
)

func init() {
	passthroughBoundary["auth.whoami"] = []string{"user"}
	passthroughBoundary["me"] = []string{"user"}
	passthroughFidelity["auth.whoami"] = struct {
		path  []string
		value string
	}{path: []string{"user", "futureCamelField"}, value: "native identity extension" + attackPayload}
	passthroughFidelity["me"] = passthroughFidelity["auth.whoami"]
	stylingExpected["me"] = false
	successDataShape["me"] = successDataShape["auth.whoami"]
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
