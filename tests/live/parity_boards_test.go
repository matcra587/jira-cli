//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type parityBoardIdentity struct {
	ID       int64
	Name     string
	Type     json.RawMessage
	Self     json.RawMessage
	Projects []string
}

type parityBoardsExpected struct {
	Rows             []parityBoardIdentity
	Total, Limit     int
	Pages            int
	Source           string
	Cached           bool
	Earliest, Latest time.Time
	FetchedAt        string
}

// Fixed fixtures bind natural project-key order independently. Live comparisons
// use independently enumerated project membership; visibility is account-scoped.
func parityBoardsProjection(data []byte, lane string, want parityBoardsExpected) bool {
	if (lane != "before" && lane != "after") || !jsontext.Value(data).IsValid() {
		return false
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil || actual == nil {
		return false
	}
	var stamp string
	if json.Unmarshal(actual["fetched_at"], &stamp) != nil {
		return false
	}
	fetched, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || fetched.Before(want.Earliest.Truncate(time.Second)) || fetched.After(want.Latest) || want.FetchedAt != "" && stamp != want.FetchedAt {
		return false
	}
	rows := make([]map[string]any, 0, len(want.Rows))
	for _, board := range want.Rows {
		row := map[string]any{"id": board.ID, "name": board.Name, "project_keys": board.Projects}
		if lane == "after" {
			row["projects_complete"] = true
			if board.Type != nil {
				row["type"] = board.Type
			}
		} else {
			var kind string
			if board.Type != nil && json.Unmarshal(board.Type, &kind) != nil {
				return false
			}
			row["type"] = kind
		}
		rows = append(rows, row)
	}
	state := want.Source
	if want.Total == 0 {
		state = "empty"
	}
	expected := map[string]any{"boards": rows, "from_cache": want.Cached, "fetched_at": stamp, "truncated": false, "cache_state": state, "cache_source_state": want.Source, "cache_empty": want.Total == 0}
	if lane == "before" {
		expected["truncated_reason"] = ""
	} else {
		expected["count"], expected["cache_saved"], expected["pages_fetched"] = len(rows), true, want.Pages
		expected["project_failures"] = []any{}
		expected["pagination"] = map[string]any{"max_results": want.Limit, "total": want.Total, "is_last": len(rows) == want.Total, "complete": true}
	}
	encoded, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, encoded)
}

