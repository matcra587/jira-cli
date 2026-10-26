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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Both metadata row contracts are checked independently before the enclosing
// batch may reuse their bytes. Duration is diagnostic, never a success oracle.
func parityCacheRefreshCollected(data []byte, lane string, names []string, wants map[string]parityMetadataExpected) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	var actual map[string]json.RawMessage
	var rows []map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(actual["results"], &rows) != nil || len(rows) != len(names) {
		return false
	}
	expectedRows := make([]map[string]any, 0, len(names))
	for i, name := range names {
		want, found := wants[name]
		if !found {
			return false
		}
		switch lane {
		case "before":
			var row map[string]json.RawMessage
			var duration int64
			if json.Unmarshal(rows[i]["data"], &row) != nil || json.Unmarshal(row["duration_ms"], &duration) != nil || duration < 0 {
				return false
			}
			status := "refreshed"
			if want.Cached {
				status = "fresh"
			}
			expectedRows = append(expectedRows, map[string]any{"key": name, "ok": true, "data": map[string]any{"status": status, "from_cache": want.Cached, "count": len(want.Items), "fetched_at": want.Stamp, "duration_ms": duration, "dry_run": false}})
		case "after":
			if !parityMetadataProjection(rows[i]["data"], lane, want) {
				return false
			}
			expectedRows = append(expectedRows, map[string]any{"resource": name, "ok": true, "data": rows[i]["data"]})
		default:
			return false
		}
	}
	want := map[string]any{"results": expectedRows, "dry_run": false, "parallelism": 2}
	if lane == "before" {
		want = map[string]any{"results": expectedRows, "succeeded": len(names), "failed": 0}
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func parityCacheRefreshFixtures(t *testing.T, empty bool) []parityMetadataFixture {
	t.Helper()
	selected := []parityMetadataFixture{}
	// Explicit request order differs from the catalog order.
	for _, name := range []string{"fields", "labels"} {
		for _, fixture := range parityMetadataFixtures(t) {
			if fixture.Name == name {
				if empty {
					fixture.Native, fixture.Before = []json.RawMessage{}, []json.RawMessage{}
				}
				selected = append(selected, fixture)
			}
		}
	}
	if len(selected) != 2 {
		t.Fatal("independent batch metadata fixtures are incomplete")
	}
	return selected
}

func parityCacheRefreshTree(t *testing.T, before, after map[string]parityLocalFile, home, lane, site string, cached bool) {
	t.Helper()
	paths := []string{}
	for _, name := range []string{"fields", "labels"} {
		if cached {
			continue
		}
		_, path := parityMetadataLegacyLocation(home, site, name)
		if lane == "after" {
			identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len("fixture@example.invalid"), "fixture@example.invalid")
			namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
			key := "scope-v2:" + namespace + ":v1:metadata:" + name
			path = filepath.Join(home, "jira", "scope-v2-"+namespace, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
		}
		paths = append(paths, filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path)
	}
	if lane == "before" {
		_, path := parityMetadataLegacyLocation(home, site, "issuekeys")
		paths = append(paths, filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path)
	}
	for _, path := range paths {
		relative, err := filepath.Rel(home, path)
		if err != nil || !filepath.IsLocal(relative) {
			t.Fatal("batch cache path escaped its owned root")
		}
		file, present := after[relative]
		if !present || filepath.Ext(path) == ".json" && !file.Mode.IsRegular() || filepath.Ext(path) != ".json" && !file.Mode.IsDir() {
			t.Fatal("batch did not publish its exact owned cache paths")
		}
		before[relative] = file
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("batch changed files outside its explicit metadata/history effects")
	}
}

func TestParityActualBinaryCacheRefreshCollection(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, empty := range []bool{false, true} {
			for _, mode := range []string{"json", "compact"} {
				t.Run(fmt.Sprintf("%s/empty=%t/%s", lane.name, empty, mode), func(t *testing.T) {
					if lane.binary == "" {
						t.Skip("requires selected actual binary")
					}
					fixtures := parityCacheRefreshFixtures(t, empty)
					var fieldsCalls, labelsCalls, revision atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(io.LimitReader(r.Body, 1))
						user, password, authenticated := r.BasicAuth()
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						var fixture *parityMetadataFixture
						for i := range fixtures {
							if r.URL.Path == fixtures[i].Endpoint {
								fixture = &fixtures[i]
							}
						}
						if err != nil || queryErr != nil || len(body) != 0 || r.Method != "GET" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" || fixture == nil {
							t.Error("batch changed its exact authenticated metadata request")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						wantQuery := url.Values{}
						if fixture.Paged {
							size := 100
							if lane.name == "before" {
								size = 1000
							}
							wantQuery = url.Values{"startAt": {"0"}, "maxResults": {strconv.Itoa(size)}}
							labelsCalls.Add(1)
						} else {
							fieldsCalls.Add(1)
						}
						if !reflect.DeepEqual(query, wantQuery) {
							t.Error("batch changed its explicit metadata query")
							http.Error(w, "wrong synthetic query", http.StatusBadRequest)
							return
						}
						raw, err := json.Marshal(fixture.Native)
						if err != nil {
							t.Error(err)
							return
						}
						if revision.Load() > 0 {
							raw = []byte(strings.ReplaceAll(string(raw), "Owned", "Refreshed"))
						}
						w.Header().Set("Content-Type", "application/json")
						if fixture.Paged {
							_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":100,"total":%d,"isLast":true,"values":%s}`, len(fixture.Native), raw)
						} else {
							_, _ = w.Write(raw)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					began := time.Now()
					for _, phase := range []string{"initial", "cached", "forced", "cached-after-force", "repeated-cached"} {
						t.Run(phase, func(t *testing.T) {
							cached, force := strings.Contains(phase, "cached"), phase == "forced"
							if force {
								revision.Store(1)
							}
							names := []string{"fields", "labels"}
							args := []string{"cache", "refresh", "fields", "labels", "--parallelism=2"}
							if force {
								args = append(args, "--force")
							}
							if phase == "repeated-cached" {
								args = append(args, "fields")
								if lane.name == "before" {
									names = append(names, "fields")
								}
							}
							before := parityLocalTree(t, home)
							fc, lc := fieldsCalls.Load(), labelsCalls.Load()
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							invoked := time.Now()
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							wantCalls := int32(1)
							if cached {
								wantCalls = 0
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || fieldsCalls.Load()-fc != wantCalls || labelsCalls.Load()-lc != wantCalls {
								t.Fatalf("batch collection failed: exit %d; fields %d labels %d want %d; stdout %s; stderr %s", result.Exit, fieldsCalls.Load()-fc, labelsCalls.Load()-lc, wantCalls, result.Stdout, result.Stderr)
							}
							wants := make(map[string]parityMetadataExpected)
							for _, fixture := range fixtures {
								items := fixture.Native
								if lane.name == "before" {
									items = fixture.Before
								}
								if revision.Load() > 0 {
									var err error
									items, err = parityMetadataRefreshed(items)
									if err != nil {
										t.Fatal(err)
									}
								}
								written := parityMetadataDisk(t, home, lane.name, server.URL, fixture.Name, items, 1)
								if written.Before(began) || written.After(time.Now()) || !cached && written.Before(invoked) {
									t.Fatal("batch metadata has an impossible publication time")
								}
								state := "fresh"
								if phase == "initial" {
									state = "missing"
								}
								want := parityMetadataExpected{Resource: fixture.Name, Items: items, Pages: 1, Cached: cached, State: state, Earliest: written, Latest: time.Now()}
								if lane.name == "before" {
									want.Stamp = written.UTC().Format(time.RFC3339)
								} else if cached {
									want.Stamp = written.UTC().Format(time.RFC3339Nano)
								}
								wants[fixture.Name] = want
							}
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "cache.refresh"))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !parityCacheRefreshCollected(data, lane.name, names, wants) {
								t.Fatalf("batch differs from independently observed metadata and persisted collection facts: %s", result.Stdout)
							}
							for _, mutation := range []struct{ name, original, replacement string }{
								{"wrong-resource", `"fields"`, `"statuses"`},
								{"false-success", `"ok":true`, `"ok":false`},
								{"false-preview", `"dry_run":false`, `"dry_run":true`},
								{"extra-row", `"results":[`, `"results":[{"ok":true},`},
							} {
								t.Run("guard/"+mutation.name, func(t *testing.T) {
									changed := strings.Replace(string(data), mutation.original, mutation.replacement, 1)
									if changed == string(data) || parityCacheRefreshCollected([]byte(changed), lane.name, names, wants) {
										t.Fatal("changed batch receipt earned collection or persistence credit")
									}
								})
							}
							if lane.name == "before" {
								written := parityMetadataDisk(t, home, lane.name, server.URL, "issuekeys", []json.RawMessage{json.RawMessage(`"FIELDS"`), json.RawMessage(`"LABELS"`)}, 0)
								if written.Before(invoked) || written.After(time.Now()) {
									t.Fatal("batch history publication has an impossible time")
								}
							}
							parityCacheRefreshTree(t, before, parityLocalTree(t, home), home, lane.name, server.URL, cached)
							id := fmt.Sprintf("C038.refresh.collection.empty=%t.%s.%s", empty, phase, mode)
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						})
					}
				})
			}
		}
	}
	saveParityLocal(t, "cache-refresh-fixture", evidence)
}

