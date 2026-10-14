package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestUserSearchAmbiguousNativeRowsRefuseDiscoveryAndAccountWrites(t *testing.T) {
	t.Parallel()
	rows := []string{`{"accountId":"other","accountId":"literal","active":true}`, `{"accountId":"literal","active":false,"active":true}`, `{"accountId":"literal","active":true,"deleted":true,"deleted":false}`, `{"ACCOUNTID":"literal","active":true}`, `{"accountId":"literal","Active":true}`, `{"accountId":"literal","active":true,"Deleted":true}`, `{"accountId":"other","account\u0049d":"literal","active":true}`}
	for _, operation := range []string{"search", "watcher add", "watcher remove", "create", "edit", "clone"} {
		for _, row := range rows {
			t.Run(operation+"/"+row, func(t *testing.T) {
				t.Parallel()
				var searches, other atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user/search" {
						searches.Add(1)
						if r.URL.Query().Get("startAt") == "0" {
							_, _ = fmt.Fprint(w, "["+row+"]")
						} else {
							_, _ = fmt.Fprint(w, "[]")
						}
						return
					}
					other.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				defer server.Close()
				var args []string
				switch operation {
				case "search":
					args = []string{"user", "search", "fixture@example.com"}
				case "watcher add":
					args = []string{"issue", "watchers", "add", "KEY-1", "--user-query=fixture@example.com"}
				case "watcher remove":
					args = []string{"issue", "watchers", "remove", "KEY-1", "--user-query=fixture@example.com"}
				case "create":
					args = []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=fixture", "--assignee=fixture@example.com"}
				case "edit":
					args = []string{"issue", "edit", "KEY-1", "--summary=fixture", "--assignee=fixture@example.com"}
				case "clone":
					args = []string{"issue", "clone", "KEY-1", "--assignee=fixture@example.com"}
				}
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, "--output=json", "--no-input")...)
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if exit == 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "user_search_response_invalid" || searches.Load() != 1 || other.Load() != 0 {
					t.Fatalf("ambiguous native search admitted: operation=%s exit=%d searches=%d other=%d stdout=%s", operation, exit, searches.Load(), other.Load(), stdout)
				}
			})
		}
	}
}
