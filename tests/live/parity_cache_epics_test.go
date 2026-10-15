//go:build live

package live

import (
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type parityEpicCacheFixture struct {
	Native, Before, After []json.RawMessage
}

func parityEpicCacheFixtures(t *testing.T) parityEpicCacheFixture {
	t.Helper()
	var fixture parityEpicCacheFixture
	raw, err := os.ReadFile("testdata/parity-epic-cache-oracles.json")
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixture) != nil || len(fixture.Native) != 2 || len(fixture.Before) != 2 || len(fixture.After) != 2 {
		t.Fatal("independent epic cache fixtures are unavailable or malformed")
	}
	return fixture
}

func TestParityActualBinaryEpicCache(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	fixture := parityEpicCacheFixtures(t)
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, sample := range []string{"empty", "single-page", "multiple-pages"} {
			for _, mode := range []string{"json", "compact"} {
				t.Run(lane.name+"/"+sample+"/"+mode, func(t *testing.T) {
					if lane.binary == "" {
						t.Skip("requires selected actual binary")
					}
					var calls, revision atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
						user, password, authenticated := r.BasicAuth()
						var request map[string]json.RawMessage
						if err != nil || len(body) > 8192 || !jsontext.Value(body).IsValid() || json.Unmarshal(body, &request) != nil || r.Method != "POST" || r.URL.Path != "/rest/api/3/search/jql" || r.URL.RawQuery != "" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							t.Error("epic primer changed its exact authenticated enhanced-search request")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						pageSize := 100
						if lane.name == "before" {
							pageSize = 200
						}
						want := map[string]any{"jql": "issuetype = Epic", "maxResults": pageSize, "fields": []string{"summary", "status"}}
						var token string
						if raw, present := request["nextPageToken"]; present {
							if json.Unmarshal(raw, &token) != nil || token != "next-owned" || sample != "multiple-pages" || lane.name != "after" {
								t.Error("epic primer changed its native continuation token")
								http.Error(w, "wrong synthetic token", http.StatusBadRequest)
								return
							}
							want["nextPageToken"] = token
						}
						expected, err := json.Marshal(want)
						if err != nil || !parityAuthoringJSON(body, expected) {
							t.Error("epic primer changed its exact query, selected fields or page size")
							http.Error(w, "wrong synthetic body", http.StatusBadRequest)
							return
						}
						rows, last, next := fixture.Native, true, ""
						switch sample {
						case "empty":
							rows = []json.RawMessage{}
						case "multiple-pages":
							if token == "" {
								rows, last, next = fixture.Native[:1], false, "next-owned"
							} else {
								rows = fixture.Native[1:]
							}
						}
						response := map[string]any{"issues": rows, "isLast": last}
						if !last {
							response["nextPageToken"] = next
						}
						raw, err := json.Marshal(response)
						if err != nil {
							t.Error(err)
							return
						}
						if revision.Load() > 0 {
							raw = []byte(strings.ReplaceAll(string(raw), "Owned", "Refreshed"))
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(raw)
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
							items := fixture.After
							if lane.name == "before" {
								items = fixture.Before
							}
							pages := 1
							switch sample {
							case "empty":
								items = []json.RawMessage{}
							case "multiple-pages":
								if lane.name == "before" {
									// The old primer issues one page request. Its receipt
									// does not claim a complete walk of visible epics.
									items = items[:1]
								} else {
									pages = 2
								}
							}
							if revision.Load() > 0 {
								var err error
								items, err = parityMetadataRefreshed(items)
								if err != nil {
									t.Fatal(err)
								}
							}
							args := []string{"cache", "epics"}
							if force {
								flag := "--force"
								if lane.name == "before" {
									flag = "--refresh"
								}
								args = append(args, flag)
							}
							beforeFiles, beforeCalls := parityLocalTree(t, home), calls.Load()
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							invoked := time.Now()
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							wantCalls := int32(pages)
							if cached {
								wantCalls = 0
								if !reflect.DeepEqual(beforeFiles, parityLocalTree(t, home)) {
									t.Fatal("cached epic reuse changed owned files")
								}
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load()-beforeCalls != wantCalls {
								t.Fatalf("epic primer failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load()-beforeCalls, wantCalls, result.Stdout, result.Stderr)
							}
							written := parityMetadataDisk(t, home, lane.name, server.URL, "epics", items, pages)
							if written.Before(began) || written.After(time.Now()) || !cached && written.Before(invoked) {
								t.Fatal("epic cache has an impossible publication time")
							}
							state := "fresh"
							if phase == "initial" {
								state = "missing"
							} else if force && lane.name == "before" {
								state = "refresh"
							}
							want := parityMetadataExpected{Resource: "epics", Profile: "fixture", State: state, Items: items, TTL: 240, Pages: pages, Cached: cached, Force: force, Earliest: written, Latest: time.Now()}
							if lane.name == "before" {
								want.Stamp = written.UTC().Format(time.RFC3339)
							} else if cached {
								want.Stamp = written.UTC().Format(time.RFC3339Nano)
							}
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "cache.epics"))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !parityMetadataProjection(data, lane.name, want) {
								t.Fatalf("epic cache differs from independent native/summarized values and walk facts: %s", result.Stdout)
							}
							id := fmt.Sprintf("C030.epics.%s.%s.%s", sample, phase, mode)
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						}) {
							return
						}
					}
				})
			}
		}
	}
	saveParityLocal(t, "epic-cache-fixture", evidence)
}

