package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueMinePreviewPinsCurrentAccountThroughTheBinary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, `assignee = currentUser() ORDER BY updated ASC`},
		{"blank raw retains preset", []string{"--jql= \n\t "}, `assignee = currentUser() ORDER BY updated ASC`},
		{"structured", []string{"--project=ABC", "--status=!Done"}, `project in ("ABC") AND assignee = currentUser() AND status != "Done" ORDER BY updated ASC`},
		{"raw or", []string{"--jql=project = ABC OR project = DEF ORDER BY key DESC"}, `assignee = currentUser() AND (project = ABC OR project = DEF) ORDER BY key DESC`},
		{"raw and structured", []string{"--project=ABC", "--jql=status = Open OR status = Done"}, `project in ("ABC") AND assignee = currentUser() AND (status = Open OR status = Done)`},
		{"raw order wins", []string{"--order-by=created", "--jql=project = ABC ORDER BY key DESC"}, `assignee = currentUser() AND (project = ABC) ORDER BY key DESC`},
		{"order only", []string{"--jql=ORDER BY updated DESC"}, `assignee = currentUser() ORDER BY updated DESC`},
		{"quoted ordering", []string{`--jql=summary ~ "order by priority" OR summary ~ 'order by key' ORDER BY created ASC`}, `assignee = currentUser() AND (summary ~ "order by priority" OR summary ~ 'order by key') ORDER BY created ASC`},
		{"explicit sort", []string{"--order-by=created", "--reverse"}, `assignee = currentUser() ORDER BY created DESC`},
		{"keys epics dates", []string{"--key=ABC-1,ABC-2", "--epic=ABC-9", "--updated=-7d", "--created=2026-01-01..2026-02-01", "--resolved=<=2026-03-01"}, `key in ("ABC-1", "ABC-2") AND parent in ("ABC-9") AND assignee = currentUser() AND created >= "2026-01-01" AND created <= "2026-02-01" AND updated >= -7d AND resolved <= "2026-03-01" ORDER BY updated ASC`},
		{"key range", []string{"--key=ABC-1..ABC-2"}, `key in ("ABC-1", "ABC-2") AND assignee = currentUser() ORDER BY updated ASC`},
	} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				args := append([]string{"issue", "mine", "--as-jql"}, tc.args...)
				if mode != "agent" {
					args = append(args, "--output="+mode)
				}
				stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "AGENT=1", "JIRA_READ_ONLY=true"}, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("offline current-account preview: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				if mode == "human" {
					if strings.TrimSpace(stdout) != tc.want {
						t.Fatalf("human query=%q, want %q", stdout, tc.want)
					}
					return
				}
				format := mode
				if mode == "agent" {
					format = "compact"
				}
				var out envelope.IssueListOutput
				data := adfData(t, stdout, format, "issue.mine", true)
				if err := json.Unmarshal(data, &out); err != nil || out.JQL != tc.want || out.Issues == nil || len(out.Issues) != 0 || out.Board != nil {
					t.Fatalf("pinned query or typed preview changed: err=%v data=%s want=%q", err, data, tc.want)
				}
			})
		}
	}
}

func TestIssueMineRefusesUnspecifiedReverseOrderingBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--jql=project = ABC", "--reverse"},
		{"--jql=project = ABC ORDER BY key ASC", "--reverse"},
		{"--jql=project = ABC", "--desc"},
		{"--board=Sprint", "--reverse"},
		{"--board-id=42", "--reverse"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusBadRequest) }))
			t.Cleanup(server.Close)
			argv := append([]string{"issue", "mine"}, args...)
			argv = append(argv, "--output=json")
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), argv...)
			var frame apiFrame
			if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_not_applicable" || frame.Errors[0].Field != "reverse" {
				t.Fatalf("reverse was ignored or resolved ordering through HTTP: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
			}
		})
	}
}

func TestIssueMineReadsPinnedQueryThroughTheBinary(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var searches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/search/jql" {
					t.Errorf("unexpected current-account request %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				searches.Add(1)
				var body struct {
					JQL        string   `json:"jql"`
					Fields     []string `json:"fields"`
					MaxResults int      `json:"maxResults"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body.JQL != `assignee = currentUser() AND (project = ABC OR project = DEF) ORDER BY key ASC` || body.MaxResults != 2 || len(body.Fields) != 1 || body.Fields[0] != "*all" {
					t.Errorf("search lost account scope, raw ordering, limit or full fields: %+v", body)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"issues":[{"id":"1001","key":"ABC-1","fields":{"summary":"Owned","customfield_10011":null}}],"isLast":true}`))
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "mine", "--jql=project = ABC OR project = DEF ORDER BY key ASC", "--fields=*all", "--limit=2"}
			env := searchEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || searches.Load() != 1 || stderr != "" {
				t.Fatalf("pinned read: exit=%d searches=%d stdout=%s stderr=%s", exit, searches.Load(), stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "ABC-1") || !strings.Contains(stdout, "Owned") {
					t.Fatalf("human read lost the observed issue: %s", stdout)
				}
				return
			}
			format := mode
			if mode == "agent" {
				format = "compact"
			}
			var out envelope.IssueListOutput
			data := adfData(t, stdout, format, "issue.mine", true)
			if err := json.Unmarshal(data, &out); err != nil || len(out.Issues) != 1 || out.Issues[0].Key != "ABC-1" || !out.Pagination.IsLast || out.Pagination.Returned != 1 || out.Truncated || !strings.Contains(string(data), `"customfield_10011":null`) {
				t.Fatalf("native list data or pagination lost: err=%v data=%s", err, data)
			}
		})
	}
}

