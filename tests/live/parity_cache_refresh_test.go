//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
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

func parityCacheRefreshPreview(data []byte, lane string, repeated bool) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	names := []string{"fields", "labels"}
	if repeated && lane == "before" {
		names = append(names, "fields")
	}
	var actual struct {
		Results []struct{ Data map[string]json.RawMessage }
	}
	if json.Unmarshal(data, &actual) != nil || len(actual.Results) != len(names) {
		return false
	}
	rows := make([]map[string]any, 0, len(names))
	for i, name := range names {
		switch lane {
		case "before":
			var duration int64
			if json.Unmarshal(actual.Results[i].Data["duration_ms"], &duration) != nil || duration < 0 || duration > 15000 {
				return false
			}
			rows = append(rows, map[string]any{"key": name, "ok": true, "data": map[string]any{"status": "would-refresh", "from_cache": false, "count": 0, "fetched_at": "0001-01-01T00:00:00Z", "duration_ms": duration, "dry_run": true}})
		case "after":
			rows = append(rows, map[string]any{"resource": name, "ok": true, "data": map[string]any{"dry_run": true, "items": []any{}, "count": 0, "pages": 0, "complete": false, "resource": name, "would_fetch": true, "attempted": false, "from_cache": false, "cache_state": "missing", "saved": false}})
		default:
			return false
		}
	}
	want := map[string]any{"results": rows, "dry_run": true, "parallelism": 2}
	if lane == "before" {
		want = map[string]any{"results": rows, "succeeded": len(rows), "failed": 0}
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func TestParityActualBinaryCacheRefreshPreview(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				t.Error("metadata preview entered HTTP")
				http.Error(w, "unexpected request", http.StatusTeapot)
			}))
			defer server.Close()
			for _, repeated := range []bool{false, true} {
				for _, mode := range []string{"json", "compact"} {
					t.Run(fmt.Sprintf("repeated=%t/%s", repeated, mode), func(t *testing.T) {
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						before := parityLocalTree(t, home)
						args := []string{"cache", "refresh", "fields", "labels"}
						if repeated {
							args = append(args, "fields")
						}
						args = append(args, "--dry-run", "--parallelism=2")
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						began := time.Now()
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 {
							t.Fatalf("metadata preview failed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" {
							var err error
							data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "cache.refresh"))
							if err != nil {
								t.Fatal(err)
							}
						}
						if !parityCacheRefreshPreview(data, lane.name, repeated) {
							t.Fatalf("metadata preview lost resource identity, ordering, deduplication or plan facts: %s", result.Stdout)
						}
						// Alter observed receipts only after binding them to the
						// independent expected plan. These guards earn no new pairs.
						for _, mutation := range []struct{ name, original, replacement string }{
							{"wrong-resource", `"fields"`, `"statuses"`},
							{"wrong-count", `"count":0`, `"count":1`},
							{"false-success", `"ok":true`, `"ok":false`},
							{"false-live", `"dry_run":true`, `"dry_run":false`},
							{"extra-row", `"results":[`, `"results":[{"ok":true},`},
						} {
							t.Run("guard/"+mutation.name, func(t *testing.T) {
								changed := strings.Replace(string(data), mutation.original, mutation.replacement, 1)
								if changed == string(data) {
									t.Fatal("preview receipt mutation did not apply")
								}
								if parityCacheRefreshPreview([]byte(changed), lane.name, repeated) {
									t.Fatal("changed resource identity or preview plan earned batch acceptance")
								}
							})
						}
						after := parityLocalTree(t, home)
						if lane.name == "before" {
							// Legacy keyed output records resource names as issue keys,
							// even on previews. Keep that actual side effect explicit.
							written := parityMetadataDisk(t, home, "before", server.URL, "issuekeys", []json.RawMessage{json.RawMessage(`"FIELDS"`), json.RawMessage(`"LABELS"`)}, 0)
							if written.Before(began) || written.After(time.Now()) {
								t.Fatal("legacy preview history has an impossible publication time")
							}
							_, history := parityMetadataLegacyLocation(home, server.URL, "issuekeys")
							for _, path := range []string{filepath.Dir(filepath.Dir(history)), filepath.Dir(history), history} {
								relative, err := filepath.Rel(home, path)
								if err != nil {
									t.Fatal(err)
								}
								file, present := after[relative]
								if !present || path != history && !file.Mode.IsDir() || path == history && !file.Mode.IsRegular() {
									t.Fatal("legacy preview did not publish its exact owned history path")
								}
								before[relative] = file
							}
						}
						if !reflect.DeepEqual(before, after) {
							t.Fatal("preview changed files beyond its explicit lane-specific history outcome")
						}
						id := fmt.Sprintf("C038.refresh.preview.repeated=%t.%s", repeated, mode)
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "cache-refresh-preview", evidence)
}
