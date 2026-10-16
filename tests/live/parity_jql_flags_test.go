//go:build live

package live

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type parityJQLFlagCase struct {
	name          string
	args          []string
	before, after string
}

func parityJQLFlagCases() []parityJQLFlagCase {
	// These are literal query contracts. Neither lane's builder generates the
	// other lane's expectation; clause ordering and quoting differ explicitly.
	cases := []parityJQLFlagCase{
		{"project", []string{"--project=KEY"}, `project = KEY`, `project in ("KEY")`},
		{"project-csv", []string{"--project=KEY,OTHER"}, `project in (KEY, OTHER)`, `project in ("KEY", "OTHER")`},
		{"project-repeat", []string{"--project=KEY", "--project=OTHER"}, `project in (KEY, OTHER)`, `project in ("KEY", "OTHER")`},
		{"key-range", []string{"--key=PAIR-1..PAIR-3"}, `key in (PAIR-1, PAIR-2, PAIR-3)`, `key in ("PAIR-1", "PAIR-2", "PAIR-3")`},
		{"epic", []string{"--epic=PAIR-7,PAIR-8"}, `parent in (PAIR-7, PAIR-8)`, `parent in ("PAIR-7", "PAIR-8")`},
		{"assignee-me", []string{"--assignee=me"}, `assignee = currentUser()`, `assignee = currentUser()`},
		{"assignee-none", []string{"--assignee=none"}, `assignee is EMPTY`, `assignee is EMPTY`},
		{"assignee-literal", []string{"--assignee=owned:account"}, `assignee = "owned:account"`, `assignee in ("owned:account")`},
		{"reporter-me", []string{"--reporter=me"}, `reporter = currentUser()`, `reporter = currentUser()`},
		{"reporter-none", []string{"--reporter=none"}, `reporter is EMPTY`, `reporter is EMPTY`},
		{"reporter-literal", []string{"--reporter=owned:reporter"}, `reporter = "owned:reporter"`, `reporter in ("owned:reporter")`},
		{"status-csv", []string{"--status=Open,In Progress"}, `status in (Open, "In Progress")`, `status in ("Open", "In Progress")`},
		{"status-category-exclusion", []string{"--status=>=In Progress", "--status=!Abandoned"}, `statusCategory in ("In Progress", Done) AND status != Abandoned`, `statusCategory in ("In Progress", "Done") AND status != "Abandoned"`},
		{"status-alternatives", []string{"--status=Open", "--status=>=In Progress", "--status=!Abandoned"}, `(status = Open OR statusCategory in ("In Progress", Done)) AND status != Abandoned`, `(status in ("Open") OR statusCategory in ("In Progress", "Done")) AND status != "Abandoned"`},
		{"priority", []string{"--priority=High,Low"}, `priority in (High, Low)`, `priority in ("High", "Low")`},
		{"label-quoted-csv", []string{`--label="team,one",second`}, `labels in ("team,one", second)`, `labels in ("team,one", "second")`},
		{"type-repeat", []string{"--type=Task", "--type=Bug"}, `issuetype in (Task, Bug)`, `issuetype in ("Task", "Bug")`},
		{
			"combined",
			[]string{"--project=KEY", "--key=PAIR-1", "--epic=PAIR-7", "--assignee=me", "--reporter=none", "--status=Open", "--priority=High", "--label=owned", "--type=Task", "--created=2026-01-01", "--updated=-7d", "--resolved=..2026-10-01"},
			`project = KEY AND key = PAIR-1 AND assignee = currentUser() AND reporter is EMPTY AND parent = PAIR-7 AND status = Open AND priority = High AND labels = owned AND issuetype = Task AND updated >= -7d AND created >= "2026-01-01" AND resolved <= "2026-10-01"`,
			`project in ("KEY") AND key in ("PAIR-1") AND parent in ("PAIR-7") AND assignee = currentUser() AND status in ("Open") AND issuetype in ("Task") AND labels in ("owned") AND reporter is EMPTY AND priority in ("High") AND created >= "2026-01-01" AND updated >= -7d AND resolved <= "2026-10-01"`,
		},
	}
	for _, field := range []string{"created", "updated", "resolved"} {
		for _, date := range []struct{ name, value, before, after string }{
			{"relative", "-7d", ` >= -7d`, ` >= -7d`},
			{"future-relative", "+2w", ` >= +2w`, ` >= 2w`},
			{"absolute", "2026-01-01", ` >= "2026-01-01"`, ` >= "2026-01-01"`},
			{"comparator", "<2026-10-01", ` < "2026-10-01"`, ` < "2026-10-01"`},
			{"closed-range", "2026-01-01..2026-10-01", ` >= "2026-01-01" AND %s <= "2026-10-01"`, ` >= "2026-01-01" AND %s <= "2026-10-01"`},
			{"open-start", "..2026-10-01", ` <= "2026-10-01"`, ` <= "2026-10-01"`},
			{"open-end", "2026-01-01..", ` >= "2026-01-01"`, ` >= "2026-01-01"`},
		} {
			before := field + strings.ReplaceAll(date.before, "%s", field)
			after := field + strings.ReplaceAll(date.after, "%s", field)
			cases = append(cases, parityJQLFlagCase{field + "-" + date.name, []string{"--" + field + "=" + date.value}, before, after})
		}
	}
	return cases
}

