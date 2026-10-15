//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parityBoardCacheCurrentPath(root, site, account string) (string, string) {
	identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len(account), account)
	namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	key := "scope-v2:" + namespace + ":v1:boards-list"
	return filepath.Join(root, "jira", "scope-v2-"+namespace, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key)))), key
}

// Fixed fixtures retain page order and natural project order. Live native
// enumeration may reorder rows: complete identity/project membership is checked
// separately, while output must still match this exact persisted payload.
func parityBoardCachePayload(raw []byte, lane string, rows []parityBoardIdentity, pages int, began, ended time.Time, live bool) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(raw, &actual) != nil {
		return false
	}
	var stamp string
	if json.Unmarshal(actual["fetched_at"], &stamp) != nil {
		return false
	}
	fetched, err := time.Parse(time.RFC3339Nano, stamp)
	if lane == "before" {
		began = began.Truncate(time.Second)
	}
	if err != nil || fetched.Before(began) || fetched.After(ended) {
		return false
	}
	member := "boards"
	if lane == "before" {
		member = "items"
	} else if lane != "after" {
		return false
	}
	var observed []map[string]json.RawMessage
	if json.Unmarshal(actual[member], &observed) != nil || observed == nil || len(observed) != len(rows) {
		return false
	}
	ordered := rows
	if live {
		byID := make(map[int64]parityBoardIdentity)
		for _, row := range rows {
			if _, duplicate := byID[row.ID]; duplicate {
				return false
			}
			byID[row.ID] = row
		}
		ordered = make([]parityBoardIdentity, 0, len(rows))
		for _, row := range observed {
			var id int64
			var keys []string
			if json.Unmarshal(row["id"], &id) != nil || json.Unmarshal(row["project_keys"], &keys) != nil || keys == nil {
				return false
			}
			want, found := byID[id]
			if !found {
				return false
			}
			membership := append([]string{}, keys...)
			sort.Strings(membership)
			expected := append([]string{}, want.Projects...)
			sort.Strings(expected)
			if !reflect.DeepEqual(membership, expected) {
				return false
			}
			want.Projects = keys
			ordered = append(ordered, want)
			delete(byID, id)
		}
	}
	expectedRows := make([]map[string]any, 0, len(ordered))
	for _, row := range ordered {
		expected := map[string]any{"id": row.ID, "name": row.Name, "project_keys": row.Projects}
		if lane == "after" {
			expected["projects_complete"] = true
			if row.Type != nil {
				expected["type"] = row.Type
			}
		} else {
			if row.Type != nil && string(row.Type) != "null" {
				expected["type"] = row.Type
			}
			if row.Self != nil && string(row.Self) != "null" {
				expected["self"] = row.Self
			}
		}
		expectedRows = append(expectedRows, expected)
	}
	want := map[string]any{"boards": expectedRows, "fetched_at": stamp, "pages_fetched": pages, "complete": true, "truncated": false, "project_failures": []any{}}
	if lane == "before" {
		want = map[string]any{"items": expectedRows, "fetched_at": stamp, "pages_fetched": pages, "ttl_seconds": 40320 * 60, "truncated": false, "truncated_reason": ""}
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(raw, expected)
}

func parityBoardCacheStored(t *testing.T, path, lane, identity string, rows []parityBoardIdentity, pages int, began time.Time, live bool) (time.Time, json.RawMessage) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("owned board cache is absent or nonregular")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !jsontext.Value(raw).IsValid() {
		t.Fatal("owned board wrapper is malformed")
	}
	var wrapper struct {
		Key, Profile, Resource string
		Schema                 int
		Written                time.Time
		FetchedAt              time.Time `json:"fetched_at"`
		Value                  []byte
		Data                   json.RawMessage
	}
	if json.Unmarshal(raw, &wrapper) != nil {
		t.Fatal("owned board wrapper cannot be decoded")
	}
	payload, written := wrapper.Value, wrapper.Written
	if lane == "before" {
		if wrapper.Profile != identity || wrapper.Resource != "boards" || wrapper.Schema != 1 {
			t.Fatal("legacy board cache changed its selected profile/schema")
		}
		payload, written = wrapper.Data, wrapper.FetchedAt
	} else if wrapper.Key != identity {
		t.Fatal("current board cache changed its selected site/account key")
	}
	if written.Before(began) || written.After(time.Now()) || !parityBoardCachePayload(payload, lane, rows, pages, began, written, live) {
		t.Fatal("stored board identities/projects or collection facts differ from independent native observations")
	}
	return written, payload
}