func parityBoardsData(t *testing.T, stdout []byte, lane, mode string, want parityBoardsExpected) []byte {
	t.Helper()
	if mode != "json" && lane != "before" {
		return stdout
	}
	var data map[string]json.RawMessage
	var pagination json.RawMessage
	if mode == "json" {
		data = parityAuthoringCleanData(t, stdout, lane, "boards.list")
	} else if !jsontext.Value(stdout).IsValid() || json.Unmarshal(stdout, &data) != nil || data == nil {
		t.Fatal("invalid compact board result")
	}
	if lane == "before" {
		if mode == "json" {
			var frame struct {
				Meta struct{ Pagination json.RawMessage }
			}
			if json.Unmarshal(stdout, &frame) != nil {
				t.Fatal("invalid legacy board frame")
			}
			pagination = frame.Meta.Pagination
		} else {
			// Legacy compact moves meta.pagination into its result object.
			pagination = data["pagination"]
			delete(data, "pagination")
		}
		expected, err := json.Marshal(map[string]any{"startAt": 0, "maxResults": want.Limit, "total": want.Total, "isLast": len(want.Rows) == want.Total})
		if err != nil || !parityAuthoringJSON(pagination, expected) {
			t.Fatalf("legacy board window differs: %s", pagination)
		}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func parityBoardsLegacyNegative(stdout []byte, exit int) bool {
	if exit != 3 || !jsontext.Value(stdout).IsValid() || parityValidateFrame(stdout, "before", "boards.list", exit) != nil {
		return false
	}
	var frame struct {
		Data     json.RawMessage
		Errors   []struct{ Code, Message string }
		Warnings json.RawMessage
	}
	return json.Unmarshal(stdout, &frame) == nil && string(frame.Data) == "null" && len(frame.Errors) == 1 && frame.Errors[0].Code == "validation_failed" && frame.Errors[0].Message == "validation: --limit must not be negative (got -1); 0 uses the default page size" && parityAuthoringJSON(frame.Warnings, []byte(`[]`))
}

func TestParityActualBinaryBoards(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
					var calls atomic.Int32
					var revision atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 1))
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						start, startErr := strconv.Atoi(query.Get("startAt"))
						if lane.name == "before" && r.URL.Path == "/rest/agile/1.0/board" && query.Get("startAt") == "" {
							start, startErr = 0, nil
						}
						wantQuery := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"50"}}
						if lane.name == "before" && start == 0 && r.URL.Path == "/rest/agile/1.0/board" {
							wantQuery.Del("startAt")
						}
						user, password, ok := r.BasicAuth()
						if err != nil || len(body) != 0 || queryErr != nil || startErr != nil || start < 0 || !reflect.DeepEqual(query, wantQuery) || r.Method != "GET" || !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							t.Error("board discovery changed its exact authenticated GET/query/body")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						var rows []string
						switch r.URL.Path {
						case "/rest/agile/1.0/board":
							if revision.Load() > 0 {
								rows = []string{`{"id":20,"name":"Fresh <&> café","type":"kanban","location":{"projectKey":"WRONG"}}`, `{"id":10,"name":"Empty relation"}`, `{"id":30,"name":"Scrum board","type":"scrum"}`}
							} else if !empty {
								rows = []string{`{"id":20,"name":"  Owned <&> café  ","type":null,"location":{"projectKey":"WRONG"}}`, `{"id":10,"name":"Empty relation","location":{"projectKey":"WRONG"}}`, `{"id":30,"name":"Scrum board","type":"scrum"}`}
							}
						case "/rest/agile/1.0/board/20/project":
							rows = []string{`{"id":"200","key":"PROJ10"}`, `{"id":"201","key":"PROJ2"}`}
							if revision.Load() > 0 {
								rows = []string{`{"id":"202","key":"NEW10"}`, `{"id":"203","key":"NEW2"}`}
							}
						case "/rest/agile/1.0/board/10/project":
						case "/rest/agile/1.0/board/30/project":
							rows = []string{`{"id":"300","key":"PAIR"}`}
						default:
							t.Error("unexpected synthetic board endpoint: ", r.URL.Path)
							http.Error(w, "unexpected endpoint", http.StatusBadRequest)
							return
						}
						if start > len(rows) || start == len(rows) && start != 0 {
							t.Error("board drain requested beyond its final page")
							http.Error(w, "unexpected offset", http.StatusBadRequest)
							return
						}
						value := "[]"
						if len(rows) > start {
							value = "[" + rows[start] + "]"
						}
						_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":1,"total":%d,"isLast":%t,"values":%s}`, start, len(rows), start+1 >= len(rows), value)
					}))
					defer server.Close()
					home := t.TempDir()
					began := time.Now()
					rows := []parityBoardIdentity{{ID: 10, Name: "Empty relation", Projects: []string{}}, {ID: 20, Name: "Owned <&> café", Type: json.RawMessage(`null`), Projects: []string{"PROJ2", "PROJ10"}}, {ID: 30, Name: "Scrum board", Type: json.RawMessage(`"scrum"`), Projects: []string{"PAIR"}}}
					pages, perFetch := 3, int32(7)
					if empty {
						rows, pages, perFetch = []parityBoardIdentity{}, 1, 1
					}
					stamp := ""
					for _, tc := range []struct {
						name, mode, source string
						args               []string
						limit              int
						cached, window     bool
					}{
						{"initial", "json", "missing", nil, 50, false, false},
						{"cached", "json", "fresh", nil, 50, true, false},
						{"window", "json", "fresh", []string{"--limit=1"}, 1, true, true},
						{"all-overrides-limit", "compact", "fresh", []string{"--all", "--limit=1"}, 1, true, false},
						{"zero-limit", "compact", "fresh", []string{"--limit=0"}, 50, true, false},
						{"negative-limit", "compact", "fresh", []string{"--limit=-1"}, 50, true, false},
						{"nonpositive-ttl", "json", "fresh", []string{"--ttl-minutes=0"}, 50, true, false},
						{"refresh", "json", "refresh", []string{"--refresh", "--all"}, 50, false, false},
						{"cached-after-refresh", "json", "fresh", []string{"--all"}, 50, true, false},
						{"unbounded", "compact", "refresh", []string{"--refresh", "--unbounded", "--all"}, 50, false, false},
						{"cached-after-unbounded", "compact", "fresh", []string{"--all"}, 50, true, false},
					} {
						t.Run(tc.name, func(t *testing.T) {
							if tc.name == "refresh" {
								revision.Store(1)
								rows = []parityBoardIdentity{{ID: 10, Name: "Empty relation", Projects: []string{}}, {ID: 20, Name: "Fresh <&> café", Type: json.RawMessage(`"kanban"`), Projects: []string{"NEW2", "NEW10"}}, {ID: 30, Name: "Scrum board", Type: json.RawMessage(`"scrum"`), Projects: []string{"PAIR"}}}
								pages, perFetch = 3, 7
							}
							beforeCalls := calls.Load()
							beforeFiles := parityLocalTree(t, home)
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Output: tc.mode}, []string{"JIRA_READ_ONLY=true"}, append([]string{"boards", "list"}, tc.args...)...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							id := fmt.Sprintf("C026.boards.empty=%t.%s.%s", empty, tc.name, tc.mode)
							if tc.cached && !reflect.DeepEqual(beforeFiles, parityLocalTree(t, home)) {
								t.Fatal("fresh board reuse or refusal changed owned files")
							}
							if lane.name == "before" && tc.name == "negative-limit" {
								if calls.Load() != beforeCalls || len(result.Stderr) != 0 || !parityBoardsLegacyNegative(result.Stdout, result.Exit) {
									t.Fatalf("legacy negative-limit refusal changed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
								}
								evidence.Checks[id] = append(evidence.Checks[id], lane.name)
								return
							}
							wantCalls := perFetch
							if tc.cached {
								wantCalls = 0
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load()-beforeCalls != wantCalls {
								t.Fatalf("board invocation failed: exit %d; calls %d want %d; stdout %s; stderr %s", result.Exit, calls.Load()-beforeCalls, wantCalls, result.Stdout, result.Stderr)
							}
							want := parityBoardsExpected{Rows: rows, Total: len(rows), Limit: tc.limit, Pages: pages, Source: tc.source, Cached: tc.cached, Earliest: began, Latest: time.Now()}
							if tc.window && len(rows) > 1 {
								want.Rows = rows[:1]
							}
							if tc.cached {
								want.FetchedAt = stamp
							}
							data := parityBoardsData(t, result.Stdout, lane.name, tc.mode, want)
							if !parityBoardsProjection(data, lane.name, want) {
								t.Fatalf("boards differ from independent identity/project/window/cache expectations: %s", result.Stdout)
							}
							var receipt struct {
								FetchedAt string `json:"fetched_at"`
							}
							if json.Unmarshal(data, &receipt) != nil {
								t.Fatal("missing board timestamp")
							}
							stamp = receipt.FetchedAt
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						})
					}
				})
			}
		})
	}
	saveParityLocal(t, "boards-fixture", evidence)
}