// Jira field enumeration can reorder equivalent native rows between adjacent
// requests. Live admission compares complete independent membership, then binds
// the command's exact row order to its persisted snapshot. Fixed fixtures keep
// their authored order checks; this does not sort or weaken those comparisons.
func parityCacheRefreshNativeItems(raw []byte, expected []json.RawMessage, resource string) ([]json.RawMessage, bool) {
	var actual []json.RawMessage
	if (resource != "fields" && resource != "labels" && resource != "epics") || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &actual) != nil || actual == nil || len(actual) != len(expected) {
		return nil, false
	}
	identity := func(item json.RawMessage) (string, bool) {
		var key string
		switch resource {
		case "labels":
			var value *string
			if json.Unmarshal(item, &value) != nil || value == nil {
				return "", false
			}
			return *value, true
		case "fields", "epics":
			var row map[string]json.RawMessage
			member := "id"
			if resource == "epics" {
				member = "key"
			}
			if json.Unmarshal(item, &row) != nil || json.Unmarshal(row[member], &key) != nil || key == "" {
				return "", false
			}
			return key, true
		default:
			return "", false
		}
	}
	remaining := make(map[string]json.RawMessage)
	for _, item := range expected {
		key, ok := identity(item)
		if _, duplicate := remaining[key]; !ok || duplicate {
			return nil, false
		}
		remaining[key] = item
	}
	for _, item := range actual {
		key, ok := identity(item)
		want, found := remaining[key]
		if !ok || !found || !parityAuthoringJSON(item, want) {
			return nil, false
		}
		delete(remaining, key)
	}
	return actual, len(remaining) == 0
}

