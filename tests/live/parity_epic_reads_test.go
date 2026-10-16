//go:build live

package live

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type parityEpicReadFixture struct {
	Name             string
	Native, Children []json.RawMessage
	Expected         map[string]map[string]json.RawMessage
}

func parityEpicReadFixtures(t *testing.T) []parityEpicReadFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/parity-epic-read-oracles.json")
	var fixtures []parityEpicReadFixture
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixtures) != nil || len(fixtures) != 5 {
		t.Fatal("independent epic read fixtures unavailable")
	}
	return fixtures
}

func parityEpicReadOutput(t *testing.T, stdout []byte, lane, mode, operation string, unknown, legacyMore bool) []byte {
	t.Helper()
	if !jsontext.Value(stdout).IsValid() {
		t.Fatal("epic read output is not strict original JSON")
	}
	data := stdout
	pagination := map[string]any{"startAt": 0, "maxResults": 50, "isLast": !legacyMore}
	if legacyMore {
		pagination["nextCursor"] = "next-owned"
	}
	expectedPage, err := json.Marshal(pagination)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "json" {
		projected := parityAuthoringData(t, stdout, lane, "epic."+operation, 0)
		var frame map[string]json.RawMessage
		if json.Unmarshal(stdout, &frame) != nil {
			t.Fatal("invalid epic envelope")
		}
		var meta map[string]json.RawMessage
		if json.Unmarshal(frame["meta"], &meta) != nil {
			t.Fatal("invalid epic metadata")
		}
		if lane == "before" && operation == "list" {
			if !parityAuthoringJSON(meta["pagination"], expectedPage) {
				t.Fatal("legacy epic list lost native paging facts")
			}
		} else if _, present := meta["pagination"]; present {
			t.Fatal("epic read invented legacy global pagination")
		}
		warnings := `[]`
		if lane == "after" && operation == "board" && unknown {
			warnings = `[{"type":"epic_counts_incomplete","message":"Epic status totals are incomplete; inspect epic pagination and each row's children state.","lossy":true}]`
		}
		if !parityAuthoringJSON(frame["warnings"], []byte(warnings)) {
			t.Fatal("epic read lost exact count completeness warning")
		}
		var err error
		data, err = json.Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "compact" && lane == "before" && operation == "list" {
		var compact map[string]json.RawMessage
		if json.Unmarshal(data, &compact) != nil || !parityAuthoringJSON(compact["pagination"], expectedPage) {
			t.Fatal("legacy compact epic list lost native paging facts")
		}
		delete(compact, "pagination")
		data, err = json.Marshal(compact)
		if err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func TestParityActualBinaryEpicReads(t *testing.T) {
	fixtures := parityEpicReadFixtures(t)
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, fixture := range fixtures {
			for _, operation := range []string{"list", "board"} {
				if operation == "list" && (fixture.Name == "unknown-status" || fixture.Name == "child-continuation") {
					continue
				}
				for _, mode := range []string{"json", "compact"} {
					t.Run(lane.name+"/"+fixture.Name+"/"+operation+"/"+mode, func(t *testing.T) {
						if lane.binary == "" {
							t.Skip("requires selected actual binary")
						}
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							call := int(calls.Add(1))
							body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
							user, password, auth := r.BasicAuth()
							if err != nil || len(body) > 8192 || !jsontext.Value(body).IsValid() || r.Method != "POST" || r.URL.Path != "/rest/api/3/search/jql" || r.URL.RawQuery != "" || !auth || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
								t.Error("epic reads changed authenticated search transport")
								http.Error(w, "wrong request", 400)
								return
							}
							epicPages := 1
							if fixture.Name == "continuation" && lane.name == "after" {
								epicPages = 2
							}
							want := map[string]any{"jql": "issuetype = Epic", "maxResults": 50, "fields": []string{"summary", "status"}}
							rows, last, next := fixture.Native, true, ""
							if call <= epicPages {
								if fixture.Name == "continuation" {
									if call == 1 {
										rows, last, next = fixture.Native[:1], false, "next-owned"
									} else {
										rows = fixture.Native[1:]
										want["nextPageToken"] = "next-owned"
										want["maxResults"] = 49
									}
								}
							} else {
								child := call - epicPages
								childToken := ""
								secondChildPage := fixture.Name == "child-continuation" && lane.name == "after" && child == 2
								if fixture.Name == "child-continuation" && lane.name == "after" && child >= 2 {
									child--
									if secondChildPage {
										childToken = "child-owned"
									}
								}
								epicCount := len(fixture.Native)
								if fixture.Name == "continuation" && lane.name == "before" {
									epicCount = 1
								}
								if operation != "board" || child > epicCount {
									t.Error("epic reads requested an unplanned page")
									http.Error(w, "unplanned page", 400)
									return
								}
								key := fmt.Sprintf("PAIR-%d", child)
								if lane.name == "after" {
									want = map[string]any{"jql": `parent = "` + key + `"`, "maxResults": 100, "fields": []string{"status"}}
								} else {
									want = map[string]any{"jql": "parent = " + key, "fields": []string{"key", "summary", "status", "assignee", "priority", "created", "updated"}}
								}
								rows = []json.RawMessage{}
								if child == 1 {
									rows = fixture.Children
									if fixture.Name == "child-continuation" {
										if secondChildPage {
											rows = rows[1:]
										} else {
											rows, last, next = rows[:1], false, "child-owned"
										}
									}
								}
								if childToken != "" {
									want["nextPageToken"] = childToken
								}
							}
							expected, err := json.Marshal(want)
							if err != nil || !parityAuthoringJSON(body, expected) {
								t.Errorf("epic read changed exact query, fields, size or token: %s", body)
								http.Error(w, "wrong body", 400)
								return
							}
							response := map[string]any{"issues": rows, "isLast": last}
							if next != "" {
								response["nextPageToken"] = next
							}
							encoded, err := json.Marshal(response)
							if err != nil {
								t.Error(err)
								return
							}
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write(encoded)
						}))
						defer server.Close()
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, "epic", operation)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						count := len(fixture.Native)
						wantCalls := 1
						if fixture.Name == "continuation" {
							if lane.name == "before" {
								count = 1
							} else {
								wantCalls = 2
							}
						}
						if operation == "board" {
							wantCalls += count
							if fixture.Name == "child-continuation" && lane.name == "after" {
								wantCalls++
							}
						}
						if result.Exit != 0 || len(result.Stderr) != 0 || int(calls.Load()) != wantCalls {
							t.Fatalf("epic read failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), wantCalls, result.Stdout, result.Stderr)
						}
						data := parityEpicReadOutput(t, result.Stdout, lane.name, mode, operation, fixture.Name == "unknown-status", fixture.Name == "continuation")
						expectedLane := lane.name
						if lane.name == "before" && mode == "compact" {
							expectedLane = "before-compact"
						}
						expected := fixture.Expected[expectedLane][operation]
						if !parityAuthoringJSON(data, expected) {
							t.Fatalf("epic read differs from independent native/count projection: %s", result.Stdout)
						}
						// Full fixed-response comparisons remain sensitive to native identity,
						// missing/null fields, exact number tokens and count completeness.
						for _, mutation := range []struct{ name, original, replacement string }{{"query", "issuetype = Epic", "issuetype = Task"}, {"identity", "PAIR-1", "PAIR-9"}, {"count", `"Done":1`, `"Done":2`}} {
							changed := strings.Replace(string(data), mutation.original, mutation.replacement, 1)
							if changed == string(data) {
								continue
							}
							t.Run(mutation.name, func(t *testing.T) {
								if parityAuthoringJSON([]byte(changed), expected) {
									t.Fatal("changed epic identity/query/count earned acceptance")
								}
							})
						}
						id := "C050"
						if operation == "board" {
							id = "C049"
						}
						check := id + ".epics.fixture." + fixture.Name + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					})
				}
			}
		}
	}
	saveParityLocal(t, "epic-read-fixture", evidence)
}

