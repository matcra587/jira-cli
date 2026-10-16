//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestParityActualBinaryCachePrimerFlags(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the admitted current binary")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "cache-primer-flags-local", evidence) })
	// Independent public TTL and command inventory, not the production catalog.
	for _, resource := range []struct {
		id, name  string
		ttl       int
		unbounded bool
	}{
		{"C034", "labels", 60, true},
		{"C037", "projects", 10080, true},
		{"C030", "epics", 240, true},
		{"C031", "fields", 20160, false},
		{"C033", "issuetypes", 43200, false},
		{"C035", "linktypes", 129600, false},
		{"C028", "boards", 40320, true},
		{"C040", "statuses", 43200, false},
		{"C036", "priorities", 129600, true},
		{"C039", "resolutions", 129600, true},
	} {
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"defaults", "custom", "nonpositive"} {
				t.Run(resource.name+"/"+mode+"/"+scenario, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						http.Error(w, "metadata preview entered HTTP", http.StatusBadGateway)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					args := []string{"cache", resource.name, "--dry-run"}
					ttl, force, unbounded := resource.ttl, false, false
					switch scenario {
					case "custom":
						ttl, force, unbounded = 7, true, resource.unbounded
						args = append(args, "--ttl-minutes=7", "--force")
						if unbounded {
							args = append(args, "--unbounded")
						}
					case "nonpositive":
						args = append(args, "--ttl-minutes=0")
					}
					before := parityLocalTree(t, home)
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("metadata preview contacted Jira or changed owned storage")
					}
					want := map[string]any{"dry_run": true, "items": []any{}, "count": 0, "pages": 0, "complete": false, "resource": resource.name, "ttl_minutes": ttl, "force": force, "unbounded": unbounded, "would_fetch": true, "attempted": false, "from_cache": false, "cache_state": "missing", "saved": false, "age_seconds": 0}
					parityCacheLocalData(t, result, lane.Name, mode, "cache."+resource.name, want)
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					id := fmt.Sprintf("%s.primer.flags.%s.%s", resource.id, scenario, mode)
					evidence.Checks[id] = []string{lane.Name}
				})
			}
		}
	}
}