func parityBoardCacheReceipt(data []byte, lane, profile, state string, cached, force bool, rows, pages int, written time.Time, payload json.RawMessage, ended time.Time) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	want := map[string]any{"profile": profile, "primed": !cached, "from_cache": cached, "boards_count": rows, "fetched_at": written.UTC().Format(time.RFC3339), "ttl_seconds": 40320 * 60, "truncated": false, "truncated_reason": "", "cache_state": state, "cache_source_state": state, "cache_empty": rows == 0}
	switch lane {
	case "before":
		if rows == 0 {
			want["cache_state"] = "empty"
		}
	case "after":
		var actual map[string]json.RawMessage
		var stamp string
		var age int64
		if json.Unmarshal(data, &actual) != nil || json.Unmarshal(actual["written_at"], &stamp) != nil || json.Unmarshal(actual["age_seconds"], &age) != nil {
			return false
		}
		ack, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || ack.Before(written) || ack.After(ended) || cached && !ack.Equal(written) || age < 0 || age > int64(ended.Sub(written).Seconds())+1 || !cached && age != 0 {
			return false
		}
		want = map[string]any{"dry_run": false, "resource": "boards", "items": []any{}, "payload": payload, "count": rows, "pages": pages, "complete": true, "ttl_minutes": 40320, "force": force, "unbounded": false, "would_fetch": !cached, "attempted": !cached, "from_cache": cached, "cache_state": state, "saved": !cached, "written_at": stamp, "age_seconds": age}
	default:
		return false
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func TestParityActualBinaryBoardCache(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, empty := range []bool{false, true} {
			for _, mode := range []string{"json", "compact"} {
				t.Run(fmt.Sprintf("%s/empty=%t/%s", lane.name, empty, mode), func(t *testing.T) {
					if lane.binary == "" {
						t.Skip("requires selected actual binary")
					}
					var calls, revision atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 1))
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						start, startErr := strconv.Atoi(query.Get("startAt"))
						wantQuery := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"50"}}
						if lane.name == "before" && r.URL.Path == "/rest/agile/1.0/board" && query.Get("startAt") == "" {
							start, startErr = 0, nil
							wantQuery.Del("startAt")
						}
						user, password, authenticated := r.BasicAuth()
						if err != nil || len(body) != 0 || queryErr != nil || startErr != nil || start < 0 || !reflect.DeepEqual(query, wantQuery) || r.Method != "GET" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							t.Error("board primer changed its exact authenticated GET/query/body")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						rows := []string{}
						switch r.URL.Path {
						case "/rest/agile/1.0/board":
							if !empty {
								rows = []string{`{"id":20,"name":"  Owned <&> café  ","type":null,"self":"https://native.invalid/board/20","location":{"projectKey":"WRONG"}}`, `{"id":10,"name":"Empty relation"}`}
							}
						case "/rest/agile/1.0/board/20/project":
							rows = []string{`{"id":"200","key":"PROJ10"}`, `{"id":"201","key":"PROJ2"}`}
						case "/rest/agile/1.0/board/10/project":
						default:
							t.Error("board primer entered an unexpected endpoint")
							http.Error(w, "wrong synthetic endpoint", http.StatusBadRequest)
							return
						}
						if start > len(rows) || start == len(rows) && start != 0 {
							t.Error("board primer continued beyond a final page")
							http.Error(w, "wrong synthetic offset", http.StatusBadRequest)
							return
						}
						value := "[]"
						if start < len(rows) {
							value = "[" + rows[start] + "]"
						}
						if revision.Load() > 0 {
							value = strings.ReplaceAll(strings.ReplaceAll(value, "Owned", "Refreshed"), "PROJ", "NEXT")
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":1,"total":%d,"isLast":%t,"values":%s}`, start, len(rows), start+1 >= len(rows), value)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					began := time.Now()
					for _, phase := range []string{"initial", "cached", "forced", "cached-after-force"} {
						if !t.Run(phase, func(t *testing.T) {
							cached, force := strings.Contains(phase, "cached"), phase == "forced"
							if force {
								revision.Store(1)
							}
							rows := []parityBoardIdentity{{ID: 20, Name: "Owned <&> café", Type: json.RawMessage(`null`), Self: json.RawMessage(`"https://native.invalid/board/20"`), Projects: []string{"PROJ2", "PROJ10"}}, {ID: 10, Name: "Empty relation", Projects: []string{}}}
							pages, perFetch := 2, int32(5)
							if empty {
								rows, pages, perFetch = []parityBoardIdentity{}, 1, 1
							} else if revision.Load() > 0 {
								rows[0].Name, rows[0].Projects = "Refreshed <&> café", []string{"NEXT2", "NEXT10"}
							}
							args := []string{"cache", "boards"}
							if force {
								flag := "--force"
								if lane.name == "before" {
									flag = "--refresh"
								}
								args = append(args, flag)
							}
							before, beforeCalls := parityLocalTree(t, home), calls.Load()
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							invoked := time.Now()
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							wantCalls := perFetch
							if cached {
								wantCalls = 0
								if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
									t.Fatal("cached board primer changed owned files")
								}
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load()-beforeCalls != wantCalls {
								t.Fatalf("board primer failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load()-beforeCalls, wantCalls, result.Stdout, result.Stderr)
							}
							identity, path := parityMetadataLegacyLocation(home, server.URL, "boards")
							if lane.name == "after" {
								path, identity = parityBoardCacheCurrentPath(home, server.URL, "fixture@example.invalid")
							}
							readBegan := began
							if !cached {
								readBegan = invoked
							}
							written, payload := parityBoardCacheStored(t, path, lane.name, identity, rows, pages, readBegan, false)
							if !cached && written.Before(invoked) {
								t.Fatal("fresh board primer reused an old publication")
							}
							state := "fresh"
							if phase == "initial" {
								state = "missing"
							} else if force && lane.name == "before" {
								state = "refresh"
							}
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "cache.boards"))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !parityBoardCacheReceipt(data, lane.name, "fixture", state, cached, force, len(rows), pages, written, payload, time.Now()) {
								t.Fatalf("board primer lost independently stored identities/projects or collection/publication facts: %s", result.Stdout)
							}
							id := fmt.Sprintf("C028.boards.primer.empty=%t.%s.%s", empty, phase, mode)
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
							if phase == "cached-after-force" {
								before, beforeCalls := parityLocalTree(t, home), calls.Load()
								if err := parityFingerprint(lane.binary, lane.digest); err != nil {
									t.Fatal(err)
								}
								result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config}, []string{"JIRA_READ_ONLY=true"}, "boards", "list", "--all")
								evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
								if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != beforeCalls || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
									t.Fatal("board-list consumer failed to reuse the actual primer without requests or file changes")
								}
								sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
								var native struct {
									FetchedAt string `json:"fetched_at"`
								}
								if json.Unmarshal(payload, &native) != nil {
									t.Fatal("validated payload lost its native read time")
								}
								if lane.name == "before" {
									native.FetchedAt = written.UTC().Format(time.RFC3339)
								}
								want := parityBoardsExpected{Rows: rows, Total: len(rows), Limit: 50, Pages: pages, Source: "fresh", Cached: true, Earliest: began, Latest: time.Now(), FetchedAt: native.FetchedAt}
								if !parityBoardsProjection(parityBoardsData(t, result.Stdout, lane.name, "json", want), lane.name, want) {
									t.Fatal("board-list consumer differs from independently expected producer identities/projects")
								}
								id := fmt.Sprintf("C026.boards.primer-consumer.empty=%t.%s", empty, mode)
								evidence.Checks[id] = append(evidence.Checks[id], lane.name)
							}
						}) {
							return
						}
					}
				})
			}
		}
	}
	saveParityLocal(t, "board-cache-fixture", evidence)
}

