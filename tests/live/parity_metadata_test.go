//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"io/fs"
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

type parityMetadataFixture struct {
	ID, Name, Endpoint string
	TTL                int
	BeforeEndpoint     string            `json:"before_endpoint"`
	BeforePageSize     int               `json:"before_page_size"`
	Paged              bool              `json:"paged"`
	Native             []json.RawMessage `json:"native"`
	Before             []json.RawMessage `json:"before"`
}

func parityMetadataFixtures(t *testing.T) []parityMetadataFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/parity-metadata-oracles.json")
	var fixtures []parityMetadataFixture
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixtures) != nil || len(fixtures) != 8 {
		t.Fatal("independent metadata fixtures are unavailable or malformed")
	}
	return fixtures
}

func parityMetadataRefreshed(items []json.RawMessage) ([]json.RawMessage, error) {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	// Decode into a fresh slice and fresh raw buffers. Reusing a shallow copy
	// would mutate the immutable native fixture while its server reads it.
	var refreshed []json.RawMessage
	err = json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "Owned", "Refreshed")), &refreshed)
	return refreshed, err
}

// This explicit reduced projection is the legacy public contract, independent
// of its generated models, registry fetchers and the current metadata codecs.
func parityMetadataLegacyItems(native []json.RawMessage, name string) ([]json.RawMessage, bool) {
	if native == nil {
		return nil, false
	}
	items := make([]json.RawMessage, 0, len(native))
	for _, raw := range native {
		if !jsontext.Value(raw).IsValid() {
			return nil, false
		}
		if name == "labels" {
			var label string
			if json.Unmarshal(raw, &label) != nil {
				return nil, false
			}
			items = append(items, raw)
			continue
		}
		var source map[string]json.RawMessage
		if json.Unmarshal(raw, &source) != nil || source == nil {
			return nil, false
		}
		valid := true
		text := func(fields map[string]json.RawMessage, key string) string {
			var value string
			if raw, present := fields[key]; present && json.Unmarshal(raw, &value) != nil {
				valid = false
			}
			return value
		}
		value := map[string]any{"id": text(source, "id"), "name": text(source, "name")}
		optional := func(key, content string) {
			if content != "" {
				value[key] = content
			}
		}
		switch name {
		case "fields", "statuses", "projects":
			owner, member := "schema", "type"
			switch name {
			case "statuses":
				owner, member = "statusCategory", "key"
			case "projects":
				owner, member = "lead", "displayName"
			}
			var nested map[string]json.RawMessage
			if raw, present := source[owner]; present && json.Unmarshal(raw, &nested) != nil {
				return nil, false
			}
			switch name {
			case "fields":
				optional("type", text(nested, member))
			case "statuses":
				optional("status_category", text(nested, member))
			case "projects":
				value["key"] = text(source, "key")
				optional("project_type", text(source, "projectTypeKey"))
				optional("lead", text(nested, member))
			}
		case "issuetypes":
			var subtask bool
			if raw, present := source["subtask"]; present && json.Unmarshal(raw, &subtask) != nil {
				return nil, false
			}
			value["subtask"] = subtask
		case "linktypes":
			value["inward"], value["outward"] = text(source, "inward"), text(source, "outward")
			optional("self", text(source, "self"))
		case "priorities", "resolutions":
		default:
			return nil, false
		}
		if !valid {
			return nil, false
		}
		if (name == "statuses" || name == "priorities" || name == "resolutions") && value["name"] == "" {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		items = append(items, encoded)
	}
	return items, true
}

type parityMetadataExpected struct {
	Resource, Profile, State, Stamp string
	Items                           []json.RawMessage
	Pages                           int
	Cached                          bool
	Earliest, Latest                time.Time
}

func parityMetadataProjection(data []byte, lane string, want parityMetadataExpected) bool {
	if (lane != "before" && lane != "after") || !jsontext.Value(data).IsValid() {
		return false
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil || actual == nil {
		return false
	}
	stampKey := "written_at"
	if lane == "before" {
		stampKey = "fetched_at"
	}
	var stamp string
	if json.Unmarshal(actual[stampKey], &stamp) != nil {
		return false
	}
	written, err := time.Parse(time.RFC3339Nano, stamp)
	earliest := want.Earliest
	if lane == "before" {
		earliest = earliest.Truncate(time.Second)
	}
	if err != nil || written.Before(earliest) || written.After(want.Latest) || want.Stamp != "" && want.Stamp != stamp {
		return false
	}
	expected := map[string]any{"count": len(want.Items), "from_cache": want.Cached, "cache_state": want.State, stampKey: stamp}
	if lane == "before" {
		key := want.Resource
		if key == "linktypes" {
			key = "link_types"
		}
		expected[key], expected["profile"] = want.Items, want.Profile
		expected["cache_source_state"], expected["cache_empty"] = want.State, len(want.Items) == 0
		if len(want.Items) == 0 {
			expected["cache_state"] = "empty"
		}
	} else {
		expected["dry_run"] = false
		expected["resource"], expected["items"], expected["pages"], expected["complete"] = want.Resource, want.Items, want.Pages, true
		expected["would_fetch"], expected["attempted"], expected["saved"] = !want.Cached, !want.Cached, !want.Cached
	}
	encoded, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, encoded)
}

func parityMetadataLegacyLocation(home, site, name string) (string, string) {
	// The fixture always authors this absolute config path and sets its owned
	// XDG_CACHE_HOME on every platform. Neither default user path participates.
	identity := filepath.Join(home, "legacy.toml") + "\x00" + strings.TrimRight(strings.ToLower(strings.TrimSpace(site)), "/") + "\x00fixture"
	digest := sha256.Sum256([]byte(identity))
	profile := fmt.Sprintf("fixture-%x", digest[:8])
	return profile, filepath.Join(home, "jira-cli", profile, name+".json")
}

func parityMetadataLegacyWrapper(raw []byte, home, site, name, path string) (json.RawMessage, time.Time, bool) {
	var wrapper struct {
		Profile, Resource string
		Schema            int
		FetchedAt         time.Time `json:"fetched_at"`
		Data              json.RawMessage
	}
	profile, expectedPath := parityMetadataLegacyLocation(home, site, name)
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &wrapper) != nil || wrapper.Profile != profile || wrapper.Resource != name || wrapper.Schema != 1 || wrapper.FetchedAt.IsZero() || filepath.Clean(path) != expectedPath {
		return nil, time.Time{}, false
	}
	return wrapper.Data, wrapper.FetchedAt, true
}

