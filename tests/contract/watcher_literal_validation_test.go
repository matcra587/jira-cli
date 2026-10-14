package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestWatcherLiteralRemoteValidationReadsExactAccountOnceBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", verb, preview), func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				const id = "opaque &?+é@account"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user" {
						reads.Add(1)
						if len(r.URL.Query()) != 1 || r.URL.Query().Get("accountId") != id {
							t.Error("lookup did not preserve exact literal account")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"accountId": id, "active": true, "emailAddress": nil, "future": map[string]any{"zero": 0}})
						return
					}
					writes.Add(1)
					if reads.Load() != 1 || !strings.HasSuffix(r.URL.Path, "/watchers") {
						t.Error("write entered before one exact-user lookup")
					}
					if verb == "add" {
						var body string
						if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || body != id {
							t.Error("watcher write changed validated account")
						}
					} else if r.Method != http.MethodDelete || r.URL.Query().Get("accountId") != id {
						t.Error("watcher removal changed validated account")
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer server.Close()
				args := []string{"issue", "watchers", verb, "KEY-1", "KEY-2", "--user=" + id, "--validate-remote", "--no-readback", "--output=json", "--no-input"}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame struct {
					OK   bool `json:"ok"`
					Data struct {
						AccountID string                                                  `json:"account_id"`
						Results   []envelope.KeyedResult[envelope.IssueWatchersWriteData] `json:"results"`
					} `json:"data"`
				}
				wantWrites := int32(2)
				if preview {
					wantWrites = 0
				}
				if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.AccountID != id || len(frame.Data.Results) != 2 || reads.Load() != 1 || writes.Load() != wantWrites {
					t.Fatalf("literal preflight missing: exit=%d reads=%d writes=%d stdout=%s stderr=%s", exit, reads.Load(), writes.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestWatcherLiteralRemoteValidationRefusesUnusableAccountBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, body := range []string{`{"accountId":"literal","active":false}`, `{"accountId":"literal","active":true,"deleted":true}`, `{"accountId":"wrong","active":true}`, `{"accountId":"literal"}`, `{"accountId":null,"active":true}`, `{"accountId":"literal","active":true} {}`} {
			t.Run(verb+"/"+body, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user" {
						reads.Add(1)
						_, _ = fmt.Fprint(w, body)
						return
					}
					writes.Add(1)
					w.WriteHeader(204)
				}))
				defer server.Close()
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user=literal", "--validate-remote", "--no-readback", "--output=json", "--no-input")
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				want := "user_lookup_response_invalid"
				if strings.Contains(body, `"active":false`) || strings.Contains(body, `"deleted":true`) {
					want = "user_not_found"
				}
				if exit == 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != want || reads.Load() != 1 || writes.Load() != 0 {
					t.Fatalf("unusable literal admitted: exit=%d reads=%d writes=%d stdout=%s", exit, reads.Load(), writes.Load(), stdout)
				}
			})
		}
	}
}

func TestWatcherLiteralRemoteBlankIDRefusesBeforeClientReads(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user=   ", "--validate-remote", "--dry-run", "--output=json", "--no-input")
			var frame struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code  string `json:"code"`
					Field string `json:"field"`
				} `json:"errors"`
			}
			if exit == 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != "user" || reads.Load() != 0 {
				t.Fatalf("invalid literal entered client lookup: exit=%d reads=%d stdout=%s", exit, reads.Load(), stdout)
			}
		})
	}
}

func TestWatcherLiteralRemoteRejectsAmbiguousNativeMembers(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, body := range []string{`{"accountId":"other","accountId":"literal","active":true}`, `{"accountId":"literal","active":false,"active":true}`, `{"accountId":"literal","active":true,"deleted":true,"deleted":false}`, `{"ACCOUNTID":"literal","active":true}`, `{"accountId":"literal","Active":true}`, `{"accountId":"literal","active":true,"Deleted":true}`, `{"accountId":"other","account\u0049d":"literal","active":true}`} {
			t.Run(verb+"/"+body, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user" {
						reads.Add(1)
						_, _ = fmt.Fprint(w, body)
						return
					}
					writes.Add(1)
					w.WriteHeader(204)
				}))
				defer server.Close()
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user=literal", "--validate-remote", "--no-readback", "--output=json", "--no-input")
				var frame struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if exit == 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "user_lookup_response_invalid" || reads.Load() != 1 || writes.Load() != 0 {
					t.Fatalf("ambiguous native account entered writes: exit=%d reads=%d writes=%d stdout=%s", exit, reads.Load(), writes.Load(), stdout)
				}
			})
		}
	}
}