func TestLiveParityBoardCache(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			rows, pages := parityNativeBoardIdentities(t, p)
			root := p.owner.cacheDir
			path, identity := parityBoardCacheCurrentPath(root, p.owner.manifest.BaseURL, p.owner.account)
			force := "--force"
			if lane.Name == "before" {
				root = filepath.Join(p.owner.dir, "cache-before")
				var history string
				history, identity = parityLiveHistoryPath(p, lane)
				path = filepath.Join(filepath.Dir(history), "boards.json")
				force = "--refresh"
			}
			began := time.Now()
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + p.owner.account}, "cache", "boards", force)
			if exit != 0 || !frame.OK {
				t.Fatal("native board primer failed; inspect retained receipts")
			}
			written, payload := parityBoardCacheStored(t, path, lane.Name, identity, rows, pages, began, true)
			state := "missing"
			if lane.Name == "before" {
				state = "refresh"
			}
			data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.boards"))
			if err != nil || !parityBoardCacheReceipt(data, lane.Name, lane.Profile, state, false, true, len(rows), pages, written, payload, time.Now()) {
				t.Fatal("native board primer differs from independent identities/projects and stored/output facts")
			}
			p.check(t, lane, "C028.boards.native")
			before := parityLocalTree(t, root)
			frame, stdout, exit = p.invoke(t, lane, []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + p.owner.account}, "cache", "boards")
			if exit != 0 || !frame.OK || !reflect.DeepEqual(before, parityLocalTree(t, root)) {
				t.Fatal("native cached board primer failed or changed owned files")
			}
			data, err = json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.boards"))
			if err != nil || !parityBoardCacheReceipt(data, lane.Name, lane.Profile, "fresh", true, false, len(rows), pages, written, payload, time.Now()) {
				t.Fatal("native cached board receipt changed its stored values or timestamps")
			}
			p.check(t, lane, "C028.boards.native-cached")
		})
	}
}