// The legacy list models summary and status but loses their native nulls and
// status IDs. Keep that projection explicit; do not import either lane's codec.
func parityEpicLegacyListViews(native []json.RawMessage) ([]json.RawMessage, bool) {
	if _, ok := parityEpicCacheViews(native, "before"); !ok {
		return nil, false
	}
	rows := make([]json.RawMessage, 0, len(native))
	for _, raw := range native {
		var item, fields map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || json.Unmarshal(item["fields"], &fields) != nil {
			return nil, false
		}
		for _, name := range []string{"summary", "status"} {
			value, present := fields[name]
			if !present {
				continue
			}
			if string(value) == "null" {
				delete(fields, name)
				continue
			}
			if name == "summary" {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return nil, false
				}
				continue
			}
			var status map[string]json.RawMessage
			if json.Unmarshal(value, &status) != nil || status == nil {
				return nil, false
			}
			projected := map[string]any{}
			if value, present := status["name"]; present && string(value) != "null" {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return nil, false
				}
				projected["name"] = text
			}
			if value, present := status["statusCategory"]; present && string(value) != "null" {
				var category map[string]json.RawMessage
				if json.Unmarshal(value, &category) != nil || category == nil {
					return nil, false
				}
				target := map[string]string{}
				for _, name := range []string{"key", "name", "colorName"} {
					if value, present := category[name]; present && string(value) != "null" {
						var text string
						if json.Unmarshal(value, &text) != nil {
							return nil, false
						}
						target[name] = text
					}
				}
				projected["statusCategory"] = target
			}
			encoded, err := json.Marshal(projected)
			if err != nil {
				return nil, false
			}
			fields[name] = encoded
		}
		row := map[string]any{"id": item["id"], "key": item["key"], "fields": fields}
		if raw, present := item["self"]; present && string(raw) != "null" {
			var self string
			if json.Unmarshal(raw, &self) != nil {
				return nil, false
			}
			if self != "" {
				row["self"] = self
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, false
		}
		rows = append(rows, encoded)
	}
	return rows, true
}