// This is the public lane mapping, independent of generated Jira models and
// the production IssueView projection. Literal fixtures below pin its meaning.
func parityEpicCacheViews(items []json.RawMessage, lane string) ([]json.RawMessage, bool) {
	if items == nil || (lane != "before" && lane != "after") {
		return nil, false
	}
	out := make([]json.RawMessage, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		var native, fields map[string]json.RawMessage
		var id, key string
		if !jsontext.Value(item).IsValid() || json.Unmarshal(item, &native) != nil || json.Unmarshal(native["id"], &id) != nil || id == "" || json.Unmarshal(native["key"], &key) != nil || key == "" || seen[key] || json.Unmarshal(native["fields"], &fields) != nil || fields == nil {
			return nil, false
		}
		seen[key] = true
		var value map[string]any
		if lane == "before" {
			var summary, status string
			if raw, present := fields["summary"]; present && json.Unmarshal(raw, &summary) != nil {
				return nil, false
			}
			if raw, present := fields["status"]; present {
				var object map[string]json.RawMessage
				if json.Unmarshal(raw, &object) != nil {
					return nil, false
				}
				if raw, present := object["name"]; present && json.Unmarshal(raw, &status) != nil {
					return nil, false
				}
			}
			value = map[string]any{"key": key, "summary": summary, "status": status}
		} else {
			value = map[string]any{"id": id, "key": key, "fields": native["fields"]}
			if raw, present := native["self"]; present {
				var self string
				if json.Unmarshal(raw, &self) != nil {
					return nil, false
				}
				if self != "" {
					value["self"] = self
				}
			}
			for _, member := range []string{"id", "key", "self", "fields"} {
				delete(native, member)
			}
			if len(native) > 0 {
				value["details"] = native
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		out = append(out, raw)
	}
	return out, true
}

func TestParityEpicCacheProjectionMatchesIndependentFixtures(t *testing.T) {
	fixture := parityEpicCacheFixtures(t)
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			want := fixture.Before
			if lane == "after" {
				want = fixture.After
			}
			preimage, err := json.Marshal(fixture.Native)
			if err != nil {
				t.Fatal(err)
			}
			items, ok := parityEpicCacheViews(fixture.Native, lane)
			actual, err := json.Marshal(items)
			expected, wantErr := json.Marshal(want)
			if !ok || err != nil || wantErr != nil || !parityAuthoringJSON(actual, expected) {
				t.Fatal("epic lane mapping differs from independently authored native fixtures")
			}
			postimage, err := json.Marshal(fixture.Native)
			if err != nil || string(preimage) != string(postimage) {
				t.Fatal("epic mapping changed its immutable native fixture")
			}
		})
	}
}

func parityNativeEpics(t *testing.T, p *parityRun, lane string) ([]json.RawMessage, int) {
	t.Helper()
	items := []json.RawMessage{}
	token := ""
	seen := make(map[string]bool)
	for page := 1; page <= 50; page++ {
		size := 100
		if lane == "before" {
			size = 200
		}
		request := map[string]any{"jql": "issuetype = Epic", "fields": []string{"summary", "status"}, "maxResults": size}
		if token != "" {
			request["nextPageToken"] = token
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.owner.dir, fmt.Sprintf("epic-search-%s-%03d.json", lane, page))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal("cannot save independent native epic search input")
		}
		read := rawRead(t, p.owner, "api", "post", "/rest/api/3/search/jql", "--input", path)
		if read.Request.BodyBase64 == nil || *read.Request.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
			t.Fatal("independent native epic search changed exact input bytes")
		}
		var response map[string]json.RawMessage
		var rows []json.RawMessage
		var last *bool
		if !jsontext.Value(read.Response.JSON).IsValid() || json.Unmarshal(read.Response.JSON, &response) != nil || json.Unmarshal(response["issues"], &rows) != nil || rows == nil || len(rows) > size || json.Unmarshal(response["isLast"], &last) != nil || last == nil || len(items)+len(rows) > 5000 {
			t.Fatal("independent epic search lacks complete bounded native page facts")
		}
		items = append(items, rows...)
		if _, ok := parityEpicCacheViews(items, lane); !ok {
			t.Fatal("independent epic search has malformed or repeated native identities")
		}
		if *last || lane == "before" {
			return items, page
		}
		var next string
		if json.Unmarshal(response["nextPageToken"], &next) != nil || next == "" || seen[next] {
			t.Fatal("independent epic search has no fresh continuation token")
		}
		seen[next], token = true, next
	}
	t.Fatal("independent epic search exceeded its owned page bound")
	return nil, 0
}

func TestLiveParityEpicCache(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			native, pages := parityNativeEpics(t, p, lane.Name)
			items, ok := parityEpicCacheViews(native, lane.Name)
			if !ok {
				t.Fatal("independent native epic projection is unavailable")
			}
			flag := "--force"
			if lane.Name == "before" {
				flag = "--refresh"
			}
			began := time.Now()
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "cache", "epics", flag)
			if exit != 0 || !frame.OK {
				t.Fatal("native epic primer failed; inspect retained receipts")
			}
			fixture := parityMetadataFixture{Name: "epics", TTL: 240}
			written, stored := parityLiveMetadataStored(t, p, lane, fixture, items, pages)
			if written.Before(began) || written.After(time.Now()) {
				t.Fatal("native epic cache has an impossible publication time")
			}
			state := "missing"
			if lane.Name == "before" {
				state = "refresh"
			}
			want := parityMetadataExpected{Resource: "epics", Profile: lane.Profile, State: state, Items: stored, TTL: 240, Pages: pages, Force: true, Earliest: written, Latest: time.Now()}
			if lane.Name == "before" {
				want.Stamp = written.UTC().Format(time.RFC3339)
			}
			data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.epics"))
			if err != nil || !parityMetadataProjection(data, lane.Name, want) {
				t.Fatal("native epic primer differs from independent search membership and stored/output facts")
			}
			p.check(t, lane, "C030.epics.native")
		})
	}
}