func parityLiveMetadataStored(t *testing.T, p *parityRun, lane parityLane, fixture parityMetadataFixture, items []json.RawMessage, pages int) (time.Time, []json.RawMessage) {
	t.Helper()
	path, identity := parityLiveHistoryPath(p, lane)
	path = filepath.Join(filepath.Dir(path), fixture.Name+".json")
	if lane.Name == "after" {
		identity = strings.TrimSuffix(identity, "issuekeys") + "metadata:" + fixture.Name
		path = filepath.Join(filepath.Dir(path), fmt.Sprintf("%x.json", sha256.Sum256([]byte(identity))))
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("live metadata publication is absent or nonregular")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !jsontext.Value(raw).IsValid() {
		t.Fatal("live metadata wrapper is malformed")
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
		t.Fatal("live metadata wrapper cannot be decoded")
	}
	if lane.Name == "before" {
		stored, ok := parityCacheRefreshNativeItems(wrapper.Data, items, fixture.Name)
		if wrapper.Profile != identity || wrapper.Resource != fixture.Name || wrapper.Schema != 1 || wrapper.FetchedAt.IsZero() || !ok {
			t.Fatal("live legacy metadata differs from its independent scope/schema/native membership")
		}
		return wrapper.FetchedAt, stored
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(wrapper.Value, &snapshot) != nil {
		t.Fatal("live current metadata snapshot cannot be decoded")
	}
	stored, ok := parityCacheRefreshNativeItems(snapshot["items"], items, fixture.Name)
	expected, err := json.Marshal(map[string]any{"version": 1, "items": stored, "count": len(items), "pages": pages, "complete": true})
	if err != nil || !ok || wrapper.Key != identity || wrapper.Written.IsZero() || !parityAuthoringJSON(wrapper.Value, expected) {
		t.Fatal("live current metadata differs from its independent scope/native membership and snapshot")
	}
	return wrapper.Written, stored
}

func TestLiveParityCacheRefreshCollection(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			fixtures := parityCacheRefreshFixtures(t, false)
			items := make(map[string][]json.RawMessage)
			pages := make(map[string]int)
			root := p.owner.cacheDir
			if lane.Name == "before" {
				root = filepath.Join(p.owner.dir, "cache-before")
			}
			began := time.Now()
			for _, phase := range []string{"initial", "cached", "forced", "cached-after-force", "repeated-cached"} {
				if !t.Run(phase, func(t *testing.T) {
					cached, force := strings.Contains(phase, "cached"), phase == "forced"
					if !cached {
						for _, fixture := range fixtures {
							native, count := parityNativeMetadata(t, p, lane.Name, fixture)
							if lane.Name == "before" {
								var ok bool
								native, ok = parityMetadataLegacyItems(native, fixture.Name)
								if !ok {
									t.Fatal("independent live legacy batch projection is unavailable")
								}
							}
							items[fixture.Name], pages[fixture.Name] = native, count
						}
					}
					args := []string{"cache", "refresh", "fields", "labels", "--parallelism=2"}
					names := []string{"fields", "labels"}
					if force {
						args = append(args, "--force")
					}
					if phase == "repeated-cached" {
						args = append(args, "fields")
						if lane.Name == "before" {
							names = append(names, "fields")
						}
					}
					var before map[string]parityLocalFile
					if cached {
						before = parityLocalTree(t, root)
					}
					invoked := time.Now()
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + p.owner.account}, args...)
					if exit != 0 || !frame.OK {
						t.Fatal("live batch metadata failed; inspect retained receipts")
					}
					wants := make(map[string]parityMetadataExpected)
					for _, fixture := range fixtures {
						written, stored := parityLiveMetadataStored(t, p, lane, fixture, items[fixture.Name], pages[fixture.Name])
						if written.Before(began) || written.After(time.Now()) || !cached && written.Before(invoked) {
							t.Fatal("live batch publication has an impossible time")
						}
						state := "fresh"
						if phase == "initial" {
							state = "missing"
						}
						want := parityMetadataExpected{Resource: fixture.Name, Items: stored, Pages: pages[fixture.Name], Cached: cached, State: state, Earliest: written, Latest: time.Now()}
						if lane.Name == "before" {
							want.Stamp = written.UTC().Format(time.RFC3339)
						} else if cached {
							want.Stamp = written.UTC().Format(time.RFC3339Nano)
						}
						wants[fixture.Name] = want
					}
					data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.refresh"))
					if err != nil || !parityCacheRefreshCollected(data, lane.Name, names, wants) {
						t.Fatal("live batch differs from independently read native metadata and persisted snapshots")
					}
					if lane.Name == "before" {
						written := parityLiveHistoryStored(t, p, lane, []string{"FIELDS", "LABELS"})
						if written.Before(invoked) || written.After(time.Now()) {
							t.Fatal("legacy live batch history has an impossible publication time")
						}
					}
					if cached {
						after := parityLocalTree(t, root)
						if lane.Name == "before" {
							history, _ := parityLiveHistoryPath(p, lane)
							relative, err := filepath.Rel(root, history)
							if err != nil {
								t.Fatal(err)
							}
							delete(before, relative)
							delete(after, relative)
						}
						if !reflect.DeepEqual(before, after) {
							t.Fatal("live cache reuse changed metadata or unrelated owned entries")
						}
					}
					p.check(t, lane, "C038.refresh.native."+phase)
				}) {
					return
				}
			}
		})
	}
}