// Callers bind bounded native observations and collection facts before using
// this projection. Legacy child counts represent one page; current child
// completeness requires an independently exhausted walk.
func parityEpicReadExpected(native []json.RawMessage, children map[string][]json.RawMessage, lane, operation string) ([]byte, int, bool) {
	views, ok := parityEpicCacheViews(native, lane)
	if !ok || (operation != "list" && operation != "board") {
		return nil, 0, false
	}
	if operation == "list" {
		if lane == "before" {
			views, ok = parityEpicLegacyListViews(native)
			if !ok {
				return nil, 0, false
			}
		}
		want := map[string]any{"jql": "issuetype = Epic", "epics": views}
		if lane == "before" {
			want["detail"] = false
		} else {
			want["pagination"] = map[string]any{"is_last": true, "returned": len(views)}
			want["truncated"] = false
		}
		raw, err := json.Marshal(want)
		return raw, 0, err == nil
	}
	totals := map[string]int{"To Do": 0, "In Progress": 0, "Done": 0}
	rows := []map[string]any{}
	unknownTotal := 0
	for i, view := range views {
		var row map[string]any
		var identity struct{ Key string }
		decoder := json.NewDecoder(bytes.NewReader(view))
		decoder.UseNumber()
		if decoder.Decode(&row) != nil || json.Unmarshal(native[i], &identity) != nil {
			return nil, 0, false
		}
		records, present := children[identity.Key]
		if !present || records == nil {
			return nil, 0, false
		}
		if _, ok := parityEpicCacheViews(records, lane); !ok {
			return nil, 0, false
		}
		counts := map[string]int{"To Do": 0, "In Progress": 0, "Done": 0}
		unknown := 0
		for _, record := range records {
			var child struct {
				Fields struct{ Status *struct{ Name *string } }
			}
			if json.Unmarshal(record, &child) != nil {
				return nil, 0, false
			}
			if child.Fields.Status == nil || child.Fields.Status.Name == nil || lane == "after" && strings.TrimSpace(*child.Fields.Status.Name) == "" {
				unknown++
				continue
			}
			name := *child.Fields.Status.Name
			counts[name]++
			totals[name]++
		}
		row["counts"] = counts
		if lane == "after" {
			row["children"] = map[string]any{"attempted": true, "complete": unknown == 0, "pagination": map[string]any{"is_last": true, "returned": len(records)}, "truncated": false, "unknown_status": unknown}
		}
		unknownTotal += unknown
		rows = append(rows, row)
	}
	want := map[string]any{"epics": rows, "totals": totals}
	if lane == "after" {
		want["jql"] = "issuetype = Epic"
		want["pagination"] = map[string]any{"is_last": true, "returned": len(views)}
		want["truncated"] = false
		want["unknown_status"] = unknownTotal
		want["counts_complete"] = unknownTotal == 0
	}
	raw, err := json.Marshal(want)
	return raw, unknownTotal, err == nil
}

func TestParityEpicReadOracleMatchesIndependentFixtures(t *testing.T) {
	for _, fixture := range parityEpicReadFixtures(t) {
		for _, lane := range []string{"before", "after"} {
			for _, operation := range []string{"list", "board"} {
				t.Run(fixture.Name+"/"+lane+"/"+operation, func(t *testing.T) {
					native := fixture.Native
					if fixture.Name == "continuation" && lane == "before" {
						native = native[:1]
					}
					children := map[string][]json.RawMessage{"PAIR-1": fixture.Children, "PAIR-2": {}}
					if fixture.Name == "child-continuation" && lane == "before" {
						children["PAIR-1"] = fixture.Children[:1]
					}
					expected, _, ok := parityEpicReadExpected(native, children, lane, operation)
					if !ok || !parityAuthoringJSON(expected, fixture.Expected[lane][operation]) {
						t.Fatalf("native epic oracle differs from independent fixture: %s", expected)
					}
				})
			}
		}
	}
}

