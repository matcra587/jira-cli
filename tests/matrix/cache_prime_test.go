//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func cachePrimeHandler(rec *mx3Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		row := `{"id":"1","name":"Example","key":"KEY","inward":"inside","outward":"outside","futureField":null}`
		body := "[" + row + "]"
		switch r.URL.Path {
		case "/rest/api/3/label":
			body = `{"startAt":0,"maxResults":100,"isLast":true,"values":["Example"]}`
		case "/rest/api/3/project/search", "/rest/api/3/priority/search", "/rest/api/3/resolution/search":
			body = `{"startAt":0,"maxResults":100,"isLast":true,"values":[` + row + `]}`
		case "/rest/api/3/issueLinkType":
			body = `{"issueLinkTypes":[` + row + `]}`
		case "/rest/api/3/search/jql":
			body = `{"isLast":true,"issues":[{"id":"1","key":"KEY-1","fields":{"summary":"Epic","futureField":null}}]}`
		case "/rest/agile/1.0/board":
			body = `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Example board","type":"scrum"}]}`
		case "/rest/agile/1.0/board/42/project":
			body = `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":"1","key":"KEY"}]}`
		}
		_, _ = w.Write([]byte(body))
	})
}

func cachePrimeData(t *testing.T, result mx3Result) envelope.CachePrimeOutput {
	t.Helper()
	frame := mx3Decode(t, result)
	var out envelope.CachePrimeOutput
	if result.Exit != 0 || !frame.OK || json.Unmarshal(frame.Data, &out) != nil {
		t.Fatalf("primer failed: exit=%d stdout=%s stderr=%s", result.Exit, result.Stdout, result.Stderr)
	}
	return out
}

func TestCachePrimerFlagsAffectActualFreshnessAndOfflinePreview(t *testing.T) {
	for _, resource := range metacache.MetadataResources() {
		t.Run(resource.Name, func(t *testing.T) {
			work := t.TempDir()
			rec := &mx3Recorder{}
			site := mx3Stub(t, cachePrimeHandler(rec))
			preview := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--dry-run", "--output=json"))
			if !preview.DryRun || !preview.WouldFetch || preview.Attempted || preview.Saved || len(rec.all()) != 0 {
				t.Fatal("preview fetched or wrote metadata")
			}
			if _, err := os.Stat(filepath.Join(work, "cache")); !os.IsNotExist(err) {
				t.Fatal("preview created storage")
			}
			Prove(t, "jira cache "+resource.Name, "dry-run", "cold preview reports its plan, makes no requests and creates no cache storage")
			fresh := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--output=json"))
			if !fresh.Attempted || !fresh.Saved || fresh.Count != 1 {
				t.Fatal("warm-up did not populate selected metadata")
			}
			calls := len(rec.all())
			control := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--force=false", "--output=json"))
			if !control.FromCache || control.Attempted || len(rec.all()) != calls {
				t.Fatal("explicit false force bypassed freshness")
			}
			forced := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--force", "--output=json"))
			if !forced.Attempted || !forced.Saved || forced.FromCache || len(rec.all()) <= calls {
				t.Fatal("force did not refetch fresh metadata")
			}
			Prove(t, "jira cache "+resource.Name, "force", "false reuses the stored entry; true makes new native reads and acknowledges replacement")
			mx3AgeCacheEntries(t, work, 2*time.Minute)
			calls = len(rec.all())
			control = cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--ttl-minutes=0", "--output=json"))
			if !control.FromCache || control.TTLMinutes != int(resource.DefaultTTL.Minutes()) || len(rec.all()) != calls {
				t.Fatal("nonpositive TTL failed resource default freshness")
			}
			shortened := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--ttl-minutes=1", "--output=json"))
			if shortened.FromCache || !shortened.Saved || shortened.TTLMinutes != 1 || len(rec.all()) <= calls {
				t.Fatal("short TTL did not refetch the same aged entry")
			}
			Prove(t, "jira cache "+resource.Name, "ttl-minutes", "resource default reuses the two-minute-old entry; one minute makes the same entry stale and fetches again")
		})
	}
}

func TestCachePrimerUnboundedRefetchesCappedProducerSnapshots(t *testing.T) {
	for _, resource := range metacache.MetadataResources() {
		if resource.Name == "fields" || resource.Name == "issuetypes" || resource.Name == "linktypes" || resource.Name == "statuses" {
			continue
		}
		t.Run(resource.Name, func(t *testing.T) {
			work := t.TempDir()
			rec := &mx3Recorder{}
			site := mx3Stub(t, cachePrimeHandler(rec))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(work, "cache"))
			disk, err := adapters.NewDiskCache()
			if err != nil {
				t.Fatal(err)
			}
			store, err := adapters.NewScopedCache(disk, site, "person@example.com")
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"version":1,"items":[{"id":"1","name":"Example","key":"KEY"}],"count":1,"pages":100,"complete":false,"truncated_reason":"max_pages","next_start":1}`
			switch resource.Name {
			case "labels":
				raw = strings.Replace(raw, `{"id":"1","name":"Example","key":"KEY"}`, `"Example"`, 1)
			case "epics":
				raw = `{"version":1,"items":[{"id":"1","key":"KEY-1","fields":{"summary":"Epic"}}],"count":1,"pages":50,"complete":false,"truncated_reason":"max_pages","next_page_token":"next"}`
			case "boards":
				encoded, encodeErr := jira.MarshalNoEscape(struct {
					Boards    []envelope.BoardRow            `json:"boards"`
					FetchedAt time.Time                      `json:"fetched_at"`
					Pages     int                            `json:"pages_fetched"`
					Complete  bool                           `json:"complete"`
					Truncated bool                           `json:"truncated"`
					Reason    string                         `json:"reason"`
					NextStart *int                           `json:"next_start"`
					Failures  []envelope.BoardProjectFailure `json:"project_failures"`
				}{Boards: []envelope.BoardRow{{ID: 42, Name: "Example", ProjectKeys: []string{"KEY"}, ProjectsComplete: true}}, FetchedAt: time.Now().UTC(), Pages: 100, Truncated: true, Reason: "max_pages", NextStart: new(1), Failures: []envelope.BoardProjectFailure{}})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				raw = string(encoded)
			}
			if err := store.Set(resource.Key(site), []byte(raw)); err != nil {
				t.Fatal(err)
			}
			bounded := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--unbounded=false", "--output=json"))
			if !bounded.FromCache || bounded.Complete || len(rec.all()) != 0 {
				t.Fatal("bounded read failed to reuse capped snapshot")
			}
			unbounded := cachePrimeData(t, mx3Run(t, work, site, "cache", resource.Name, "--unbounded", "--output=json"))
			if unbounded.FromCache || !unbounded.Attempted || !unbounded.Saved || !unbounded.Complete || len(rec.all()) == 0 {
				t.Fatal("unbounded retained capped snapshot")
			}
			Prove(t, "jira cache "+resource.Name, "unbounded", "false reuses the exact owning capped snapshot; true refetches it and persists a complete native walk")
		})
	}
}