func TestLiveParityJQLPositiveDateGrammar(t *testing.T) {
	p := newParityRun(t)
	queries := []string{
		"created >= +2w ORDER BY key ASC", "created >= 2w ORDER BY key ASC",
		"updated >= +2w ORDER BY key ASC", "updated >= 2w ORDER BY key ASC",
		"resolved >= +2w ORDER BY key ASC", "resolved >= 2w ORDER BY key ASC",
	}
	request, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(p.owner.dir, "positive-date-grammar.json")
	if err := os.WriteFile(input, request, 0o600); err != nil {
		t.Fatal(err)
	}
	native := rawRead(t, p.owner, "api", "post", "/rest/api/3/jql/parse", "--query=validation=strict", "--input", input)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			args := append([]string{"jql", "validate", "--mode=strict", "--"}, queries...)
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=false"}, args...)
			if exit != 0 || !frame.OK {
				t.Fatal("positive-period parser diagnostics became a command failure")
			}
			parityAuthoringCleanData(t, stdout, lane.Name, "jql.validate")
			data := parityOriginalFrameData(stdout)
			if !parityParseProjection(data, native.Response.JSON, queries, "strict", lane.Name) {
				t.Fatal("positive-period diagnostics differ from independent native Jira parsing")
			}
			var result struct{ Queries []struct{ Valid *bool } }
			if json.Unmarshal(data, &result) != nil || len(result.Queries) != len(queries) {
				t.Fatal("positive-period parser omitted an admitted query")
			}
			for i, valid := range []bool{false, true, false, true, false, true} {
				if result.Queries[i].Valid == nil || *result.Queries[i].Valid != valid {
					t.Fatalf("unexpected Jira positive-period grammar at query %d", i)
				}
			}
			p.check(t, lane, "C059.parser.positive-date-grammar.native")
		})
	}
	if observed, err := os.ReadFile(input); err != nil || !reflect.DeepEqual(observed, request) {
		t.Fatal("parser changed its owned input")
	}
}

