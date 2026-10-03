package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestEpicMembershipBinaryWritesReceiptsAndModes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(verb+" "+mode, func(t *testing.T) {
				t.Parallel()
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						if r.URL.Path == "/rest/api/3/issue/KEY-100" && r.URL.Query().Get("fields") == "issuetype" {
							_, _ = fmt.Fprint(w, `{"fields":{"issuetype":{"name":"Epic","subtask":false,"hierarchyLevel":1}}}`)
							return
						}
						if r.URL.Path == "/rest/api/3/issue/KEY-1" || r.URL.Path == "/rest/api/3/issue/KEY-2" {
							if verb == "remove" && r.URL.Query().Get("fields") == "parent,issuetype" {
								_, _ = fmt.Fprint(w, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
							} else if r.URL.Query().Get("fields") == "parent,summary" {
								if verb == "add" {
									_, _ = fmt.Fprint(w, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
								} else {
									_, _ = fmt.Fprint(w, `{"fields":{"parent":null}}`)
								}
							} else {
								t.Error("unexpected membership read fields")
								w.WriteHeader(400)
							}
							return
						}
					}
					if r.Method != "PUT" || (r.URL.Path != "/rest/api/3/issue/KEY-1" && r.URL.Path != "/rest/api/3/issue/KEY-2") || r.URL.Query().Get("notifyUsers") != "false" {
						t.Errorf("unexpected membership request: %s %s", r.Method, r.URL)
						w.WriteHeader(400)
						return
					}
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid parent body")
						w.WriteHeader(400)
						return
					}
					var parent any
					if verb == "add" {
						parent = map[string]any{"key": "KEY-100"}
					}
					if !reflect.DeepEqual(body, map[string]any{"fields": map[string]any{"parent": parent}}) {
						t.Errorf("membership changed unrelated fields: %v", body)
					}
					writes.Add(1)
					if r.URL.Path == "/rest/api/3/issue/KEY-2" {
						w.WriteHeader(404)
						return
					}
					w.WriteHeader(204)
				}))
				defer server.Close()
				args := []string{"epic", verb, "KEY-1:2"}
				if verb == "add" {
					args = append(args, "KEY-100")
				}
				args = append(args, "--verify", "--notify=false", "--parallelism=1", "--no-input", "--output="+mode)
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var out envelope.EpicMembershipOutput
				if json.Unmarshal(adfData(t, stdout, mode, "epic."+verb, true), &out) != nil || exit != 0 || stderr != "" || out.DryRun || out.Notify || len(out.Results) != 2 || writes.Load() != 2 {
					t.Fatalf("membership receipt/mode changed: exit=%d %s stderr=%q writes=%d", exit, stdout, stderr, writes.Load())
				}
				first, second := out.Results[0], out.Results[1]
				if first.Issue.Key != "KEY-1" || !first.OK || !first.Data.Applied || !first.Data.RemoteValidated || first.Data.VerifyFailed || first.Data.Parent == nil || second.Issue.Key != "KEY-2" || second.OK || second.Err.Code != "jira_not_found" {
					t.Fatalf("ordered partial outcome/readback lost: %s", stdout)
				}
				want := "value"
				if verb == "remove" {
					want = "null"
				}
				if first.Data.Parent.State != want {
					t.Fatalf("parent presence lost: %s", stdout)
				}
			})
		}
	}
}

func TestEpicMembershipBinarySafeguardsAndNoReplay(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, tc := range []struct {
			name         string
			env          []string
			flags        []string
			exit, writes int
			code         string
		}{
			{"local preview", nil, []string{"--dry-run"}, 0, 0, ""},
			{"explicit headless target", nil, nil, 5, 1, "jira_server_error"},
			{"read only", []string{"JIRA_READ_ONLY=true"}, []string{"--no-input"}, 3, 0, "read_only_blocked"},
			{"server failure", nil, []string{"--no-input"}, 5, 1, "jira_server_error"},
		} {
			t.Run(verb+" "+tc.name, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						reads.Add(1)
						if strings.HasSuffix(r.URL.Path, "KEY-100") {
							_, _ = fmt.Fprint(w, `{"fields":{"issuetype":{"name":"Epic","subtask":false}}}`)
						} else {
							_, _ = fmt.Fprint(w, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
						}
						return
					}
					writes.Add(1)
					w.WriteHeader(503)
				}))
				defer server.Close()
				env := append(editReceiptEnv(server.URL), tc.env...)
				args := []string{"epic", verb, "KEY-1"}
				if verb == "add" {
					args = append(args, "KEY-100")
				}
				args = append(args, "--output=json")
				args = append(args, tc.flags...)
				stdout, _, exit := runBinaryEnv(t, env, args...)
				if exit != tc.exit || writes.Load() != int32(tc.writes) || (tc.code != "" && !strings.Contains(stdout, tc.code)) {
					t.Fatalf("safeguard/replay failed: exit=%d writes=%d output=%s", exit, writes.Load(), stdout)
				}
				if tc.name == "local preview" && reads.Load() != 0 {
					t.Fatal("local safeguard reached Jira")
				}
				if tc.name == "server failure" {
					var frame struct {
						Data envelope.EpicMembershipOutput `json:"data"`
					}
					if json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Data.Results) != 1 || frame.Data.Results[0].OK || frame.Data.Results[0].Err.Code != tc.code {
						t.Fatal("total failure lost per-issue receipt")
					}
				}
			})
		}
	}
}

func TestEpicMembershipBinaryLocalValidationAndDiscovery(t *testing.T) {
	t.Parallel()
	env := []string{"JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL="}
	for _, args := range [][]string{
		{"epic", "add", "KEY-1", "KEY-1"},
		{"epic", "add", "KEY-1", "KEY-100:100"},
		{"epic", "add", "KEY-1:1001", "KEY-100"},
		{"epic", "remove", "KEY-0"},
		{"epic", "remove", "KEY-1", "--parallelism=17"},
		{"epic", "add", "KEY-1", "KEY-100", "--notify=true", "--skip-notify"},
	} {
		stdout, _, exit := runBinaryEnv(t, env, append(args, "--dry-run", "--output=json")...)
		if exit != 3 || (!strings.Contains(stdout, "arg_value_invalid") && !strings.Contains(stdout, "flag_value_invalid") && !strings.Contains(stdout, "flag_conflict")) {
			t.Fatalf("invalid input reached auth or was accepted: %v exit=%d %s", args, exit, stdout)
		}
	}
	for _, verb := range []string{"add", "remove"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "epic", verb, "--help")
		if exit != 0 || !strings.Contains(stdout+stderr, "--verify") || !strings.Contains(stdout+stderr, "--validate-remote") || !strings.Contains(stdout+stderr, "--parallelism") {
			t.Fatalf("membership help lost flags or needs auth: %s %s", stdout, stderr)
		}
	}
}