// Inspect only an owned synthetic home. Decode the persisted wrapper and native
// payload independently so an acknowledgement alone cannot earn storage credit.
func parityMetadataDisk(t *testing.T, home, lane, site, name string, native []json.RawMessage, pages int, truncated ...string) time.Time {
	t.Helper()
	if len(truncated) > 1 || lane == "before" && len(truncated) != 0 {
		t.Fatal("unsupported independent metadata snapshot expectation")
	}
	logical := "v1:metadata:" + name
	if name == "linktypes" {
		logical = "v1:link-types:" + site
	}
	identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len("fixture@example.invalid"), "fixture@example.invalid")
	wantKey := fmt.Sprintf("scope-v2:%x:%s", sha256.Sum256([]byte(identity)), logical)
	var stamp time.Time
	found := 0
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("owned cache fixture contains a nonregular JSON entry")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var wrapper struct {
			Key, Resource string
			Written       time.Time
			Value         []byte
		}
		if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &wrapper) != nil {
			return fmt.Errorf("owned cache wrapper is malformed")
		}
		var items json.RawMessage
		if lane == "before" {
			if wrapper.Resource != name {
				return nil
			}
			var ok bool
			items, stamp, ok = parityMetadataLegacyWrapper(raw, home, site, name, path)
			if !ok {
				return fmt.Errorf("legacy cache wrapper has the wrong profile, schema or owned path")
			}
		} else {
			if wrapper.Key != wantKey {
				return nil
			}
			stamp = wrapper.Written
			if name == "linktypes" {
				items = wrapper.Value
			} else {
				var snapshot map[string]json.RawMessage
				if !jsontext.Value(wrapper.Value).IsValid() || json.Unmarshal(wrapper.Value, &snapshot) != nil {
					return fmt.Errorf("owned metadata snapshot is malformed")
				}
				facts := map[string]any{"version": 1, "items": native, "count": len(native), "pages": pages, "complete": len(truncated) == 0}
				if len(truncated) == 1 {
					facts["next_start"], facts["truncated_reason"] = len(native), truncated[0]
				}
				expected, err := json.Marshal(facts)
				if err != nil || !parityAuthoringJSON(wrapper.Value, expected) {
					return fmt.Errorf("persisted metadata lost native values or collection facts")
				}
				items = snapshot["items"]
			}
		}
		expected, err := json.Marshal(native)
		if err != nil || !parityAuthoringJSON(items, expected) || stamp.IsZero() {
			return fmt.Errorf("persisted metadata differs from the independent native expectation")
		}
		found++
		return nil
	})
	if err != nil || found != 1 {
		t.Fatalf("independent owned metadata publication failed: matches %d; error %v", found, err)
	}
	return stamp
}

