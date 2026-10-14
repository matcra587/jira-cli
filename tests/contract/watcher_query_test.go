package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestWatcherQueryResolvesOnceBeforeKeyedWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, query := range []string{"Ada Lovelace", "fixture@example.com", "@me", " @me "} {
			t.Run(verb+"/"+query, func(t *testing.T) {
				t.Parallel()
				var searches, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user/search" {
						searches.Add(1)
						if r.URL.Query().Get("query") != strings.TrimSpace(query) {
							t.Error("watcher query spelling changed")
						}
						if r.URL.Query().Get("startAt") == "0" {
							_, _ = io.WriteString(w, `[{"accountId":"resolved@opaque","active":true,"emailAddress":null}]`)
						} else {
							_, _ = io.WriteString(w, `[]`)
						}
						return
					}
					writes.Add(1)
					if !strings.HasSuffix(r.URL.Path, "/watchers") {
						t.Error("lookup reached unexpected read/write")
					}
					if verb == "add" {
						var id string
						if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&id) != nil || id != "resolved@opaque" {
							t.Error("watcher add did not send resolved native identity")
						}
					} else if r.Method != http.MethodDelete || r.URL.Query().Get("accountId") != "resolved@opaque" {
						t.Error("watcher remove did not send resolved native identity")
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer server.Close()
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "KEY-2", "--user-query="+query, "--no-readback", "--output=json")
				var frame struct {
					OK   bool `json:"ok"`
					Data struct {
						AccountID string                                                  `json:"account_id"`
						UserQuery string                                                  `json:"user_query"`
						Results   []envelope.KeyedResult[envelope.IssueWatchersWriteData] `json:"results"`
					} `json:"data"`
				}
				if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.AccountID != "resolved@opaque" || frame.Data.UserQuery != query || searches.Load() != 2 || writes.Load() != 2 || len(frame.Data.Results) != 2 || frame.Data.Results[0].Issue.Key != "KEY-1" || frame.Data.Results[1].Issue.Key != "KEY-2" {
					t.Fatalf("watcher query capability missing: exit=%d searches=%d writes=%d stdout=%q stderr=%q", exit, searches.Load(), writes.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestWatcherQueryLocalPreviewRetainsIntentWithoutIdentityOrIO(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user-query=Ada Lovelace", "--dry-run", "--output=json")
			var frame struct {
				OK   bool `json:"ok"`
				Data struct {
					AccountID string `json:"account_id"`
					UserQuery string `json:"user_query"`
				} `json:"data"`
				Warnings []envelope.Warning `json:"warnings"`
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.AccountID != "" || frame.Data.UserQuery != "Ada Lovelace" || calls.Load() != 0 || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "account_query_unresolved" {
				t.Fatalf("query preview fabricated identity or read: exit=%d calls=%d out=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestWatcherQueryRemotePreviewResolvesWithoutWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				searches.Add(1)
				if r.URL.Query().Get("startAt") == "0" {
					_, _ = io.WriteString(w, `[{"accountId":"resolved","active":true}]`)
				} else {
					_, _ = io.WriteString(w, `[]`)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "issue", "watchers", verb, "KEY-1", "--user-query=Ada Lovelace", "--dry-run", "--validate-remote", "--output=json")
			var frame struct {
				OK   bool `json:"ok"`
				Data struct {
					AccountID string `json:"account_id"`
					UserQuery string `json:"user_query"`
				} `json:"data"`
				Warnings []envelope.Warning `json:"warnings"`
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.AccountID != "resolved" || frame.Data.UserQuery != "Ada Lovelace" || searches.Load() != 2 || writes.Load() != 0 || len(frame.Warnings) != 0 {
				t.Fatalf("remote query preview did not remain read-only: exit=%d searches=%d writes=%d out=%q stderr=%q", exit, searches.Load(), writes.Load(), stdout, stderr)
			}
		})
	}
}

func TestWatcherQueryFormsRefuseBeforeIO(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, tc := range []struct {
			name, code string
			flags      []string
		}{
			{name: "empty", code: "flag_value_invalid", flags: []string{"--user-query="}},
			{name: "blank", code: "flag_value_invalid", flags: []string{"--user-query= \t"}},
			{name: "conflict", code: "flag_conflict", flags: []string{"--user=@me", "--user-query=someone"}},
			{name: "authored empty ID", code: "flag_conflict", flags: []string{"--user=", "--user-query=someone"}},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer server.Close()
				args := append([]string{"issue", "watchers", verb, "KEY-1", "--output=json"}, tc.flags...)
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if exit != 3 || calls.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code || frame.Errors[0].Field != "user-query" {
					t.Fatalf("invalid identity flags reached HTTP: exit=%d calls=%d out=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestWatcherQueryRefusalsNeverReachIssueWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, failure := range []string{"missing", "ambiguous", "duplicate", "malformed"} {
			t.Run(verb+"/"+failure, func(t *testing.T) {
				t.Parallel()
				var searches, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" {
						writes.Add(1)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					searches.Add(1)
					switch failure {
					case "missing":
						_, _ = io.WriteString(w, `[]`)
					case "malformed":
						_, _ = io.WriteString(w, `[{"accountId":null,"active":true}]`)
					case "duplicate":
						_, _ = io.WriteString(w, `[{"accountId":"one","active":true}]`)
					case "ambiguous":
						if r.URL.Query().Get("startAt") == "0" {
							_, _ = io.WriteString(w, `[{"accountId":"one","active":true}]`)
						} else {
							_, _ = io.WriteString(w, `[{"accountId":"two","active":true}]`)
						}
					}
				}))
				defer server.Close()
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "KEY-2", "--user-query=Someone", "--output=json")
				wantExit, wantCode := 3, "user_not_found"
				switch failure {
				case "ambiguous":
					wantCode = "user_ambiguous"
				case "duplicate", "malformed":
					wantExit, wantCode = 5, "user_search_response_invalid"
				}
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if exit != wantExit || searches.Load() == 0 || writes.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode {
					t.Fatalf("unsafe query reached watcher write/readback: exit=%d searches=%d writes=%d out=%q stderr=%q", exit, searches.Load(), writes.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestWatcherQueryWriteFailuresRetainResolvedContext(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, mode := range []string{"json", "compact"} {
			for _, failure := range []string{"partial", "all", "readback"} {
				t.Run(verb+"/"+mode+"/"+failure, func(t *testing.T) {
					t.Parallel()
					var searches, writes, readbacks atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user/search" {
							searches.Add(1)
							if r.URL.Query().Get("startAt") == "0" {
								_, _ = io.WriteString(w, `[{"accountId":"resolved","active":true}]`)
							} else {
								_, _ = io.WriteString(w, `[]`)
							}
							return
						}
						if !strings.HasSuffix(r.URL.Path, "/watchers") {
							t.Error("unexpected watcher route")
						}
						if r.Method == http.MethodGet {
							readbacks.Add(1)
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, `{"errorMessages":["readback refused"]}`)
							return
						}
						writes.Add(1)
						if failure == "all" || failure == "partial" && strings.Contains(r.URL.Path, "/KEY-2/") {
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, `{"errorMessages":["write refused"]}`)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					defer server.Close()
					args := []string{"issue", "watchers", verb, "KEY-1", "KEY-2", "--user-query=Ada Lovelace", "--output=" + mode}
					if failure != "readback" {
						args = append(args, "--no-readback")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					var frame struct {
						OK   bool            `json:"ok"`
						Data json.RawMessage `json:"data"`
					}
					payload := json.RawMessage(stdout)
					if mode == "json" || failure == "all" {
						if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK != (failure != "all") {
							t.Fatal("query outcome lost global status", exit, stdout, stderr)
						}
						payload = frame.Data
					}
					var out envelope.IssueWatchersAddOutput
					if json.Unmarshal(payload, &out) != nil || out.AccountID != "resolved" || out.UserQuery != "Ada Lovelace" || len(out.Results) != 2 || searches.Load() != 2 || writes.Load() != 2 || (exit == 0) != (failure != "all") {
						t.Fatal("query failure lost resolved context or replayed writes", exit, stdout, stderr)
					}
					for i, row := range out.Results {
						wantOK := failure == "readback" || failure == "partial" && i == 0
						if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK != wantOK || !wantOK && row.Err == nil {
							t.Fatal("query failure lost ordered key result", stdout)
						}
						if failure == "readback" && (row.Data == nil || row.Data.Readback != nil || row.Data.ReadbackError == nil) {
							t.Fatal("failed readback lost acknowledged watcher write", stdout)
						}
					}
					wantReads := int32(0)
					if failure == "readback" {
						wantReads = 2
					}
					if readbacks.Load() != wantReads {
						t.Fatal("query changed watcher readback policy", readbacks.Load())
					}
				})
			}
		}
	}
}

func TestWatcherQueryLiteralUserValuesStayOpaque(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			const id = "authored@opaque"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					searches.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				writes.Add(1)
				if verb == "add" {
					var got string
					if json.NewDecoder(r.Body).Decode(&got) != nil || got != id {
						t.Error("literal user ID rewritten")
					}
				} else if r.URL.Query().Get("accountId") != id {
					t.Error("literal user ID queried or rewritten")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "watchers", verb, "KEY-1", "--user="+id, "--no-readback", "--output=json")
			var frame struct {
				OK   bool                       `json:"ok"`
				Data map[string]json.RawMessage `json:"data"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil {
				t.Fatal("literal account result has no envelope", stdout)
			}
			var got string
			_, queryPresent := frame.Data["user_query"]
			if exit != 0 || !frame.OK || searches.Load() != 0 || writes.Load() != 1 || json.Unmarshal(frame.Data["account_id"], &got) != nil || got != id || queryPresent {
				t.Fatalf("literal account flag acquired query semantics: exit=%d searches=%d writes=%d out=%q stderr=%q", exit, searches.Load(), writes.Load(), stdout, stderr)
			}
		})
	}
}

func TestWatcherQueryGeneratedBashCompletionKeepsQueryAndIssueRolesOffline(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/DEMO-1" {
			t.Error("completion opened an unrelated endpoint")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1","key":"DEMO-1","fields":{"summary":"Fixture"}}`)
	}))
	defer server.Close()
	env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+t.TempDir(), "JIRA_NO_UPDATE_CHECK=1")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "view", "DEMO-1", "--output=json")
	if exit != 0 {
		t.Fatal("issue-key history seed did not execute", stdout, stderr)
	}
	env = append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
	script, stderr, exit := runBinaryEnv(t, env, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("generated Bash completion did not execute", exit, stderr)
	}
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			base := []string{"jira", "issue", "watchers", verb}
			if got := completionArgumentReplies(t, script, env, append(slices.Clone(base), "--user-q")); !slices.Equal(got, []string{"--user-query"}) {
				t.Fatal("new query flag was not completed", got)
			}
			if got := completionArgumentReplies(t, script, env, append(slices.Clone(base), "--user-query", "DEM")); len(got) != 0 {
				t.Fatal("query value received issue-key candidates", got)
			}
			if got := completionArgumentReplies(t, script, env, append(slices.Clone(base), "--user-query", "Demo Person", "DEM")); !slices.Equal(got, []string{"DEMO-1"}) {
				t.Fatal("query value stole following issue-key role", got)
			}
		})
	}
	if requests.Load() != 1 {
		t.Fatal("query completion opened credentials or HTTP", requests.Load())
	}
}
