//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func cacheRefreshData(t *testing.T, result mx3Result) envelope.CacheRefreshOutput {
	t.Helper()
	frame := mx3Decode(t, result)
	var out envelope.CacheRefreshOutput
	if result.Exit != 0 || !frame.OK || json.Unmarshal(frame.Data, &out) != nil {
		t.Fatalf("refresh failed: exit=%d stdout=%s stderr=%s", result.Exit, result.Stdout, result.Stderr)
	}
	return out
}

func TestCacheRefreshFlagsAffectActualFreshnessAndOfflinePreview(t *testing.T) {
	work := t.TempDir()
	rec := &mx3Recorder{}
	site := mx3Stub(t, cachePrimeHandler(rec))
	run := func(flags ...string) envelope.CacheRefreshOutput {
		t.Helper()
		return cacheRefreshData(t, mx3Run(t, work, site, append([]string{"cache", "refresh", "fields", "labels", "--output=json"}, flags...)...))
	}
	preview := run("--dry-run")
	if len(preview.Results) != 2 || !preview.DryRun || len(rec.all()) != 0 {
		t.Fatal("refresh preview fetched metadata")
	}
	for _, row := range preview.Results {
		if row.Data == nil || !row.Data.WouldFetch || row.Data.Attempted || row.Data.Saved {
			t.Fatal("refresh preview fabricated storage")
		}
	}
	if _, err := os.Stat(filepath.Join(work, "cache")); !os.IsNotExist(err) {
		t.Fatal("refresh preview created storage")
	}
	Prove(t, "jira cache refresh", "dry-run", "selected cold resources report plans without requests or cache storage")
	fresh := run()
	if len(fresh.Results) != 2 {
		t.Fatal("refresh warmup omitted selected resource")
	}
	calls := len(rec.all())
	control := run("--force=false")
	for _, row := range control.Results {
		if row.Data == nil || !row.Data.FromCache || row.Data.Attempted {
			t.Fatal("false force bypassed fresh metadata")
		}
	}
	if len(rec.all()) != calls {
		t.Fatal("false force fetched fresh metadata")
	}
	forced := run("--force")
	for _, row := range forced.Results {
		if row.Data == nil || !row.Data.Attempted || !row.Data.Saved || row.Data.FromCache {
			t.Fatal("true force did not replace metadata")
		}
	}
	if len(rec.all()) <= calls {
		t.Fatal("true force made no native reads")
	}
	Prove(t, "jira cache refresh", "force", "false reuses both stored resources; true makes native reads and acknowledges replacement")
	mx3AgeCacheEntries(t, work, 2*time.Minute)
	calls = len(rec.all())
	control = run("--ttl-minutes=0")
	for _, row := range control.Results {
		if row.Data == nil || !row.Data.FromCache || row.Data.CacheState != "fresh" {
			t.Fatal("resource default ignored freshness")
		}
	}
	if len(rec.all()) != calls {
		t.Fatal("default TTL fetched two-minute-old metadata")
	}
	shortened := run("--ttl-minutes=1")
	for _, row := range shortened.Results {
		if row.Data == nil || row.Data.FromCache || !row.Data.Saved || row.Data.CacheState != "stale" {
			t.Fatal("shortened TTL did not refresh aged resource")
		}
	}
	if len(rec.all()) <= calls {
		t.Fatal("shortened TTL made no native reads")
	}
	Prove(t, "jira cache refresh", "ttl-minutes", "default TTL reuses both aged snapshots; one minute makes both stale and fetches again")
}

func TestCacheRefreshUnboundedRefetchesCappedResource(t *testing.T) {
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
	resource := metacache.MetadataResources()[0]
	if err := store.Set(resource.Key(site), []byte(`{"version":1,"items":["Example"],"count":1,"pages":100,"complete":false,"truncated_reason":"max_pages","next_start":1}`)); err != nil {
		t.Fatal(err)
	}
	control := cacheRefreshData(t, mx3Run(t, work, site, "cache", "refresh", "labels", "--unbounded=false", "--output=json"))
	if len(control.Results) != 1 || control.Results[0].Data == nil || !control.Results[0].Data.FromCache || control.Results[0].Data.Complete || len(rec.all()) != 0 {
		t.Fatal("bounded refresh failed to reuse capped snapshot")
	}
	unbounded := cacheRefreshData(t, mx3Run(t, work, site, "cache", "refresh", "labels", "--unbounded", "--output=json"))
	if len(unbounded.Results) != 1 || unbounded.Results[0].Data == nil || unbounded.Results[0].Data.FromCache || !unbounded.Results[0].Data.Complete || !unbounded.Results[0].Data.Saved || len(rec.all()) == 0 {
		t.Fatal("unbounded refresh reused capped snapshot")
	}
	Prove(t, "jira cache refresh", "unbounded", "false reuses a capped label snapshot; true fetches and persists a complete walk")
}

func TestCacheRefreshParallelismChangesNativeOverlap(t *testing.T) {
	for _, workers := range []string{"1", "2"} {
		t.Run(workers, func(t *testing.T) {
			var active, peak, calls atomic.Int32
			rendezvous := make(chan struct{})
			base := cachePrimeHandler(&mx3Recorder{})
			site := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				width := active.Add(1)
				for old := peak.Load(); width > old && !peak.CompareAndSwap(old, width); old = peak.Load() {
				}
				index := calls.Add(1)
				if workers == "2" && index <= 2 {
					if index == 2 {
						close(rendezvous)
					}
					select {
					case <-rendezvous:
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
						t.Error("refresh --parallelism=2 did not dispatch its second request while the first was blocked")
					}
				}
				active.Add(-1)
				base.ServeHTTP(w, r)
			}))
			out := cacheRefreshData(t, mx3Run(t, t.TempDir(), site, "cache", "refresh", "fields", "statuses", "issuetypes", "--parallelism="+workers, "--output=json"))
			want := int32(1)
			if workers == "2" {
				want = 2
			}
			if len(out.Results) != 3 || peak.Load() != want {
				t.Fatalf("refresh --parallelism=%s peak=%d, want %d", workers, peak.Load(), want)
			}
		})
	}
	Prove(t, "jira cache refresh", "parallelism", "one worker serializes actual reads; two overlaps exactly two native requests while retaining ordered results")
}