// Change only a previously verified owned wrapper. Backdating avoids wall-clock
// sleeps while the selected binaries still make their own freshness decision.
func parityMetadataBackdate(t *testing.T, home, lane, site, name string, prior time.Time) time.Time {
	t.Helper()
	identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len("fixture@example.invalid"), "fixture@example.invalid")
	logical := "v1:metadata:" + name
	if name == "linktypes" {
		logical = "v1:link-types:" + site
	}
	key := fmt.Sprintf("scope-v2:%x:%s", sha256.Sum256([]byte(identity)), logical)
	_, legacyPath := parityMetadataLegacyLocation(home, site, name)
	aged := time.Now().Add(-2 * time.Minute).UTC()
	found := 0
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("owned cache fixture contains a nonregular JSON entry")
		}
		raw, err := os.ReadFile(path)
		var wrapper map[string]json.RawMessage
		if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &wrapper) != nil {
			return fmt.Errorf("owned cache wrapper is unavailable or malformed")
		}
		stampKey := "written"
		if lane == "before" {
			if filepath.Clean(path) != legacyPath {
				return nil
			}
			if _, stamp, ok := parityMetadataLegacyWrapper(raw, home, site, name, path); !ok || !stamp.Equal(prior) {
				return fmt.Errorf("legacy wrapper changed before backdating")
			}
			stampKey = "fetched_at"
		} else {
			var observed string
			if json.Unmarshal(wrapper["key"], &observed) != nil || observed != key {
				return nil
			}
		}
		var stamp time.Time
		if json.Unmarshal(wrapper[stampKey], &stamp) != nil || !stamp.Equal(prior) {
			return fmt.Errorf("owned wrapper timestamp changed before backdating")
		}
		wrapper[stampKey], err = json.Marshal(aged)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(wrapper)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
		found++
		return nil
	})
	if err != nil || found != 1 {
		t.Fatalf("owned cache backdating failed: matches %d; error %v", found, err)
	}
	return aged
}

