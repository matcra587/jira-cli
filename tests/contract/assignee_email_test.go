package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAssigneeEmailResolvesBeforeIssueWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit", "clone"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user/search" {
					searches.Add(1)
					if r.URL.Query().Get("query") != "fixture@example.com" {
						t.Error("email lookup changed authored query")
					}
					if r.URL.Query().Get("startAt") == "0" {
						_, _ = io.WriteString(w, `[{"accountId":"resolved@opaque","active":true,"emailAddress":null}]`)
					} else {
						_, _ = io.WriteString(w, `[]`)
					}
					return
				}
				if verb == "clone" && r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/KEY-1" {
					_, _ = io.WriteString(w, `{"id":"10001","key":"KEY-1","fields":{"project":{"id":"10402","key":"KEY"},"issuetype":{"id":"10002","name":"Task"},"summary":"Source","description":null,"labels":[]}}`)
					return
				}
				writes.Add(1)
				var payload struct {
					Fields struct {
						Assignee struct {
							AccountID string `json:"accountId"`
						} `json:"assignee"`
					} `json:"fields"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Fields.Assignee.AccountID != "resolved@opaque" {
					t.Error("authored email escaped unresolved into a Jira write")
				}
				if (verb == "create" || verb == "clone") && r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue" {
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"10002","key":"KEY-2"}`)
				} else if verb == "edit" && r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/KEY-1" {
					w.WriteHeader(http.StatusNoContent)
				} else {
					t.Error("unexpected assignment request")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			args := []string{"issue", verb}
			if verb == "create" {
				args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
			} else {
				args = append(args, "KEY-1")
			}
			args = append(args, "--assignee=fixture@example.com", "--output=json", "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if exit != 0 || searches.Load() == 0 || writes.Load() != 1 {
				t.Fatalf("email assignment capability missing: exit=%d searches=%d writes=%d stdout=%q stderr=%q", exit, searches.Load(), writes.Load(), stdout, stderr)
			}
		})
	}
}

func TestAssigneeEmailMissingAmbiguousAndRedirectedSearchNeverWrites(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"missing", "ambiguous", "malformed", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			var reads, writes atomic.Int32
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("foreign user lookup leaked credentials")
				}
				_, _ = io.WriteString(w, `[{"accountId":"foreign","active":true}]`)
			}))
			defer foreign.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				reads.Add(1)
				switch failure {
				case "missing":
					_, _ = io.WriteString(w, `[]`)
				case "ambiguous":
					if r.URL.Query().Get("startAt") == "0" {
						_, _ = io.WriteString(w, `[{"accountId":"one","active":true}]`)
					} else {
						_, _ = io.WriteString(w, `[{"accountId":"two","active":true}]`)
					}
				case "malformed":
					_, _ = io.WriteString(w, `[{"accountId":" ","active":true}]`)
				case "redirect":
					http.Redirect(w, r, foreign.URL+"/reply", http.StatusFound)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "edit", "KEY-1", "--assignee=fixture@example.com", "--output=json", "--no-input")
			wantExit, wantCode := 3, "user_not_found"
			if failure == "ambiguous" {
				wantCode = "user_ambiguous"
			}
			if failure == "malformed" || failure == "redirect" {
				wantExit, wantCode = 5, "user_search_response_invalid"
			}
			if exit != wantExit || writes.Load() != 0 || reads.Load() == 0 || !strings.Contains(stdout, wantCode) {
				t.Fatalf("unsafe email lookup authorized write: exit=%d writes=%d stdout=%q stderr=%q", exit, writes.Load(), stdout, stderr)
			}
		})
	}
}

func TestAssigneeEmailLocalPreviewIsOfflineAndWarned(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			args := []string{"issue", verb}
			if verb == "create" {
				args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
			} else {
				args = append(args, "KEY-1")
			}
			args = append(args, "--assignee=fixture@example.com", "--dry-run", "--output=json")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame struct {
				OK       bool               `json:"ok"`
				Warnings []envelope.Warning `json:"warnings"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != 0 || !frame.OK || calls.Load() != 0 || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "account_email_unresolved" || !strings.Contains(stdout, "fixture@example.com") {
				t.Fatalf("email preview read credentials/network or invented resolution: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestAssigneeEmailRemotePreviewUsesSharedFieldPreflightWithoutWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var searches, metadata, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/rest/api/3/user/search" {
					searches.Add(1)
					if r.URL.Query().Get("startAt") == "0" {
						_, _ = io.WriteString(w, `[{"accountId":"resolved@opaque","active":true}]`)
					} else {
						_, _ = io.WriteString(w, `[]`)
					}
					return
				}
				metadata.Add(1)
				switch r.URL.Path {
				case "/rest/api/3/project/KEY":
					_, _ = io.WriteString(w, `{"id":"10402","key":"KEY"}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes":
					_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10001","name":"Task"}]}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes/10001":
					_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":2,"fields":[{"fieldId":"summary","key":"summary","name":"Summary","required":true,"schema":{"type":"string"},"operations":["set"]},{"fieldId":"assignee","key":"assignee","name":"Assignee","required":false,"schema":{"type":"user"},"operations":["set"]}]}`)
				case "/rest/api/3/issue/KEY-1/editmeta":
					_, _ = io.WriteString(w, `{"fields":{"assignee":{"key":"assignee","name":"Assignee","required":false,"schema":{"type":"user"},"operations":["set"]}}}`)
				default:
					t.Error("email preflight used unexpected metadata route")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			args := []string{"issue", verb}
			if verb == "create" {
				args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
			} else {
				args = append(args, "KEY-1")
			}
			args = append(args, "--assignee=fixture@example.com", "--dry-run", "--validate-remote", "--output=json")
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), args...)
			if exit != 0 || searches.Load() != 2 || metadata.Load() == 0 || writes.Load() != 0 || !strings.Contains(stdout, "resolved@opaque") || !strings.Contains(stdout, `"field_preflight"`) || strings.Contains(stdout, "account_email_unresolved") {
				t.Fatalf("remote email preview bypassed shared preflight or wrote: exit=%d searches=%d metadata=%d writes=%d stdout=%q stderr=%q", exit, searches.Load(), metadata.Load(), writes.Load(), stdout, stderr)
			}
		})
	}
}

func TestAssigneeEmailMalformedSelectorRefusesBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"edit", "clone"} {
		for _, preview := range []bool{false, true} {
			t.Run(verb+"/preview="+strconv.FormatBool(preview), func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.WriteString(w, `{"isLast":true,"issues":[{"id":"10001","key":"KEY-1"}]}`)
				}))
				defer server.Close()
				args := []string{"issue", verb, "--jql=project=KEY", "--assignee=invalid@", "--output=json", "--no-input"}
				if preview {
					args = append(args, "--dry-run", "--validate-remote")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != 3 || calls.Load() != 0 || !strings.Contains(stdout, "flag_value_invalid") {
					t.Fatalf("malformed email reached selector/credential work: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
			})
		}
	}
}
