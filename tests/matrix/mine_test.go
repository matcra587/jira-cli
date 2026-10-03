//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestMineFilterAndOrderFlagsKeepRequiredAccount(t *testing.T) {
	for _, tc := range []struct{ flag, value, conditions, ordering string }{
		{"project", "ABC", `project in ("ABC") AND assignee = currentUser()`, "updated ASC"},
		{"key", "KEY-1..KEY-2", `key in ("KEY-1", "KEY-2") AND assignee = currentUser()`, "updated ASC"},
		{"epic", "KEY-9", `parent in ("KEY-9") AND assignee = currentUser()`, "updated ASC"},
		{"status", "!Done", `assignee = currentUser() AND status != "Done"`, "updated ASC"},
		{"type", "Task", `assignee = currentUser() AND issuetype in ("Task")`, "updated ASC"},
		{"label", "owned", `assignee = currentUser() AND labels in ("owned")`, "updated ASC"},
		{"reporter", "me", `assignee = currentUser() AND reporter = currentUser()`, "updated ASC"},
		{"priority", "High", `assignee = currentUser() AND priority in ("High")`, "updated ASC"},
		{"created", "2026-01-01..2026-02-01", `assignee = currentUser() AND created >= "2026-01-01" AND created <= "2026-02-01"`, "updated ASC"},
		{"updated", "-7d", `assignee = currentUser() AND updated >= -7d`, "updated ASC"},
		{"resolved", "..+2w", `assignee = currentUser() AND resolved <= 2w`, "updated ASC"},
		{"created-after", "-7d", `assignee = currentUser() AND created >= -7d`, "updated ASC"},
		{"created-before", "2026-02-01", `assignee = currentUser() AND created <= "2026-02-01"`, "updated ASC"},
		{"updated-after", "-7d", `assignee = currentUser() AND updated >= -7d`, "updated ASC"},
		{"updated-before", "2026-02-01", `assignee = currentUser() AND updated <= "2026-02-01"`, "updated ASC"},
		{"watching", "true", `assignee = currentUser() AND watcher = currentUser()`, "updated ASC"},
		{"watched", "true", `assignee = currentUser() AND watcher = currentUser()`, "updated ASC"},
		{"order-by", "created", `assignee = currentUser()`, "created ASC"},
		{"sort", "created", `assignee = currentUser()`, "created ASC"},
		{"order", "created", `assignee = currentUser()`, "created ASC"},
		{"reverse", "true", `assignee = currentUser()`, "updated DESC"},
		{"desc", "true", `assignee = currentUser()`, "updated DESC"},
		{"jql", `project = ABC OR project = DEF ORDER BY key DESC`, `assignee = currentUser() AND (project = ABC OR project = DEF)`, "key DESC"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			rec := &mx3Recorder{}
			url := mx3Stub(t, mx3RecordOnly(rec))
			res := mx3Run(t, t.TempDir(), url, "issue", "mine", "--as-jql", "--"+tc.flag+"="+tc.value, "--output=json")
			want := tc.conditions + " ORDER BY " + tc.ordering
			if res.Exit != 0 || mx3Data(t, res)["jql"] != want {
				t.Fatalf("mine --%s ignored or widened scope: exit=%d stdout=%s", tc.flag, res.Exit, res.Stdout)
			}
			mx3RequireNoRequests(t, rec)
			Prove(t, "jira issue mine", tc.flag, "the real binary emitted the exact narrowed current-account query and ordering without making a request")
			Prove(t, "jira issue mine", "as-jql", "the real binary emitted the composed current-account query without searching or constructing an authenticated request")
		})
	}
}

func TestListNewKeyEpicAndDateFlagEffects(t *testing.T) {
	for _, tc := range []struct{ flag, value, want string }{
		{"key", "KEY-1..KEY-2", `key in ("KEY-1", "KEY-2")`},
		{"epic", "KEY-9", `parent in ("KEY-9")`},
		{"created", "2026-01-01..2026-02-01", `created >= "2026-01-01" AND created <= "2026-02-01"`},
		{"updated", "-7d", `updated >= -7d`},
		{"resolved", "..+2w", `resolved <= 2w`},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			body := mx3ListJQL(t, "--"+tc.flag+"="+tc.value)
			if body.JQL != tc.want {
				t.Fatalf("list --%s query=%q, want %q", tc.flag, body.JQL, tc.want)
			}
			Prove(t, "jira issue list", tc.flag, "the real search request contains the exact validated key, parent or date-range clause")
		})
	}
}

