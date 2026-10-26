package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func savedQueryDirectory(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inbox.jql"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSavedQueryPreviewPreservesLocalMetadataWithoutCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"plain", "project = ABC ORDER BY key ASC\n"},
		{"yaml", "---\nname: Personal inbox\ndescription: Review pending work\nproject: ABC\n---\nproject = ABC ORDER BY key ASC\n"},
		{"toml", "+++\nname = 'Personal inbox'\ndescription = 'Review pending work'\nproject = 'ABC'\n+++\nproject = ABC ORDER BY key ASC\n"},
	} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				dir := savedQueryDirectory(t, tc.body)
				args := []string{"issue", "list", "--saved=inbox", "--as-jql"}
				if mode != "agent" {
					args = append(args, "--output="+mode)
				}
				stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=" + dir, "JIRA_DEFAULT_BOARD=Uncached board", "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "AGENT=1"}, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("local saved query failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				if mode == "human" {
					if strings.TrimSpace(stdout) != "project = ABC ORDER BY key ASC" {
						t.Fatalf("human saved query changed: %q", stdout)
					}
					return
				}
				format := mode
				if mode == "agent" {
					format = "compact"
				}
				var out struct {
					envelope.IssueListOutput
					SavedQuery *struct{ Key, Name, Description, Project, JQL string } `json:"saved_query"`
				}
				data := adfData(t, stdout, format, "issue.list", true)
				if err := json.Unmarshal(data, &out); err != nil || out.JQL != "project = ABC ORDER BY key ASC" || out.Issues == nil || len(out.Issues) != 0 || out.Board != nil || out.SavedQuery == nil || out.SavedQuery.Key != "inbox" || out.SavedQuery.JQL != out.JQL {
					t.Fatalf("saved query preview lost its query, identity or metadata: err=%v data=%s", err, data)
				}
				if tc.name == "plain" {
					if out.SavedQuery.Name != "inbox" || out.SavedQuery.Description != "" || out.SavedQuery.Project != "" {
						t.Fatal("plain query invented frontmatter")
					}
				} else if out.SavedQuery.Name != "Personal inbox" || out.SavedQuery.Description != "Review pending work" || out.SavedQuery.Project != "ABC" {
					t.Fatal("saved query discarded authored frontmatter")
				}
			})
		}
	}
}

func TestSavedQueryUsesSharedCursorWalkAndRetainsSourceOnFailure(t *testing.T) {
	t.Parallel()
	for _, failed := range []bool{false, true} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			name := "complete/"
			if failed {
				name = "later failure/"
			}
			t.Run(name+mode, func(t *testing.T) {
				t.Parallel()
				dir := savedQueryDirectory(t, "project = ABC ORDER BY key ASC")
				var reads atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/search/jql" {
						t.Errorf("unexpected saved-query request %s %s", req.Method, req.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					reads.Add(1)
					var body struct{ JQL, NextPageToken string }
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.JQL != "project = ABC ORDER BY key ASC" {
						t.Errorf("saved-query search changed: %+v err=%v", body, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if body.NextPageToken == "" {
						_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1","fields":{"summary":"First"}}],"isLast":false,"nextPageToken":"next"}`))
					} else if body.NextPageToken != "next" {
						t.Error("saved-query walk sent an unexpected cursor")
						w.WriteHeader(http.StatusBadRequest)
					} else if failed {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"errorMessages":["permission denied"]}`))
					} else {
						_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2","fields":{"summary":"Second"}}],"isLast":true}`))
					}
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "list", "--saved=inbox", "--limit=2"}
				if mode != "agent" {
					args = append(args, "--output="+mode)
				}
				env := append(searchEnv(server.URL), "JIRA_QUERIES_PATH="+dir, "JIRA_DEFAULT_BOARD=Uncached board", "AGENT=1", "JIRA_READ_ONLY=true")
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				wantExit := 0
				if failed {
					wantExit = cli.ExitAuth
				}
				if exit != wantExit || reads.Load() != 2 {
					t.Fatalf("saved-query cursor result: exit=%d reads=%d stdout=%s stderr=%s", exit, reads.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "ABC-1") || (!failed && !strings.Contains(stdout, "ABC-2")) {
						t.Fatalf("human saved-query receipt lost validated rows: %s", stdout)
					}
					return
				}
				format := mode
				if mode == "agent" {
					format = "compact"
				}
				var out struct {
					envelope.IssueListOutput
					SavedQuery *struct{ Key, JQL string } `json:"saved_query"`
				}
				data := adfData(t, stdout, format, "issue.list", !failed)
				if err := json.Unmarshal(data, &out); err != nil || out.Board != nil || out.SavedQuery == nil || out.SavedQuery.Key != "inbox" || out.SavedQuery.JQL != "project = ABC ORDER BY key ASC" || len(out.Issues) == 0 || out.Issues[0].Key != "ABC-1" {
					t.Fatalf("saved-query receipt discarded validated source or rows: err=%v data=%s", err, data)
				}
				if !failed && (len(out.Issues) != 2 || out.Issues[1].Key != "ABC-2" || !out.Pagination.IsLast || out.Truncated) {
					t.Fatal("saved-query walk did not finish with ordered rows")
				}
			})
		}
	}
}

