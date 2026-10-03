package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestBinaryRedirectRefusal(t *testing.T) {
	t.Parallel()
	for _, write := range []bool{false, true} {
		name := "search POST cannot become a write"
		if write {
			name = "comment mutation cannot replay"
		}
		for _, mode := range []string{"json", "compact"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var initial, redirected atomic.Int32
				path := "/rest/api/3/search/jql"
				args := []string{"issue", "list", "--jql", "project = ABC"}
				readOnly := "true"
				if write {
					path = "/rest/api/3/issue/ABC-1/comment"
					args = []string{"issue", "comment", "add", "ABC-1", "--markdown", "test comment"}
					readOnly = "false"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == path && r.Method == http.MethodPost {
						initial.Add(1)
						http.Redirect(w, r, "/rest/api/3/issue", http.StatusTemporaryRedirect)
						return
					}
					redirected.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-2"}`))
				}))
				t.Cleanup(server.Close)
				env := []string{
					"JIRA_BASE_URL=" + server.URL,
					"JIRA_EMAIL=person@example.com",
					"JIRA_API_TOKEN=example-fixture-token",
					"JIRA_KEYRING=false",
					"JIRA_READ_ONLY=" + readOnly,
					"NO_PROXY=127.0.0.1,localhost",
				}
				args = append(args, "--output="+mode, "--no-input", "--timeout=5s")
				stdout, _, exit := runBinaryEnv(t, env, args...)
				var f frame
				if err := json.Unmarshal([]byte(stdout), &f); err != nil {
					t.Fatal(err)
				}
				if exit != cli.ExitServer || f.OK || len(f.Errors) != 1 || initial.Load() != 1 || redirected.Load() != 0 {
					t.Fatalf("redirect refusal: exit=%d ok=%t errors=%d initial=%d redirected=%d", exit, f.OK, len(f.Errors), initial.Load(), redirected.Load())
				}
				var failure struct {
					Code string `json:"code"`
					Hint string `json:"hint"`
				}
				if err := json.Unmarshal(f.Errors[0], &failure); err != nil {
					t.Fatal(err)
				}
				if failure.Code != cli.CodeRedirectRefused || !strings.Contains(failure.Hint, "before retrying") {
					t.Fatalf("redirect failure lacks its classification or inspection remedy: code=%s", failure.Code)
				}
			})
		}
	}
}

func TestBinaryReadRedirectOmitsCredentials(t *testing.T) {
	t.Parallel()
	var initial, redirected atomic.Int32
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			leaked.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":{}}`))
	}))
	t.Cleanup(target.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initial.Add(1)
		if r.Header.Get("Authorization") == "" {
			t.Error("initial request lacks authentication")
		}
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	env := []string{
		"JIRA_BASE_URL=" + origin.URL,
		"JIRA_EMAIL=person@example.com",
		"JIRA_API_TOKEN=example-fixture-token",
		"JIRA_KEYRING=false",
		"NO_PROXY=127.0.0.1,localhost",
	}
	stdout, _, exit := runBinaryEnv(t, env, "issue", "view", "ABC-1", "--output=json", "--timeout=5s")
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || !f.OK || initial.Load() != 1 || redirected.Load() != 1 || leaked.Load() {
		t.Fatalf("safe read redirect: exit=%d ok=%t initial=%d redirected=%d leaked=%t", exit, f.OK, initial.Load(), redirected.Load(), leaked.Load())
	}
}