func TestMineFieldsLimitAndCursorReachSearch(t *testing.T) {
	for _, tokenFlag := range []string{"next-page-token", "cursor"} {
		t.Run(tokenFlag, func(t *testing.T) {
			rec := &mx3Recorder{}
			url := mx3Stub(t, mx3ListHandler(rec))
			res := mx3Run(t, t.TempDir(), url, "issue", "mine", "--fields=summary,status", "--limit=7", "--"+tokenFlag+"=resume", "--output=json")
			if res.Exit != 0 {
				t.Fatalf("mine read: %d %s", res.Exit, res.Stderr)
			}
			body := mx3SearchRequest(t, rec)
			if body.JQL != "assignee = currentUser() ORDER BY updated ASC" || body.NextPageToken != "resume" || body.MaxResults != 7 || !slices.Equal(body.Fields, []string{"summary", "status"}) {
				t.Fatalf("fields, limit, cursor or account scope changed: %+v", body)
			}
			Prove(t, "jira issue mine", "fields", "the search request replaces the default fields with exactly summary and status")
			Prove(t, "jira issue mine", "limit", "the search request asks for seven results instead of fifty")
			Prove(t, "jira issue mine", tokenFlag, "the search request resumes with the exact supplied cursor while retaining its required account scope")
		})
	}
}

func TestMineAllDrainsAndUnboundedRequiresAll(t *testing.T) {
	rec := &mx3Recorder{}
	url := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		var body mx3SearchBody
		if req.Method != http.MethodPost || req.Path != mx3SearchPath || json.Unmarshal(req.Body, &body) != nil || body.JQL != "assignee = currentUser() ORDER BY updated ASC" || body.MaxResults != 100 {
			t.Error("drain escaped scope or used bounded page size")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.NextPageToken == "" {
			_, _ = fmt.Fprint(w, `{"isLast":false,"nextPageToken":"next","issues":[{"key":"KEY-1"}]}`)
			return
		}
		if body.NextPageToken != "next" {
			t.Error("drain lost the server cursor")
		}
		_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"KEY-2"}]}`)
	}))
	res := mx3Run(t, t.TempDir(), url, "issue", "mine", "--all", "--output=json")
	if res.Exit != 0 || len(rec.byPath(mx3SearchPath)) != 2 {
		t.Fatalf("mine --all did not drain: %d %s", res.Exit, res.Stdout)
	}
	data := mx3Data(t, res)
	if issues, ok := data["issues"].([]any); !ok || len(issues) != 2 {
		t.Fatalf("drain lost issues: %s", res.Stdout)
	}
	Prove(t, "jira issue mine", "all", "the search asks for a hundred results, follows the native cursor and publishes both observed issues")
	noRequests := &mx3Recorder{}
	url = mx3Stub(t, mx3RecordOnly(noRequests))
	res = mx3Run(t, t.TempDir(), url, "issue", "mine", "--unbounded", "--output=json")
	mx3RequireRefusal(t, res, "flag_not_applicable", "unbounded")
	mx3RequireNoRequests(t, noRequests)
	Prove(t, "jira issue mine", "unbounded", "the flag is refused without --all before any request")
}

func TestMineCountAndColumnsUseSharedOwners(t *testing.T) {
	rec := &mx3Recorder{}
	url := mx3Stub(t, mx3ListHandler(rec))
	res := mx3Run(t, t.TempDir(), url, "issue", "mine", "--count", "--output=json")
	counts := rec.byPath(mx3CountPath)
	if res.Exit != 0 || len(counts) != 1 || len(rec.byPath(mx3SearchPath)) != 0 {
		t.Fatalf("mine count fetched issues: %d %s", res.Exit, res.Stdout)
	}
	var body mx3SearchBody
	if json.Unmarshal(counts[0].Body, &body) != nil || body.JQL != "assignee = currentUser() ORDER BY updated ASC" {
		t.Fatal("count lost current-account query")
	}
	count, ok := mx3Data(t, res)["count"].(map[string]any)
	if !ok || count["value"] != float64(7) || count["approximate"] != true {
		t.Fatal("count lost its honest approximation")
	}
	Prove(t, "jira issue mine", "count", "one account-scoped count request publishes seven as approximate without fetching issues")
	noRequests := &mx3Recorder{}
	url = mx3Stub(t, mx3RecordOnly(noRequests))
	res = mx3Run(t, t.TempDir(), url, "issue", "mine", "--fields=summary", "--columns=assignee", "--output=json")
	mx3RequireRefusal(t, res, "flag_conflict", "columns")
	mx3RequireNoRequests(t, noRequests)
	Prove(t, "jira issue mine", "columns", "requesting an unfetched column is refused before any request instead of rendering a misleading empty cell")
}