func TestSavedQueryRefusesInvalidLocalInputBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		args       []string
	}{
		{"empty query", " \n", []string{"--saved=inbox"}},
		{"empty name", "project = ABC", []string{"--saved="}},
		{"unknown name", "project = ABC", []string{"--saved=missing"}},
		{"unterminated yaml", "---\nname: Inbox\nproject = ABC", []string{"--saved=inbox"}},
		{"unterminated toml", "+++\nname = 'Inbox'\nproject = ABC", []string{"--saved=inbox"}},
		{"null metadata", "---\nproject: null\n---\nproject = ABC", []string{"--saved=inbox"}},
		{"raw conflict", "project = ABC", []string{"--saved=inbox", "--jql=project = DEF"}},
		{"board conflict", "project = ABC", []string{"--saved=inbox", "--board-id=42"}},
		{"structured conflict", "project = ABC", []string{"--saved=inbox", "--project=DEF"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := savedQueryDirectory(t, tc.body)
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reads.Add(1); w.WriteHeader(http.StatusBadRequest) }))
			t.Cleanup(server.Close)
			args := append([]string{"issue", "list", "--output=json"}, tc.args...)
			stdout, _, exit := runBinaryEnv(t, append(searchEnv(server.URL), "JIRA_QUERIES_PATH="+dir), args...)
			var frame apiFrame
			if exit != cli.ExitValidation || reads.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code == "flag_unknown" {
				t.Fatalf("invalid saved query was accepted, misclassified or sent: exit=%d reads=%d stdout=%s", exit, reads.Load(), stdout)
			}
		})
	}
}