func TestParityCacheRefreshNativeMembership(t *testing.T) {
	fields := []json.RawMessage{json.RawMessage(`{"id":"one","future":900719925474099312345}`), json.RawMessage(`{"id":"two","description":null}`)}
	labels := []json.RawMessage{json.RawMessage(`"café"`), json.RawMessage(`"second"`)}
	for _, tc := range []struct {
		name, resource, raw string
		want                []json.RawMessage
		valid               bool
	}{
		{"reordered-fields", "fields", `[{"id":"two","description":null},{"id":"one","future":900719925474099312345}]`, fields, true},
		{"changed-number", "fields", `[{"id":"one","future":900719925474099300000},{"id":"two","description":null}]`, fields, false},
		{"missing-native-member", "fields", `[{"id":"one","future":900719925474099312345},{"id":"two"}]`, fields, false},
		{"duplicate-identity", "fields", `[{"id":"one","future":900719925474099312345},{"id":"one","future":900719925474099312345}]`, fields, false},
		{"duplicate-member", "fields", `[{"id":"one","id":"two"},{"id":"one","future":900719925474099312345}]`, fields, false},
		{"missing-row", "fields", `[{"id":"one","future":900719925474099312345}]`, fields, false},
		{"wrong-member-case", "fields", `[{"ID":"two","description":null},{"id":"one","future":900719925474099312345}]`, fields, false},
		{"reordered-labels", "labels", `["second","café"]`, labels, true},
		{"changed-label", "labels", `["second","cafe"]`, labels, false},
		{"null-label", "labels", `[null]`, []json.RawMessage{json.RawMessage(`null`)}, false},
		{"null-list", "labels", `null`, []json.RawMessage{}, false},
		{"duplicate-expected", "labels", `["second","second"]`, []json.RawMessage{json.RawMessage(`"second"`), json.RawMessage(`"second"`)}, false},
		{"reordered-epics", "epics", `[{"key":"PAIR-2","fields":{}},{"key":"PAIR-1","fields":{"summary":null}}]`, []json.RawMessage{json.RawMessage(`{"key":"PAIR-1","fields":{"summary":null}}`), json.RawMessage(`{"key":"PAIR-2","fields":{}}`)}, true},
		{"changed-epic-fields", "epics", `[{"key":"PAIR-1","fields":{}}]`, []json.RawMessage{json.RawMessage(`{"key":"PAIR-1","fields":{"summary":null}}`)}, false},
		{"duplicate-epic-key", "epics", `[{"key":"PAIR-1"},{"key":"PAIR-1"}]`, []json.RawMessage{json.RawMessage(`{"key":"PAIR-1"}`), json.RawMessage(`{"key":"PAIR-2"}`)}, false},
		{"unknown-resource", "other", `[]`, []json.RawMessage{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parityCacheRefreshNativeItems([]byte(tc.raw), tc.want, tc.resource)
			if ok != tc.valid {
				t.Fatal("native membership admission changed exact values, identities or supported reordering")
			}
		})
	}
}

func TestParityCacheRefreshDurationIsDiagnostic(t *testing.T) {
	want := map[string]parityMetadataExpected{"fields": {Items: []json.RawMessage{}, Stamp: parityCacheStamp}}
	for _, tc := range []struct {
		name     string
		duration int
		valid    bool
	}{{"negative", -1, false}, {"zero", 0, true}, {"live-deadline", 30000, true}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"results":[{"key":"fields","ok":true,"data":{"status":"refreshed","from_cache":false,"count":0,"fetched_at":"2026-01-01T00:00:00Z","duration_ms":%d,"dry_run":false}}],"succeeded":1,"failed":0}`, tc.duration)
			if parityCacheRefreshCollected([]byte(raw), "before", []string{"fields"}, want) != tc.valid {
				t.Fatal("diagnostic duration changed valid batch admission independently of its owned process deadline")
			}
		})
	}
}