func parityNativeBoardPages(t *testing.T, p *parityRun, endpoint string) ([]map[string]json.RawMessage, int) {
	t.Helper()
	rows := []map[string]json.RawMessage{}
	start := 0
	for page := 1; page <= 100; page++ {
		read := rawRead(t, p.owner, "api", "get", endpoint, "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		var native struct {
			StartAt, MaxResults *int
			IsLast              *bool
			Values              []map[string]json.RawMessage
		}
		if !jsontext.Value(read.Response.JSON).IsValid() || json.Unmarshal(read.Response.JSON, &native) != nil || native.StartAt == nil || *native.StartAt != start || native.MaxResults == nil || *native.MaxResults < 1 || native.IsLast == nil || native.Values == nil || len(native.Values) > 50 || len(native.Values) > *native.MaxResults || len(rows)+len(native.Values) > 10000 {
			t.Fatal("independent native board walk lacks complete bounded page facts")
		}
		rows = append(rows, native.Values...)
		if *native.IsLast {
			return rows, page
		}
		if len(native.Values) == 0 || len(native.Values) != *native.MaxResults {
			t.Fatal("independent live fixture cannot establish equivalent legacy/current offsets")
		}
		start += len(native.Values)
	}
	t.Fatal("independent native board walk exceeded its page bound")
	return nil, 0
}