func TestParityActualBinaryCacheCollectionBounds(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the admitted current binary")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "cache-bounds-fixture", evidence) })
	for _, fixture := range []struct {
		id, name, endpoint string
		width              int
	}{
		{"C034", "labels", "/rest/api/3/label", 1},
		{"C034", "labels", "/rest/api/3/label", 100},
		{"C037", "projects", "/rest/api/3/project/search", 1},
		{"C036", "priorities", "/rest/api/3/priority/search", 1},
		{"C039", "resolutions", "/rest/api/3/resolution/search", 1},
	} {
		t.Run(fmt.Sprintf("%s/width=%d", fixture.name, fixture.width), func(t *testing.T) {
			if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
				t.Fatal(err)
			}
			// Literal independent inventory: 101 pages exceed both public bounds.
			items := make([]json.RawMessage, 101*fixture.width)
			for i := range items {
				if fixture.name == "labels" {
					items[i] = json.RawMessage(fmt.Sprintf(`"owned-%05d"`, i))
				} else {
					items[i] = json.RawMessage(fmt.Sprintf(`{"id":"%d","key":"OWN%d","name":"Owned %d","future":900719925474099312345}`, i+1, i, i))
				}
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ordinal := int(calls.Add(1)) - 1
				start := ordinal * fixture.width
				query, queryErr := url.ParseQuery(r.URL.RawQuery)
				wantQuery := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"100"}}
				body, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
				user, token, authenticated := r.BasicAuth()
				if ordinal >= 101 || queryErr != nil || readErr != nil || len(body) != 0 || !reflect.DeepEqual(query, wantQuery) || r.Method != "GET" || r.URL.Path != fixture.endpoint || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
					t.Error("collection bound changed exact authenticated request order, offset, page size or body")
					http.Error(w, "invalid owned collection request", http.StatusBadRequest)
					return
				}
				rows, err := json.Marshal(items[start : start+fixture.width])
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"isLast":%t,"values":%s}`, start, fixture.width, len(items), ordinal == 100, rows)
			}))
			defer server.Close()
			home := t.TempDir()
			began := time.Now()
			var previousWritten time.Time
			for _, step := range []struct {
				name              string
				cached, unbounded bool
				pages             int
			}{
				{"bounded", false, false, 100},
				{"capped-cache", true, false, 100},
				{"unbounded-refetch", false, true, 101},
				{"complete-cache", true, true, 101},
			} {
				if !t.Run(step.name, func(t *testing.T) {
					calls.Store(0)
					before := parityLocalTree(t, home)
					args := []string{"cache", fixture.name}
					if step.unbounded {
						args = append(args, "--unbounded")
					}
					readStarted := time.Now()
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{}, []string{"JIRA_READ_ONLY=true"}, args...)
					readFinished := time.Now()
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					wantCalls := step.pages
					if step.cached {
						wantCalls = 0
						if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatal("bounded cache reuse changed owned storage")
						}
					}
					if result.Exit != 0 || len(result.Stderr) != 0 || int(calls.Load()) != wantCalls {
						t.Fatalf("collection failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), wantCalls, result.Stdout, result.Stderr)
					}
					count := step.pages * fixture.width
					complete := step.pages == 101
					reason := "max_pages"
					if fixture.width == 100 {
						reason = "max_results"
					}
					var truncated []string
					warnings := []map[string]any{}
					if !complete {
						truncated = []string{reason}
						warnings = append(warnings, map[string]any{"type": "cache_truncated", "message": "Metadata is incomplete; inspect its resume cursor and truncated_reason. Use --force --unbounded to remove collection caps.", "lossy": true})
					}
					written := parityMetadataDisk(t, home, lane.Name, server.URL, fixture.name, items[:count], step.pages, truncated...)
					if step.cached {
						if previousWritten.IsZero() || !written.Equal(previousWritten) {
							t.Fatal("bounded cache reuse changed its previous publication timestamp")
						}
					} else if written.Before(readStarted) || written.After(readFinished) {
						t.Fatal("bounded fetch retained an old or future publication timestamp")
					}
					previousWritten = written
					data := parityAuthoringData(t, result.Stdout, lane.Name, "cache."+fixture.name, 0)
					var stamp string
					var age int64
					if json.Unmarshal(data["written_at"], &stamp) != nil || json.Unmarshal(data["age_seconds"], &age) != nil {
						t.Fatal("collection receipt lacks a timestamp or age")
					}
					observed, err := time.Parse(time.RFC3339Nano, stamp)
					minimumAge := int64(0)
					if step.cached {
						minimumAge = max(int64(readStarted.Sub(written).Seconds()), 0)
					}
					if err != nil || observed.Before(began) || observed.Before(written) || observed.After(readFinished) || step.cached && !observed.Equal(written) || age < minimumAge || age > int64(readFinished.Sub(written).Seconds())+1 || !step.cached && age != 0 {
						t.Fatal("collection receipt changed publication time or cache age")
					}
					ttl := 129600
					switch fixture.name {
					case "labels":
						ttl = 60
					case "projects":
						ttl = 10080
					}
					state := "fresh"
					if step.name == "bounded" {
						state = "missing"
					}
					want := map[string]any{"dry_run": false, "items": items[:count], "count": count, "pages": step.pages, "complete": complete, "resource": fixture.name, "ttl_minutes": ttl, "force": false, "unbounded": step.unbounded, "would_fetch": !step.cached, "attempted": !step.cached, "from_cache": step.cached, "cache_state": state, "saved": !step.cached, "age_seconds": age, "written_at": stamp}
					if !complete {
						want["next_start"], want["truncated_reason"] = count, reason
					}
					expected, err := json.Marshal(want)
					var frame map[string]json.RawMessage
					warningBytes, warningErr := json.Marshal(warnings)
					if err != nil || warningErr != nil || json.Unmarshal(result.Stdout, &frame) != nil || !parityAuthoringJSON(frame["data"], expected) || !parityAuthoringJSON(frame["warnings"], warningBytes) {
						t.Fatal("bounded collection lost exact native items, completeness, cursor or warning")
					}
					id := fmt.Sprintf("%s.bounds.width=%d.%s", fixture.id, fixture.width, step.name)
					evidence.Checks[id] = []string{lane.Name}
				}) {
					break
				}
			}
			if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
				t.Fatal(err)
			}
		})
	}
}
