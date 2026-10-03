//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJQLModeFlagEffects(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"strict", "warn", "none"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				var in struct {
					Queries []string `json:"queries"`
				}
				if r.Method != "POST" || r.URL.Path != "/rest/api/3/jql/parse" || r.URL.Query().Get("validation") != mode || json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Queries) != 2 || in.Queries[0] != "project = KEY" || in.Queries[1] != "project = (" {
					t.Error("mode or ordered parser request ignored")
				}
				_, _ = io.WriteString(w, `{"queries":[{"query":"project = KEY","errors":[],"warnings":["warning"]},{"query":"project = (","errors":["syntax error"]}]}`)
			}))
			defer server.Close()
			res := fwExec(t, server.URL, nil, "", "jql", "validate", "project = KEY", "project = (", "--mode=invalid")
			fwRefusal(t, res, "flag_value_invalid", "mode")
			if hits.Load() != 0 {
				t.Fatal("invalid mode reached HTTP")
			}
			res = fwExec(t, server.URL, nil, "", "jql", "validate", "project = KEY", "project = (", "--mode="+mode)
			out := fwParse(t, res)
			if res.Exit != 0 || !out.OK || hits.Load() != 1 || fwAt(t, out.Data, "mode") != mode || fwAt(t, out.Data, "queries", 0, "valid") != true || fwAt(t, out.Data, "queries", 1, "valid") != false || fwAt(t, out.Data, "queries", 1, "errors", 0) != "syntax error" {
				t.Fatalf("mode lost executed effect or syntax errors changed exit: %s", res.Stdout)
			}
			Prove(t, "jira jql validate", "mode", "strict, warn and none reach Jira exactly, retain diagnostics, preserve successful parser exit and refuse invalid mode before HTTP")
		})
	}
}

func TestJQLBuildFilterFlagEffects(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		flag, value, want string
	}{
		{"project", "KEY,OTHER", `project in ("KEY", "OTHER")`},
		{"key", "KEY-1..KEY-3", `key in ("KEY-1", "KEY-2", "KEY-3")`},
		{"epic", "KEY-10,OTHER-2", `parent in ("KEY-10", "OTHER-2")`},
		{"status", "Open,>=In Progress,!Abandoned", `(status in ("Open") OR statusCategory in ("In Progress", "Done")) AND status != "Abandoned"`},
		{"priority", "High,Low", `priority in ("High", "Low")`},
		{"label", "backend,frontend", `labels in ("backend", "frontend")`},
		{"type", "Task,Story", `issuetype in ("Task", "Story")`},
		{"assignee", "me,none", `(assignee = currentUser() OR assignee is EMPTY)`},
		{"reporter", "me", `reporter = currentUser()`},
		{"created", "2026-01-01..2026-02-01", `created >= "2026-01-01" AND created <= "2026-02-01"`},
		{"updated", "-7d", `updated >= -7d`},
		{"resolved", "..+2w", `resolved <= 2w`},
	} {
		t.Run(test.flag, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			defer server.Close()
			res := fwExecEnv(t, server.URL, nil, "", []string{"JIRA_API_TOKEN=", "JIRA_KEYRING=true"}, "jql", "build", "--"+test.flag+"="+test.value)
			out := fwParse(t, res)
			if res.Exit != 0 || !out.OK || fwAt(t, out.Data, "jql") != test.want+" ORDER BY updated DESC" || hits.Load() != 0 {
				t.Fatalf("filter ignored or local build made HTTP: %d %s", res.Exit, res.Stdout)
			}
			Prove(t, "jira jql build", test.flag, "executed filter changes the exact typed JQL, retains default ordering and constructs no authenticated client or HTTP request")
		})
	}
}

func TestJQLBuildOrderingFlagEffects(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		flags []string
		want  string
	}{
		{[]string{"--order-by=key"}, "updated >= -365d ORDER BY key DESC"},
		{[]string{"--order-by=cf[12345]", "--desc=false"}, `updated >= -365d ORDER BY "cf[12345]" ASC`},
		{[]string{"--order-by=none"}, "updated >= -365d"},
	} {
		res := fwExecEnv(t, "", nil, "", []string{"JIRA_API_TOKEN="}, append([]string{"jql", "build"}, test.flags...)...)
		out := fwParse(t, res)
		if res.Exit != 0 || !out.OK || fwAt(t, out.Data, "jql") != test.want {
			t.Fatalf("direction/order ignored: %d %s", res.Exit, res.Stdout)
		}
	}
	Prove(t, "jira jql build", "order-by", "named and cf identifiers replace the default field, while none removes ordering without dropping the default date bound")
	Prove(t, "jira jql build", "desc", "explicit false changes custom-field ordering to ASC rather than the default DESC")
}

func TestJQLBuildBoardFlagEffects(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint"}]}`)
		case "/rest/agile/1.0/board/42/configuration":
			_, _ = io.WriteString(w, `{"id":42,"name":"Sprint","filter":{"id":"100"}}`)
		case "/rest/api/3/filter/100":
			_, _ = io.WriteString(w, `{"id":"100","jql":"project = KEY OR labels = scope ORDER BY Rank ASC"}`)
		default:
			t.Error("board warmup used an unrelated endpoint")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	env := []string{"HOME=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, "cache")}
	for _, test := range []struct{ flag, value string }{{"board", "Sprint"}, {"board-id", "42"}} {
		warm := fwExecEnv(t, server.URL, nil, "", env, "issue", "list", "--"+test.flag+"="+test.value, "--refresh", "--as-jql")
		if warm.Exit != 0 || !fwParse(t, warm).OK {
			t.Fatal("board cache warmup failed")
		}
		before := hits.Load()
		res := fwExecEnv(t, server.URL, nil, "", append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true"), "jql", "build", "--"+test.flag+"="+test.value, "--project=OTHER")
		out := fwParse(t, res)
		if res.Exit != 0 || !out.OK || hits.Load() != before || fwAt(t, out.Data, "jql") != `(project = KEY OR labels = scope) AND project in ("OTHER") ORDER BY updated DESC` || fwAt(t, out.Data, "board_scope", "applied") != true || fwAt(t, out.Data, "board_scope", "id") != float64(42) || fwAt(t, out.Data, "precedence") != "flag" {
			t.Fatalf("cached scope ignored or fetched: %d %s", res.Exit, res.Stdout)
		}
		if !strings.Contains(res.Stdout, "cached_at") {
			t.Fatal("cached board has no snapshot provenance")
		}
		Prove(t, "jira jql build", test.flag, "the exact cached board filter narrows a structured query, retains board identity/timestamp and explicit precedence with no credentials or HTTP")
	}
}