func TestParityActualBinaryMetadataPrimers(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, base := range parityMetadataFixtures(t) {
		for _, empty := range []bool{false, true} {
			fixture := base
			if empty {
				fixture.Native, fixture.Before = []json.RawMessage{}, []json.RawMessage{}
			}
			for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
				t.Run(fmt.Sprintf("%s/%s/empty=%t", fixture.Name, lane.name, empty), func(t *testing.T) {
					if lane.binary == "" {
						t.Skip("requires selected actual binary")
					}
					var calls, revision atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 1))
						endpoint, pageSize := fixture.Endpoint, 100
						if lane.name == "before" {
							if fixture.BeforeEndpoint != "" {
								endpoint = fixture.BeforeEndpoint
							}
							pageSize = fixture.BeforePageSize
						}
						paged := fixture.Paged && (lane.name != "before" || fixture.BeforeEndpoint == "")
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						wantQuery := url.Values{}
						start := 0
						if paged {
							var err error
							start, err = strconv.Atoi(query.Get("startAt"))
							if err != nil || start < 0 || start > 1 {
								t.Error("metadata walk changed its explicit offset")
							}
							wantQuery = url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(pageSize)}}
						}
						user, password, ok := r.BasicAuth()
						if err != nil || len(body) != 0 || queryErr != nil || !reflect.DeepEqual(query, wantQuery) || r.Method != "GET" || r.URL.Path != endpoint || !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							t.Error("metadata primer changed its exact authenticated GET/query/body")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						native, err := json.Marshal(fixture.Native)
						if err != nil {
							t.Error(err)
							return
						}
						if revision.Load() > 0 {
							native = []byte(strings.ReplaceAll(string(native), "Owned", "Refreshed"))
						}
						w.Header().Set("Content-Type", "application/json")
						switch {
						case paged:
							var rows []json.RawMessage
							if json.Unmarshal(native, &rows) != nil || start > 1 || start < 0 {
								http.Error(w, "wrong synthetic offset", http.StatusBadRequest)
								return
							}
							value := "[]"
							if len(rows) > start {
								value = "[" + string(rows[start]) + "]"
							}
							_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":1,"total":%d,"isLast":%t,"values":%s}`, start, len(rows), start+1 >= len(rows), value)
						case fixture.Name == "linktypes":
							_, _ = fmt.Fprintf(w, `{"issueLinkTypes":%s}`, native)
						default:
							_, _ = w.Write(native)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					began := time.Now()
					var previousWritten time.Time
					for _, tc := range []struct {
						name, mode string
						cached     bool
						force      bool
						ttl        int
						age        bool
					}{
						{"initial", "json", false, false, 0, false},
						{"cached", "compact", true, false, 0, false},
						{"forced", "json", false, true, 0, false},
						{"cached-after-force", "json", true, false, 0, false},
						{"custom-ttl-cache", "json", true, false, 7, false},
						{"aged-default-cache", "compact", true, false, 0, true},
						{"custom-ttl-expired", "json", false, false, 1, false},
						{"custom-ttl-refetched-cache", "compact", true, false, 1, false},
					} {
						if !t.Run(tc.name, func(t *testing.T) {
							if tc.force {
								revision.Store(1)
							}
							items := fixture.Native
							if lane.name == "before" {
								items = fixture.Before
							}
							if revision.Load() > 0 {
								var err error
								items, err = parityMetadataRefreshed(items)
								if err != nil {
									t.Fatal("invalid independent refreshed metadata expectation")
								}
							}
							args := []string{"cache", fixture.Name}
							if tc.age {
								pages := 1
								if !empty && fixture.Paged && (lane.name != "before" || fixture.BeforeEndpoint == "") {
									pages = 2
								}
								prior := parityMetadataDisk(t, home, lane.name, server.URL, fixture.Name, items, pages)
								aged := parityMetadataBackdate(t, home, lane.name, server.URL, fixture.Name, prior)
								if written := parityMetadataDisk(t, home, lane.name, server.URL, fixture.Name, items, pages); !written.Equal(aged) {
									t.Fatal("owned cache backdating did not retain its exact native snapshot")
								}
								previousWritten = aged
							}
							if tc.ttl > 0 {
								args = append(args, fmt.Sprintf("--ttl-minutes=%d", tc.ttl))
							}
							if tc.force {
								flag := "--force"
								if lane.name == "before" {
									flag = "--refresh"
								}
								args = append(args, flag)
							}
							beforeCalls, beforeFiles := calls.Load(), parityLocalTree(t, home)
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							readStarted := time.Now()
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Output: tc.mode}, []string{"JIRA_READ_ONLY=true"}, args...)
							readFinished := time.Now()
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							pages := 1
							if !empty && fixture.Paged && (lane.name != "before" || fixture.BeforeEndpoint == "") {
								pages = 2
							}
							wantCalls := int32(pages)
							if tc.cached {
								wantCalls = 0
								if !reflect.DeepEqual(beforeFiles, parityLocalTree(t, home)) {
									t.Fatal("fresh metadata reuse changed owned files")
								}
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load()-beforeCalls != wantCalls {
								t.Fatalf("metadata primer failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load()-beforeCalls, wantCalls, result.Stdout, result.Stderr)
							}
							written := parityMetadataDisk(t, home, lane.name, server.URL, fixture.Name, items, pages)
							if tc.cached {
								if previousWritten.IsZero() || !written.Equal(previousWritten) {
									t.Fatal("cached metadata changed its previous publication timestamp")
								}
							} else if written.Before(readStarted) || written.After(readFinished) {
								t.Fatal("fetched metadata retained an old or future publication timestamp")
							}
							previousWritten = written
							state := "fresh"
							if tc.name == "initial" {
								state = "missing"
							} else if tc.force && lane.name == "before" {
								state = "refresh"
							} else if tc.name == "custom-ttl-expired" {
								state = "stale"
							}
							want := parityMetadataExpected{Resource: fixture.Name, Profile: "fixture", State: state, Items: items, Pages: pages, Cached: tc.cached, Earliest: began, Latest: readFinished}
							if tc.age {
								want.Earliest = written
							}
							if lane.name == "before" {
								want.Stamp = written.UTC().Format(time.RFC3339)
							} else if tc.cached {
								want.Stamp = written.UTC().Format(time.RFC3339Nano)
							} else {
								// The fresh receipt is stamped after the store returns;
								// it acknowledges publication rather than restamping it.
								want.Earliest = written
							}
							data := result.Stdout
							if tc.mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "cache."+fixture.Name))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !parityMetadataProjection(data, lane.name, want) {
								t.Fatalf("metadata differs from independent native values or collection/publication facts: %s", result.Stdout)
							}
							id := fmt.Sprintf("%s.metadata.empty=%t.%s.%s", fixture.ID, empty, tc.name, tc.mode)
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						}) {
							break
						}
					}
				})
			}
		}
	}
	saveParityLocal(t, "metadata-fixture", evidence)
}