func parityNativeBoardIdentities(t *testing.T, p *parityRun) ([]parityBoardIdentity, int) {
	t.Helper()
	boards, pages := parityNativeBoardPages(t, p, "/rest/agile/1.0/board")
	rows := make([]parityBoardIdentity, 0, len(boards))
	seen := make(map[int64]bool)
	fixtureBoard := false
	for _, board := range boards {
		var row parityBoardIdentity
		if json.Unmarshal(board["id"], &row.ID) != nil || row.ID <= 0 || seen[row.ID] || json.Unmarshal(board["name"], &row.Name) != nil || strings.TrimSpace(row.Name) == "" {
			t.Fatal("independent board response lacks a unique native identity")
		}
		seen[row.ID] = true
		fixtureBoard = fixtureBoard || strconv.FormatInt(row.ID, 10) == os.Getenv("JIRA_LIVE_BOARD_ID")
		row.Self = board["self"]
		row.Name, row.Type, row.Projects = strings.TrimSpace(row.Name), board["type"], []string{}
		projects, _ := parityNativeBoardPages(t, p, fmt.Sprintf("/rest/agile/1.0/board/%d/project", row.ID))
		projectIDs := make(map[string]bool)
		keys := make(map[string]bool)
		for _, project := range projects {
			var id, key string
			if json.Unmarshal(project["id"], &id) != nil || id == "" || projectIDs[id] || json.Unmarshal(project["key"], &key) != nil || key == "" || strings.ContainsAny(key, " \t\n\r,()'\"") {
				t.Fatal("independent associated project lacks a unique safe native identity")
			}
			projectIDs[id] = true
			if keys[key] {
				// The legacy producer retains duplicates; this live fixture
				// requires a common unambiguous relation for both lanes.
				t.Fatal("independent associated project repeats a key")
			}
			keys[key] = true
			row.Projects = append(row.Projects, key)
		}
		sort.Strings(row.Projects)
		rows = append(rows, row)
	}
	if !fixtureBoard {
		t.Fatal("configured acceptance board is not independently visible")
	}
	return rows, pages
}

func TestLiveParityBoards(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			rows, pages := parityNativeBoardIdentities(t, p)
			sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
			began := time.Now()
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "boards", "list", "--refresh", "--all", "--limit=1")
			if exit != 0 || !frame.OK {
				t.Fatal("paired live board discovery failed; inspect retained receipts")
			}
			want := parityBoardsExpected{Rows: rows, Total: len(rows), Limit: 1, Pages: pages, Source: "refresh", Earliest: began, Latest: time.Now()}
			data := parityBoardsData(t, stdout, lane.Name, "json", want)
			// Native live Jira membership is independently sorted for comparison. The
			// synthetic fixture separately pins natural PROJ2/PROJ10 ordering.
			var actual map[string]json.RawMessage
			var actualRows []map[string]json.RawMessage
			if json.Unmarshal(data, &actual) != nil || json.Unmarshal(actual["boards"], &actualRows) != nil {
				t.Fatal("live board result lacks native rows")
			}
			for _, row := range actualRows {
				var keys []string
				if json.Unmarshal(row["project_keys"], &keys) != nil || keys == nil {
					t.Fatal("live board result lacks explicit project membership")
				}
				sort.Strings(keys)
				var err error
				row["project_keys"], err = json.Marshal(keys)
				if err != nil {
					t.Fatal(err)
				}
			}
			var err error
			actual["boards"], err = json.Marshal(actualRows)
			if err != nil {
				t.Fatal(err)
			}
			data, err = json.Marshal(actual)
			if err != nil || !parityBoardsProjection(data, lane.Name, want) {
				t.Fatal("board identities, associated projects or collection/cache/window facts differ from independent native observations")
			}
			p.check(t, lane, "C026.boards.native")
		})
	}
}

