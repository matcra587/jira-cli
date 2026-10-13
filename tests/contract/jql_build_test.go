package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestJQLBuildBinaryDefaultsFiltersAndOutputModes(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "compact", "human"} {
		for _, test := range []struct {
			flags []string
			want  string
		}{
			{nil, "updated >= -365d ORDER BY updated DESC"},
			{[]string{"--order-by=none"}, "updated >= -365d"},
			{[]string{"--key=KEY-1..KEY-3", "--epic=KEY-10", "--assignee=me,none", "--status=Open,>=In Progress,!Abandoned", "--resolved=..+2w", "--order-by=resolutiondate", "--desc=false"}, `key in ("KEY-1", "KEY-2", "KEY-3") AND parent in ("KEY-10") AND (assignee = currentUser() OR assignee is EMPTY) AND (status in ("Open") OR statusCategory in ("In Progress", "Done")) AND status != "Abandoned" AND resolved <= 2w ORDER BY "resolutiondate" ASC`},
		} {
			t.Run(format+"/"+test.want, func(t *testing.T) {
				t.Parallel()
				args := append([]string{"jql", "build", "--output=" + format}, test.flags...)
				stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true"}, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("local build needed authentication: exit=%d stderr=%q stdout=%s", exit, stderr, stdout)
				}
				if format == "human" {
					if strings.TrimSpace(stdout) != test.want {
						t.Fatalf("human query differs: %q", stdout)
					}
					return
				}
				var out envelope.JQLBuildOutput
				data := adfData(t, stdout, format, "jql.build", true)
				if json.Unmarshal(data, &out) != nil || out.JQL != test.want || out.URL != "" || out.Precedence != "none" || out.BoardScope.Applied {
					t.Fatalf("typed local query differs: %s", data)
				}
			})
		}
	}
}

func TestJQLBuildBinaryReusesLiveResolutionWithoutCredentialsOrHTTP(t *testing.T) {
	t.Parallel()
	const saved = `project = KEY OR labels = scope ORDER BY Rank ASC`
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "GET" {
			t.Error("board warmup made a write")
		}
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint"}]}`)
		case "/rest/agile/1.0/board/42/configuration":
			_, _ = fmt.Fprint(w, `{"id":42,"name":"Sprint","filter":{"id":"100"}}`)
		case "/rest/api/3/filter/100":
			_, _ = fmt.Fprintf(w, `{"id":"100","jql":%q}`, saved)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	// Public and authenticated constructors must retain the same authored
	// outer namespace even when the client normalizes its inner URL spelling.
	env := boardCacheEnv(t, server.URL+"/")
	for _, selector := range []string{"--board-id=42", "--board=sprint"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "issue", "list", selector, "--refresh", "--as-jql", "--output=json")
		if exit != 0 || stderr != "" {
			t.Fatalf("live cache warmup failed: %d %s %s", exit, stderr, stdout)
		}
	}
	before := hits.Load()
	offline := append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "AGENT=1")
	for _, selector := range []string{"--board-id=42", "--board=sprint", "--board=Sprint"} {
		for _, mode := range []string{"json", "compact"} {
			stdout, stderr, exit := runBinaryEnv(t, offline, "jql", "build", selector, "--status=Done", "--desc=false", "--order-by=key", "--output="+mode)
			var out envelope.JQLBuildOutput
			data := adfData(t, stdout, mode, "jql.build", true)
			if exit != 0 || stderr != "" || json.Unmarshal(data, &out) != nil || out.JQL != `(project = KEY OR labels = scope) AND status in ("Done") ORDER BY key ASC` || !out.BoardScope.Applied || out.BoardScope.ID != 42 || out.BoardScope.Name != "Sprint" || out.BoardScope.JQL != saved || out.BoardScope.CachedAt.IsZero() || out.Precedence != "flag" || hits.Load() != before {
				t.Fatalf("offline scope changed or touched credentials/network: %d %s %s", exit, stderr, data)
			}
			link, err := url.Parse(out.URL)
			if err != nil || link.Query().Get("jql") != out.JQL {
				t.Fatalf("browser query differs: %s", out.URL)
			}
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, offline, "jql", "build", "--board-id=42", "--status=Done", "--output=human")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "Cached board: Sprint (42), saved ") || !strings.Contains(stdout, "(project = KEY OR labels = scope)") || hits.Load() != before {
		t.Fatalf("human cached scope lost provenance or touched network: %d %s %s", exit, stderr, stdout)
	}
	for _, test := range []struct {
		env      []string
		selector string
		code     string
		exit     int
	}{
		{append(slices.Clone(offline), "JIRA_EMAIL=other@example.com"), "--board-id=42", "board_cache_unavailable", 2},
		{append(slices.Clone(offline), "JIRA_BASE_URL=https://other.example"), "--board-id=42", "board_cache_unavailable", 2},
		{append(slices.Clone(offline), "JIRA_EMAIL="), "--board-id=42", "config_invalid", 3},
		{offline, "--board-id=43", "board_cache_unavailable", 2},
		{offline, "--board=Other", "board_cache_unavailable", 2},
	} {
		stdout, _, exit := runBinaryEnv(t, test.env, "jql", "build", test.selector, "--output=json")
		var out apiFrame
		if exit != test.exit || json.Unmarshal([]byte(stdout), &out) != nil || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != test.code || hits.Load() != before {
			t.Fatalf("offline scope refusal fetched/fell back: %d %s", exit, stdout)
		}
	}
}