func TestParityMetadataLegacyProjectionMatchesIndependentFixtures(t *testing.T) {
	for _, fixture := range parityMetadataFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			items, ok := parityMetadataLegacyItems(fixture.Native, fixture.Name)
			got, err := json.Marshal(items)
			want, wantErr := json.Marshal(fixture.Before)
			if !ok || err != nil || wantErr != nil || !parityAuthoringJSON(got, want) {
				t.Fatal("legacy projection disagrees with the independently authored literal fixture")
			}
		})
	}
}

func TestParityMetadataProjectionRejectsChangedFacts(t *testing.T) {
	const native = `{"id":"customfield_10001","name":"Owned","schema":{"type":"string"},"future":900719925474099312345}`
	const before = `{"fields":[{"id":"customfield_10001","name":"Owned","type":"string"}],"profile":"fixture","count":1,"from_cache":true,"fetched_at":"2026-01-01T00:00:00Z","cache_state":"fresh","cache_source_state":"fresh","cache_empty":false}`
	const after = `{"dry_run":false,"items":[` + native + `],"count":1,"pages":1,"complete":true,"resource":"fields","would_fetch":false,"attempted":false,"from_cache":true,"cache_state":"fresh","saved":false,"written_at":"2026-01-01T00:00:00Z"}`
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid, items := after, []json.RawMessage{json.RawMessage(native)}
			if lane == "before" {
				valid = before
				items = []json.RawMessage{json.RawMessage(`{"id":"customfield_10001","name":"Owned","type":"string"}`)}
			}
			want := parityMetadataExpected{Resource: "fields", Profile: "fixture", State: "fresh", Stamp: stamp.Format(time.RFC3339), Items: items, Pages: 1, Cached: true, Earliest: stamp, Latest: stamp.Add(time.Second)}
			if !parityMetadataProjection([]byte(valid), lane, want) {
				t.Fatal("independent valid metadata receipt refused")
			}
			type change struct{ name, original, replacement string }
			cases := []change{
				{"identity", "customfield_10001", "customfield_10002"},
				{"name", `"name":"Owned"`, `"name":"Changed"`},
				{"count", `"count":1`, `"count":2`},
				{"cache", `"from_cache":true`, `"from_cache":false`},
				{"source", `"cache_state":"fresh"`, `"cache_state":"missing"`},
				{"timestamp", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z"},
				{"duplicate", `"count":1`, `"count":2,"count":1`},
				{"unexpected-null", `"count":1`, `"future_receipt":null,"count":1`},
			}
			if lane == "before" {
				cases = append(cases, change{"profile", `"profile":"fixture"`, `"profile":"other"`}, change{"resource", `"fields":[`, `"statuses":[`}, change{"native-type", `"type":"string"`, `"type":"number"`})
			} else {
				cases = append(cases,
					change{"native-type", `"type":"string"`, `"type":"number"`},
					change{"native-number", "900719925474099312345", "900719925474099312344"},
					change{"native-extension", `,"future":900719925474099312345`, ``},
					change{"resource", `"resource":"fields"`, `"resource":"statuses"`},
					change{"obsolete-ttl_minutes", `"pages":1`, `"pages":1,"ttl_minutes":0`},
					change{"pages", `"pages":1`, `"pages":2`},
					change{"completeness", `"complete":true`, `"complete":false`},
					change{"obsolete-force", `"pages":1`, `"pages":1,"force":false`},
					change{"obsolete-unbounded", `"pages":1`, `"pages":1,"unbounded":false`},
					change{"would-fetch", `"would_fetch":false`, `"would_fetch":true`},
					change{"attempted", `"attempted":false`, `"attempted":true`},
					change{"saved", `"saved":false`, `"saved":true`},
					change{"preview", `"dry_run":false`, `"dry_run":true`},
					change{"obsolete-age_seconds", `"pages":1`, `"pages":1,"age_seconds":0`},
				)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if parityMetadataProjection([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), lane, want) {
						t.Fatal("changed native value or collection/publication fact earned metadata credit")
					}
				})
			}
		})
	}
}

