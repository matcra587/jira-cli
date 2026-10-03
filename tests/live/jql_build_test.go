//go:build live

package live

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestLiveJQLBuild checks actual binary composition and cache reuse against
// independent native saved-filter reads and Jira's strict parser. No writes.
func TestLiveJQLBuild(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("local builder acceptance needs the dedicated read key")
	}
	var queries []string
	for _, test := range []struct {
		flags []string
		want  string
	}{
		{nil, "updated >= -365d ORDER BY updated DESC"},
		{[]string{"--key=" + key + ".." + key, "--reporter=me", "--updated=-365d..+2w", "--desc=false"}, `key in ("` + key + `") AND reporter = currentUser() AND updated >= -365d AND updated <= 2w ORDER BY updated ASC`},
		{[]string{"--project=" + r.manifest.Project, "--status=To Do,>=In Progress,!Done", "--type=" + r.issueType, "--order-by=none"}, `project in ("` + r.manifest.Project + `") AND (status in ("To Do") OR statusCategory in ("In Progress", "Done")) AND status != "Done" AND issuetype in ("` + r.issueType + `")`},
	} {
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_KEYRING=false", "JIRA_BASE_URL=", "JIRA_EMAIL=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "AGENT=1"}, append([]string{"jql", "build"}, test.flags...)...)
		var result struct {
			Data envelope.JQLBuildOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.JQL != test.want || result.Data.URL != "" || result.Data.BoardScope.Applied || result.Data.Precedence != "none" {
			t.Fatal("credential-free binary composition changed its query or defaults; inspect local receipts")
		}
		queries = append(queries, result.Data.JQL)
	}
	frame, stdout, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var auth struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &auth) != nil || auth.Data.Email == "" || auth.Data.BaseURL != r.manifest.BaseURL {
		t.Fatal("cannot establish non-secret account identity for the cache acceptance")
	}
	boardID, err := strconv.Atoi(requiredEnv(t, "JIRA_LIVE_BOARD_ID"))
	if err != nil || boardID <= 0 {
		t.Fatal("live board identity must be a positive integer")
	}
	configuration := rawRead(t, r, "api", "get", "/rest/agile/1.0/board/"+strconv.Itoa(boardID)+"/configuration")
	var nativeConfig struct {
		Filter struct {
			ID string `json:"id"`
		} `json:"filter"`
	}
	if json.Unmarshal(configuration.Response.JSON, &nativeConfig) != nil || nativeConfig.Filter.ID == "" {
		t.Fatal("independent board configuration lacks a saved filter")
	}
	filter := rawRead(t, r, "api", "get", "/rest/api/3/filter/"+nativeConfig.Filter.ID)
	var nativeFilter struct {
		JQL string `json:"jql"`
	}
	if json.Unmarshal(filter.Response.JSON, &nativeFilter) != nil || nativeFilter.JQL == "" {
		t.Fatal("independent saved-filter read lacks JQL")
	}
	conditions, _ := command.SplitJQLOrderBy(nativeFilter.JQL)
	cachePath := filepath.Join(r.dir, "builder-cache")
	for _, selector := range []string{"--board-id=" + strconv.Itoa(boardID), "--board=" + requiredEnv(t, "JIRA_LIVE_BOARD_NAME")} {
		warm := []string{"XDG_CACHE_HOME=" + cachePath, "JIRA_EMAIL=" + auth.Data.Email, "JIRA_READ_ONLY=true"}
		frame, _, exit := r.invoke(t, t.Context(), warm, "issue", "list", selector, "--refresh", "--as-jql")
		if exit != 0 || !frame.OK {
			t.Fatal("cannot warm the live saved-filter resolution cache")
		}
		offline := append(warm, "JIRA_KEYRING=false", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "AGENT=1")
		frame, stdout, exit := r.invoke(t, t.Context(), offline, "jql", "build", selector, "--key="+key, "--order-by=key", "--desc=false")
		var result struct {
			Data envelope.JQLBuildOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.JQL != "("+conditions+`) AND key in ("`+key+`") ORDER BY key ASC` || result.Data.BoardScope.JQL != nativeFilter.JQL || !result.Data.BoardScope.Applied || result.Data.BoardScope.ID != int64(boardID) || result.Data.BoardScope.CachedAt.IsZero() || result.Data.Precedence != "flag" {
			t.Fatal("offline board query differs from the independently read saved filter; inspect local receipts")
		}
		link, err := url.Parse(result.Data.URL)
		if err != nil || link.Query().Get("jql") != result.Data.JQL {
			t.Fatal("local browser URL changed the composed query")
		}
		queries = append(queries, result.Data.JQL)
	}
	frame, stdout, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append([]string{"jql", "validate", "--mode=strict", "--"}, queries...)...)
	var validated struct {
		Data envelope.JQLValidateOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &validated) != nil || len(validated.Data.Queries) != len(queries) {
		t.Fatal("live builder strict validation did not complete")
	}
	for i, query := range validated.Data.Queries {
		if !query.Valid || len(query.Errors.Value) != 0 {
			t.Fatalf("strict parser rejected binary-built query %d; inspect local diagnostics", i)
		}
	}
}