func parityEpicNativePage(t *testing.T, p *parityRun, query string, fields []string, size int, token string) ([]json.RawMessage, bool, string) {
	t.Helper()
	request := map[string]any{"jql": query, "fields": fields, "maxResults": size}
	if token != "" {
		request["nextPageToken"] = token
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.owner.dir, fmt.Sprintf("epic-read-%06d.json", p.owner.sequence))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	reply := rawRead(t, p.owner, "api", "post", "/rest/api/3/search/jql", "--input", path)
	if reply.Request.BodyBase64 == nil || *reply.Request.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
		t.Fatal("independent epic observation changed exact search input")
	}
	var page map[string]json.RawMessage
	var native []json.RawMessage
	var last *bool
	if !jsontext.Value(reply.Response.JSON).IsValid() || json.Unmarshal(reply.Response.JSON, &page) != nil || json.Unmarshal(page["issues"], &native) != nil || native == nil || len(native) > size || json.Unmarshal(page["isLast"], &last) != nil || last == nil {
		t.Fatal("independent epic observation lacks bounded complete native page facts")
	}
	if _, ok := parityEpicCacheViews(native, "after"); !ok {
		t.Fatal("independent epic observation has invalid or repeated issue identity")
	}
	next := ""
	if value, present := page["nextPageToken"]; present && json.Unmarshal(value, &next) != nil {
		t.Fatal("independent epic observation has malformed continuation")
	}
	return native, *last, next
}

func parityEpicNativeChildren(t *testing.T, p *parityRun, key, lane string) []json.RawMessage {
	t.Helper()
	items := []json.RawMessage{}
	token := ""
	seen := map[string]bool{}
	size := 100
	if lane == "before" {
		size = 50
	}
	for page := 0; page < 50; page++ {
		native, last, next := parityEpicNativePage(t, p, `parent = "`+key+`"`, []string{"status"}, size, token)
		items = append(items, native...)
		if len(items) > 5000 {
			t.Fatal("independent child walk exceeded admitted fixture size")
		}
		if _, ok := parityEpicCacheViews(items, "after"); !ok {
			t.Fatal("independent child walk repeats or changes issue identity")
		}
		// Legacy board counts exactly its first child page. Current board drains
		// the bounded walk. Neither independently observed search is transactional.
		if last || lane == "before" {
			return items
		}
		if next == "" || seen[next] {
			t.Fatal("independent child walk lacks a fresh continuation")
		}
		seen[next] = true
		token = next
	}
	t.Fatal("independent child walk exceeded admitted page bound")
	return nil
}

func TestLiveParityEpicReads(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			native, last, _ := parityEpicNativePage(t, p, "issuetype = Epic", []string{"summary", "status"}, 50, "")
			if !last {
				t.Fatal("this epic recipe requires a complete outer page of at most 50 epics; larger outer walks remain pending")
			}
			for _, operation := range []string{"list", "board"} {
				t.Run(operation, func(t *testing.T) {
					children := map[string][]json.RawMessage{}
					if operation == "board" {
						for _, raw := range native {
							var issue struct{ Key string }
							if json.Unmarshal(raw, &issue) != nil || !liveKey.MatchString(issue.Key) {
								t.Fatal("independent epic observation returned an unsafe key")
							}
							// Observed public keys supply read context, never mutation ownership.
							children[issue.Key] = parityEpicNativeChildren(t, p, issue.Key, lane.Name)
						}
					}
					expected, unknown, ok := parityEpicReadExpected(native, children, lane.Name, operation)
					if !ok {
						t.Fatal("independent epic read projection unavailable")
					}
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "epic", operation)
					if exit != 0 || !frame.OK {
						t.Fatal("paired epic read failed; inspect retained receipts")
					}
					data := parityEpicReadOutput(t, stdout, lane.Name, "json", operation, unknown > 0, false)
					if !parityAuthoringJSON(data, expected) {
						t.Fatal("paired epic output differs from independent native epic/child observations")
					}
					id := "C050"
					if operation == "board" {
						id = "C049"
					}
					p.check(t, lane, id+".epics.native")
				})
			}
		})
	}
}