func TestParityMetadataLegacyWrapperRejectsForeignPublication(t *testing.T) {
	home := t.TempDir()
	profile, path := parityMetadataLegacyLocation(home, "https://example.invalid", "fields")
	valid := fmt.Sprintf(`{"profile":%q,"resource":"fields","schema":1,"fetched_at":"2026-01-01T00:00:00Z","data":[{"id":"summary","name":"Summary"}]}`, profile)
	if _, _, ok := parityMetadataLegacyWrapper([]byte(valid), home, "https://example.invalid", "fields", path); !ok {
		t.Fatal("independent owned legacy wrapper refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"wrong-profile", profile, "other-profile"},
		{"stale-schema", `"schema":1`, `"schema":0`},
		{"future-schema", `"schema":1`, `"schema":2`},
		{"missing-schema", `"schema":1,`, ``},
		{"null-schema", `"schema":1`, `"schema":null`},
		{"wrong-resource", `"resource":"fields"`, `"resource":"statuses"`},
		{"duplicate-schema", `"schema":1`, `"schema":0,"schema":1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := parityMetadataLegacyWrapper([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), home, "https://example.invalid", "fields", path); ok {
				t.Fatal("foreign or unusable legacy wrapper earned publication credit")
			}
		})
	}
	t.Run("wrong-owned-path", func(t *testing.T) {
		if _, _, ok := parityMetadataLegacyWrapper([]byte(valid), home, "https://example.invalid", "fields", filepath.Join(home, "elsewhere", "fields.json")); ok {
			t.Fatal("a wrapper outside its selected namespace earned publication credit")
		}
	})
}

func TestParityMetadataPublicationChronology(t *testing.T) {
	const valid = `{"dry_run":false,"items":["owned"],"count":1,"pages":1,"complete":true,"resource":"labels","would_fetch":true,"attempted":true,"from_cache":false,"cache_state":"missing","saved":true,"written_at":"2026-01-01T00:00:00.000001Z"}`
	disk := time.Date(2026, 1, 1, 0, 0, 0, 500, time.UTC)
	want := parityMetadataExpected{Resource: "labels", State: "missing", Items: []json.RawMessage{json.RawMessage(`"owned"`)}, Pages: 1, Earliest: disk, Latest: disk.Add(time.Second)}
	if !parityMetadataProjection([]byte(valid), "after", want) {
		t.Fatal("receipt acknowledging an earlier disk publication was refused")
	}
	for _, tc := range []struct{ name, stamp string }{{"before-disk-publication", "2026-01-01T00:00:00.000000001Z"}, {"after-command-finish", "2026-01-01T00:00:02Z"}} {
		t.Run(tc.name, func(t *testing.T) {
			if parityMetadataProjection([]byte(strings.Replace(valid, "2026-01-01T00:00:00.000001Z", tc.stamp, 1)), "after", want) {
				t.Fatal("impossible metadata publication chronology earned storage credit")
			}
		})
	}
}

func TestParityMetadataRefreshedPreservesSourceBuffers(t *testing.T) {
	const original = `{"id":"owned","name":"Owned","future":900719925474099312345}`
	source := []json.RawMessage{json.RawMessage(original)}
	refreshed, err := parityMetadataRefreshed(source)
	if err != nil || len(refreshed) != 1 || string(source[0]) != original || !parityAuthoringJSON(refreshed[0], []byte(`{"id":"owned","name":"Refreshed","future":900719925474099312345}`)) {
		t.Fatal("refresh changed its source fixture or lost an exact native value")
	}
	refreshed[0][0] = '!'
	if string(source[0]) != original {
		t.Fatal("refreshed raw bytes alias the immutable native fixture")
	}
}