func TestIssueMineRetainsPagesAfterLaterFailureThroughTheBinary(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected retained-page request %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			JQL           string `json:"jql"`
			NextPageToken string `json:"nextPageToken"`
			MaxResults    int    `json:"maxResults"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := calls.Add(1)
		if body.JQL != `assignee = currentUser() ORDER BY updated ASC` || (n == 1 && (body.NextPageToken != "" || body.MaxResults != 2)) || (n == 2 && (body.NextPageToken != "next" || body.MaxResults != 1)) {
			t.Errorf("resumed search escaped scope or lost remaining count: %+v", body)
		}
		if n == 1 {
			_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1","fields":{"summary":"First"}}],"isLast":false,"nextPageToken":"next"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":["later page failed"]}`))
	}))
	t.Cleanup(server.Close)
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "mine", "--limit=2", "--output=json", "--jq=.data.issues[].key")
	var frame struct {
		OK   bool `json:"ok"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
		Data   *envelope.IssueListOutput `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil || exit != cli.ExitValidation || calls.Load() != 2 || frame.OK || frame.Meta.Command != "issue.mine" || frame.Data == nil || len(frame.Data.Issues) != 1 || frame.Data.Issues[0].Key != "ABC-1" || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_bad_request" {
		t.Fatalf("later failure lost observed page or was jq-filtered: err=%v exit=%d calls=%d stdout=%s", err, exit, calls.Load(), stdout)
	}
}

func TestIssueMineHelpIsLazyAndCannotChangeAssignee(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "AGENT=1"}, "issue", "mine", "--help")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "currentUser()") || !strings.Contains(stdout, "--jql") {
		t.Fatalf("mine help requires credentials or permits changing its account: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--assignee ") {
			t.Fatalf("mine advertises an assignee override: %s", line)
		}
	}
	stdout, _, exit = runBinaryEnv(t, []string{"JIRA_KEYRING=false", "AGENT=1"}, "issue", "mine", "--assignee=none", "--output=json")
	if exit != cli.ExitValidation || strings.Contains(stdout, "auth_required") || !strings.Contains(stdout, "flag_unknown") {
		t.Fatalf("assignee override reached authentication: exit=%d stdout=%s", exit, stdout)
	}
}

func TestIssueMineRejectsMalformedFiltersBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ flag, value string }{
		{"key", "ABC-1..DEF-3"},
		{"key", "ABC-0"},
		{"epic", "ABC-1..ABC-2"},
		{"status", "Done\n"},
		{"project", "ABC\n"},
		{"created", ""},
		{"updated", " "},
		{"resolved", ".."},
		{"updated", "-7d\n"},
	} {
		t.Run(tc.flag+"/"+tc.value, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusBadRequest) }))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "mine", "--"+tc.flag+"="+tc.value, "--output=json")
			var frame apiFrame
			if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != tc.flag {
				t.Fatalf("malformed filter reached Jira or was misclassified: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
			}
		})
	}
}

func TestIssueMineWebRetainsPinnedQueryWithoutSearch(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusBadRequest) }))
	t.Cleanup(server.Close)
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "mine", "--web", "--jql=project = ABC OR project = DEF ORDER BY key ASC", "--output=json")
	var frame struct {
		OK   bool                     `json:"ok"`
		Data envelope.IssueListOutput `json:"data"`
	}
	if exit != 0 || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Data.Issues) != 0 {
		t.Fatalf("web search contacted Jira or lost typed result: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
	}
	parsed, err := url.Parse(frame.Data.WebURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host != strings.TrimPrefix(server.URL, "http://") || parsed.Query().Get("jql") != `assignee = currentUser() AND (project = ABC OR project = DEF) ORDER BY key ASC` {
		t.Fatalf("web query escaped current-account scope: err=%v stdout=%s", err, stdout)
	}
}

func TestIssueMineRefusesUnbalancedRawScopeBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`key = ABC-1) OR key = ABC-2 OR (key = ABC-1`,
		`(project = ABC`, `project = ABC)`, `summary ~ "unclosed`,
		`summary ~ 'unclosed`, `project = ABC ORDER BY "unclosed`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte(`{"issues":[],"isLast":true}`))
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "mine", "--jql="+raw, "--output=json")
			var frame apiFrame
			if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != "jql" {
				t.Fatalf("raw query escaped account scope or reached HTTP: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
			}
		})
	}
}