func parityJQLFlagData(t *testing.T, result parityFixtureResult, lane, mode, site, query string) {
	t.Helper()
	if result.Exit != 0 || len(result.Stderr) != 0 {
		t.Fatalf("local JQL failed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
	}
	data := result.Stdout
	if mode == "json" {
		parityAuthoringCleanData(t, result.Stdout, lane, "jql.build")
		data = parityOriginalFrameData(result.Stdout)
	}
	want := map[string]any{"jql": query, "url": site + "/issues/?jql=" + url.QueryEscape(query), "precedence": "none", "board_scope": map[string]any{"applied": false}}
	expected, err := json.Marshal(want)
	if err != nil || !parityAuthoringJSON(data, expected) {
		t.Fatalf("local JQL differs from independently authored query, URL or scope: %s", result.Stdout)
	}
}

func TestParityActualBinaryJQLFilterFlags(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string), Differences: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "jql-filter-flags-local", evidence) })
	for _, lane := range []parityLane{
		{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")},
		{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")},
	} {
		if lane.Binary == "" {
			t.Skip("requires both admitted binaries")
		}
		for _, mode := range []string{"json", "compact"} {
			for _, tc := range parityJQLFlagCases() {
				t.Run(lane.Name+"/"+mode+"/"+tc.name, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						http.Error(w, "local JQL entered HTTP", http.StatusBadGateway)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					before := parityLocalTree(t, home)
					args := append([]string{"jql", "build"}, tc.args...)
					args = append(args, "--order-by=key", "--desc=false")
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("local JQL contacted Jira or changed owned storage")
					}
					query := tc.after
					if lane.Name == "before" {
						query = tc.before
					}
					parityJQLFlagData(t, result, lane.Name, mode, server.URL, query+" ORDER BY key ASC")
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					id := "C057.filters." + tc.name + "." + mode
					checks := evidence.Checks
					if strings.HasSuffix(tc.name, "-future-relative") {
						checks = evidence.Differences
					}
					checks[id] = append(checks[id], lane.Name)
				})
			}
		}
	}
}

func TestParityActualBinaryIssueQueryFilterFlags(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string), Differences: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "issue-query-filter-flags-local", evidence) })
	for _, lane := range []parityLane{
		{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")},
		{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")},
	} {
		if lane.Binary == "" {
			t.Skip("requires both admitted binaries")
		}
		for _, command := range []string{"list", "mine"} {
			for _, mode := range []string{"json", "compact"} {
				for _, tc := range parityJQLFlagCases() {
					// Neither mine layout permits an assignee override. Only
					// the current layout exposes the separate reporter filter.
					if command == "mine" && (strings.HasPrefix(tc.name, "assignee-") || tc.name == "combined" || lane.Name == "before" && strings.HasPrefix(tc.name, "reporter-")) {
						continue
					}
					t.Run(lane.Name+"/"+command+"/"+mode+"/"+tc.name, func(t *testing.T) {
						if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
							t.Fatal(err)
						}
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							calls.Add(1)
							http.Error(w, "local query entered HTTP", http.StatusBadGateway)
						}))
						defer server.Close()
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.Name, server.URL)
						before := parityLocalTree(t, home)
						args := append([]string{"issue", command, "--as-jql", "--order-by=key"}, tc.args...)
						if lane.Name == "before" {
							args = append(args, "--desc=false")
						}
						result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatalf("local issue query failed or changed owned state: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						query := tc.after
						if lane.Name == "before" {
							query = tc.before
						}
						if command == "mine" {
							// Project/key precede the fixed account scope in both
							// layouts; only the current layout places parent first.
							if strings.HasPrefix(query, "project ") || strings.HasPrefix(query, "key ") || lane.Name == "after" && strings.HasPrefix(query, "parent ") {
								query += " AND assignee = currentUser()"
							} else {
								query = "assignee = currentUser() AND " + query
							}
						}
						query += " ORDER BY key ASC"
						operation := "issue." + command
						want := map[string]any{"issues": []any{}, "pagination": map[string]any{"is_last": false, "returned": 0}, "truncated": false, "jql": query}
						if lane.Name == "before" {
							operation = "issue.list.jql"
							want = map[string]any{"issues": []any{}, "detail": false, "jql": query, "precedence": "none", "board_scope": map[string]any{"applied": false}, "url": server.URL + "/issues/?jql=" + url.QueryEscape(query)}
						}
						parityCacheLocalData(t, result, lane.Name, mode, operation, want)
						if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
							t.Fatal(err)
						}
						id := "I018"
						if command == "mine" {
							id = "I019"
						}
						check := id + ".filters." + tc.name + "." + mode
						checks := evidence.Checks
						if strings.HasSuffix(tc.name, "-future-relative") {
							checks = evidence.Differences
						}
						checks[check] = append(checks[check], lane.Name)
					})
				}
			}
		}
	}
}