func TestParityBoardsProjectionRejectsChangedFacts(t *testing.T) {
	const before = `{"boards":[{"id":10,"name":"Independent board","type":"","project_keys":["PROJ2","PROJ10"]}],"from_cache":true,"fetched_at":"2026-01-01T00:00:00Z","truncated":false,"truncated_reason":"","cache_state":"fresh","cache_source_state":"fresh","cache_empty":false}`
	const after = `{"boards":[{"id":10,"name":"Independent board","project_keys":["PROJ2","PROJ10"],"projects_complete":true}],"count":1,"pagination":{"max_results":1,"total":2,"is_last":false,"complete":true},"from_cache":true,"fetched_at":"2026-01-01T00:00:00Z","cache_state":"fresh","cache_source_state":"fresh","cache_empty":false,"cache_saved":true,"pages_fetched":2,"truncated":false,"project_failures":[]}`
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := parityBoardsExpected{Rows: []parityBoardIdentity{{ID: 10, Name: "Independent board", Projects: []string{"PROJ2", "PROJ10"}}}, Total: 2, Limit: 1, Pages: 2, Source: "fresh", Cached: true, Earliest: stamp, Latest: stamp.Add(time.Second), FetchedAt: stamp.Format(time.RFC3339)}
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid := before
			if lane == "after" {
				valid = after
			}
			if !parityBoardsProjection([]byte(valid), lane, want) {
				t.Fatal("independent board receipt refused")
			}
			type change struct{ name, original, replacement string }
			cases := []change{
				{"identity", `"id":10`, `"id":20`},
				{"name", "Independent board", "Wrong board"},
				{"location-substitution", `"PROJ2","PROJ10"`, `"WRONG"`},
				{"project-order", `"PROJ2","PROJ10"`, `"PROJ10","PROJ2"`},
				{"null-projects", `["PROJ2","PROJ10"]`, `null`},
				{"cache-source", `"from_cache":true`, `"from_cache":false`},
				{"cache-state", `"cache_state":"fresh"`, `"cache_state":"missing"`},
				{"changed-timestamp", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z"},
				{"empty", `"cache_empty":false`, `"cache_empty":true`},
				{"truncated", `"truncated":false`, `"truncated":true`},
				{"duplicate", `"boards":`, `"boards":[],"boards":`},
			}
			if lane == "after" {
				cases = append(cases,
					change{"native-absence", `"id":10`, `"type":null,"id":10`},
					change{"count", `"count":1`, `"count":2`},
					change{"total", `"total":2`, `"total":1`},
					change{"window", `"is_last":false`, `"is_last":true`},
					change{"complete", `"complete":true`, `"complete":false`},
					change{"project-completeness", `"projects_complete":true`, `"projects_complete":false`},
					change{"saved", `"cache_saved":true`, `"cache_saved":false`},
					change{"pages", `"pages_fetched":2`, `"pages_fetched":1`},
					change{"null-failures", `"project_failures":[]`, `"project_failures":null`},
				)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if parityBoardsProjection([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), lane, want) {
						t.Fatal("changed native identity, associated projects or collection/cache facts earned board credit")
					}
				})
			}
		})
	}
}

func TestParityBoardsLegacyRefusalRejectsFalseSuccess(t *testing.T) {
	const valid = `{"ok":false,"meta":{"command":"boards.list","exit_code":3,"timestamp":"2026-01-01T00:00:00Z","request_id":"owned-fixture"},"data":null,"errors":[{"code":"validation_failed","message":"validation: --limit must not be negative (got -1); 0 uses the default page size"}],"warnings":[]}`
	if !parityBoardsLegacyNegative([]byte(valid), 3) {
		t.Fatal("independent legacy refusal refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"partial-success", `"data":null`, `"data":{"boards":[]}`},
		{"wrong-validation", "must not be negative", "must be positive"},
		{"wrong-code", "validation_failed", "auth_missing"},
		{"duplicate", `"data":null`, `"data":{},"data":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityBoardsLegacyNegative([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), 3) {
				t.Fatal("changed refusal earned legacy negative-limit credit")
			}
		})
	}
}