func TestSavedQueryDiscoveryAndCompletionUseSelectedLocalConfiguration(t *testing.T) {
	t.Parallel()
	dir := savedQueryDirectory(t, "---\nname: Personal inbox\n---\nproject = ABC")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("keyring = false\nqueries_path = '"+filepath.ToSlash(dir)+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=", "JIRA_KEYRING=false"}, "--@complete=savedquery", "--", "issue", "list", "--config", path)
	if exit != 0 || stderr != "" || stdout != "inbox\tPersonal inbox\n" {
		t.Fatalf("saved query completion ignored selected preferences: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=", "JIRA_KEYRING=false"}, "issue", "list", "--saved=inbox", "--as-jql", "--config", path, "--output=human")
	if exit != 0 || stderr != "" || strings.TrimSpace(stdout) != "project = ABC" {
		t.Fatalf("saved-query preview ignored selected preferences: exit=%d out=%q stderr=%q", exit, stdout, stderr)
	}
	for _, verb := range []string{"list", "mine"} {
		stdout, stderr, exit = runBinary(t, "agent", "schema", "--path=issue "+verb, "--output=json")
		for _, text := range []string{`"saved"`, `"predictor=savedquery"`, `"saved_query"`, `"description"`, "queries_path"} {
			if exit != 0 || stderr != "" || !strings.Contains(stdout, text) {
				t.Fatalf("saved-query discovery omitted %s for %s: exit=%d stderr=%s", text, verb, exit, stderr)
			}
		}
	}
	stdout, _, exit = runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=" + dir, "JIRA_KEYRING=false"}, "issue", "list", "--saved=missing", "--as-jql", "--output=json")
	var frame struct {
		Errors []struct {
			Code        string
			Suggestions []string
		}
	}
	if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "saved_query_unknown" || !slices.Equal(frame.Errors[0].Suggestions, []string{"inbox"}) {
		t.Fatalf("unknown saved name lost usable literal suggestions: exit=%d stdout=%s", exit, stdout)
	}
	stdout, stderr, exit = runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=" + dir, "JIRA_KEYRING=false"}, "issue", "list", "--saved=inbox", "--as-jql", "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatalf("suggested saved name did not recover the workflow: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
}

func TestSavedQueryMineRetainsAccountScopeAndRejectsGroupingEscape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, want string
		args             []string
		failed           bool
	}{
		{"raw scope", "project = ABC OR project = DEF ORDER BY key ASC", "assignee = currentUser() AND (project = ABC OR project = DEF) ORDER BY key ASC", nil, false},
		{"structured narrowing", "project = ABC OR project = DEF", `assignee = currentUser() AND status in ("Open") AND (project = ABC OR project = DEF)`, []string{"--status=Open"}, false},
		{"closing escape", "project = ABC) OR assignee IS EMPTY OR (project = ABC", "", nil, true},
		{"reverse needs order", "project = ABC ORDER BY key ASC", "", []string{"--reverse"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := savedQueryDirectory(t, tc.body)
			args := append([]string{"issue", "mine", "--saved=inbox", "--as-jql", "--output=human"}, tc.args...)
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_QUERIES_PATH=" + dir, "JIRA_DEFAULT_BOARD=Uncached board", "JIRA_KEYRING=false", "JIRA_API_TOKEN="}, args...)
			if tc.failed {
				if exit != cli.ExitValidation || stdout != "" {
					t.Fatalf("invalid saved-query account scope passed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
			} else if exit != 0 || stderr != "" || strings.TrimSpace(stdout) != tc.want {
				t.Fatalf("saved query lost required account grouping/order: exit=%d stdout=%q want=%q stderr=%s", exit, stdout, tc.want, stderr)
			}
		})
	}
}

func TestSavedQueryCountAndWebUseTheAuthoredQuery(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"count", "web"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			query := `project = ABC AND summary ~ "a,b & <c>"`
			dir := savedQueryDirectory(t, query)
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				reads.Add(1)
				var body struct{ JQL string }
				if mode != "count" || req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/search/approximate-count" || json.NewDecoder(req.Body).Decode(&body) != nil || body.JQL != query {
					t.Error("saved-query short circuit searched issues or changed the authored query")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"count":42}`))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, append(searchEnv(server.URL), "JIRA_QUERIES_PATH="+dir, "JIRA_DEFAULT_BOARD=Uncached board", "AGENT=1", "JIRA_READ_ONLY=true"), "issue", "list", "--saved=inbox", "--"+mode, "--output=json")
			var out envelope.IssueListOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "issue.list", true), &out) != nil || out.Board != nil || out.SavedQuery == nil || out.SavedQuery.JQL != query || out.Issues == nil || len(out.Issues) != 0 {
				t.Fatalf("saved-query short circuit lost its source or result: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			if mode == "count" {
				if reads.Load() != 1 || out.Count == nil || out.Count.Value != 42 || !out.Count.Approximate {
					t.Fatal("saved-query count lost its approximation or issued extra requests")
				}
			} else {
				target, err := url.Parse(out.WebURL)
				if reads.Load() != 0 || err != nil || target.Query().Get("jql") != query {
					t.Fatal("saved-query web route changed JQL or contacted Jira")
				}
			}
		})
	}
}
