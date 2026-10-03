package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestBinarySearchRejectsMalformedPagesAndRetainsValidatedPrefix(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		for _, tc := range []struct {
			name, body string
			late       bool
		}{
			{"missing completion", `{"issues":[]}`, false},
			{"null completion", `{"isLast":null,"issues":[]}`, false},
			{"missing issues", `{"isLast":true}`, false},
			{"null issues", `{"isLast":true,"issues":null}`, false},
			{"missing key", `{"isLast":true,"issues":[{}]}`, false},
			{"duplicates", `{"isLast":true,"issues":[{"key":"KEY-1"},{"key":"KEY-1"}]}`, false},
			{"oversized", `{"isLast":true,"issues":[{"key":"KEY-1"},{"key":"KEY-2"},{"key":"KEY-3"},{"key":"KEY-4"}]}`, false},
			{"trailing", `{"isLast":true,"issues":[]} {}`, false},
			{"late missing array", `{"isLast":true}`, true},
			{"late repeated key", `{"isLast":true,"issues":[{"key":"KEY-2"},{"key":"KEY-1"}]}`, true},
		} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						w.WriteHeader(500)
						return
					}
					if calls.Add(1) == 1 && tc.late {
						_, _ = fmt.Fprint(w, `{"isLast":false,"nextPageToken":"next","issues":[{"key":"KEY-1","fields":{"summary":"Retained"}}]}`)
						return
					}
					_, _ = fmt.Fprint(w, tc.body)
				}))
				t.Cleanup(server.Close)
				stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "list", "--jql=project = KEY", "--limit=3", "--output="+mode, "--jq=.issues[].key")
				var f frame
				if json.Unmarshal([]byte(stdout), &f) != nil || exit != cli.ExitServer || f.OK || len(f.Errors) != 1 || f.Meta.ExitCode == nil || *f.Meta.ExitCode != exit {
					t.Fatalf("bad failure envelope: %d %s", exit, stdout)
				}
				var failure struct {
					Code string `json:"code"`
				}
				if json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != cli.CodeSearchResponseInvalid {
					t.Fatalf("wrong upstream taxonomy: %s", stdout)
				}
				wantCalls := int32(1)
				if tc.late {
					wantCalls = 2
					var out envelope.IssueListOutput
					if json.Unmarshal(f.Data, &out) != nil || len(out.Issues) != 1 || out.Issues[0].Key != "KEY-1" || out.Pagination.Returned != 1 || out.Pagination.IsLast || out.Pagination.NextPageToken != "next" {
						t.Fatalf("validated prefix discarded or polluted: %s", stdout)
					}
				} else if len(f.Data) != 0 && string(f.Data) != "null" {
					t.Fatalf("first failed page invented data: %s", stdout)
				}
				if calls.Load() != wantCalls {
					t.Fatalf("refusal retried or continued: %d calls", calls.Load())
				}
			})
		}
	}
}