func TestParityBoardCachePayloadRejectsChangedNativeFacts(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []parityBoardIdentity{{ID: 20, Name: "Independent board", Type: json.RawMessage(`null`), Self: json.RawMessage(`"https://native.invalid/board/20"`), Projects: []string{"PROJ2", "PROJ10"}}}
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid := `{"fetched_at":"2026-01-01T00:00:00Z","ttl_seconds":2419200,"truncated":false,"truncated_reason":"","pages_fetched":2,"items":[{"id":20,"name":"Independent board","self":"https://native.invalid/board/20","project_keys":["PROJ2","PROJ10"]}]}`
			if lane == "after" {
				valid = `{"boards":[{"id":20,"name":"Independent board","type":null,"project_keys":["PROJ2","PROJ10"],"projects_complete":true}],"fetched_at":"2026-01-01T00:00:00Z","pages_fetched":2,"complete":true,"truncated":false,"project_failures":[]}`
			}
			if !parityBoardCachePayload([]byte(valid), lane, rows, 2, stamp, stamp.Add(time.Second), false) {
				t.Fatal("independent native board payload fixture refused")
			}
			for _, mutation := range []struct{ name, original, replacement string }{
				{"identity", `"id":20`, `"id":10`},
				{"name", "Independent board", "Wrong board"},
				{"project-membership", `"PROJ2","PROJ10"`, `"OTHER"`},
				{"project-order", `"PROJ2","PROJ10"`, `"PROJ10","PROJ2"`},
				{"page-count", `"pages_fetched":2`, `"pages_fetched":1`},
				{"truncation", `"truncated":false`, `"truncated":true`},
				{"wrong-read-time", "2026-01-01T00:00:00Z", "2025-12-31T23:59:59Z"},
				{"duplicate", `"fetched_at":`, `"fetched_at":"ignored","fetched_at":`},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					changed := strings.Replace(valid, mutation.original, mutation.replacement, 1)
					if changed == valid || parityBoardCachePayload([]byte(changed), lane, rows, 2, stamp, stamp.Add(time.Second), false) {
						t.Fatal("changed native board payload earned stored acceptance")
					}
				})
			}
		})
	}
}

func TestParityBoardCacheReceiptRejectsChangedAcknowledgements(t *testing.T) {
	written := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			payload := json.RawMessage(`{"boards":[{"id":20,"name":"Independent board","type":null,"project_keys":[],"projects_complete":true}],"fetched_at":"2026-01-01T00:00:00Z","pages_fetched":2,"complete":true,"truncated":false,"project_failures":[]}`)
			valid := `{"profile":"fixture","primed":true,"from_cache":false,"boards_count":1,"fetched_at":"2026-01-01T00:00:00Z","ttl_seconds":2419200,"truncated":false,"truncated_reason":"","cache_state":"refresh","cache_source_state":"refresh","cache_empty":false}`
			state, countKey := "refresh", "boards_count"
			if lane == "after" {
				state, countKey = "missing", "count"
				valid = `{"dry_run":false,"resource":"boards","items":[],"payload":` + string(payload) + `,"count":1,"pages":2,"complete":true,"ttl_minutes":40320,"force":true,"unbounded":false,"would_fetch":true,"attempted":true,"from_cache":false,"cache_state":"missing","saved":true,"written_at":"2026-01-01T00:00:00.000000001Z","age_seconds":0}`
			}
			accept := func(raw []byte) bool {
				return parityBoardCacheReceipt(raw, lane, "fixture", state, false, true, 1, 2, written, payload, written.Add(time.Second))
			}
			if !accept([]byte(valid)) {
				t.Fatal("independent board publication receipt refused")
			}
			type change struct {
				name, key string
				value     any
			}
			mutations := []change{{"wrong-count", countKey, 2}, {"false-cache-hit", "from_cache", true}, {"wrong-state", "cache_state", "fresh"}}
			if lane == "before" {
				mutations = append(mutations, change{"false-prime", "primed", false})
			} else {
				for _, key := range []string{"saved", "attempted", "complete", "would_fetch", "force"} {
					mutations = append(mutations, change{"false-" + key, key, false})
				}
				mutations = append(mutations, change{"lost-native-payload", "payload", nil})
			}
			for _, mutation := range mutations {
				t.Run(mutation.name, func(t *testing.T) {
					var changed map[string]any
					if json.Unmarshal([]byte(valid), &changed) != nil {
						t.Fatal("independent receipt cannot be decoded")
					}
					changed[mutation.key] = mutation.value
					raw, err := json.Marshal(changed)
					if err != nil || accept(raw) {
						t.Fatal("changed acknowledgement earned board publication credit")
					}
				})
			}
		})
	}
}
