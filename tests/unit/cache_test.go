package unit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cache"
)

// TestCacheRoundTrip exercises the read-then-write path: writing a payload
// makes Read return ok=true, the same data, and stale=false within the TTL.
func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	payload := json.RawMessage(`["a","b","c"]`)
	if _, err := cache.Write("default", "labels", payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entry, ok, stale, err := cache.Read("default", "labels", time.Hour)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read ok=false; expected true after Write")
	}
	if stale {
		t.Fatal("entry reported stale immediately after Write")
	}
	// Cache files are indent-formatted for `cat | jq`, so compare semantically.
	var got, want []string
	if err := json.Unmarshal(entry.Data, &got); err != nil {
		t.Fatalf("decode cached data: %v", err)
	}
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !equalStrings(got, want) {
		t.Fatalf("data round-trip = %v, want %v", got, want)
	}
}

func TestCacheKeySeparatesConfigSiteAndProfile(t *testing.T) {
	a := cache.Key("default", "https://one.atlassian.net", "/tmp/a/config.toml")
	b := cache.Key("default", "https://two.atlassian.net", "/tmp/a/config.toml")
	c := cache.Key("default", "https://one.atlassian.net", "/tmp/b/config.toml")
	d := cache.Key("work", "https://one.atlassian.net", "/tmp/a/config.toml")
	if a == b || a == c || a == d {
		t.Fatalf("cache keys collided: a=%q b=%q c=%q d=%q", a, b, c, d)
	}
	if !strings.HasPrefix(a, "default-") || strings.Contains(a, "/") {
		t.Fatalf("cache key = %q, want filesystem-safe profile-prefixed key", a)
	}
}

func TestCacheKeyNormalizesSiteTrailingSlash(t *testing.T) {
	a := cache.Key("default", "https://one.atlassian.net", "/tmp/a/config.toml")
	b := cache.Key("default", "https://one.atlassian.net/", "/tmp/a/config.toml")
	if a != b {
		t.Fatalf("cache keys differ for trailing slash: %q vs %q", a, b)
	}
}

func TestCachePathUsesXDGCacheHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	path, err := cache.Path("default", "labels")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(root, "jira-cli", "default", "labels.json")
	if path != want {
		t.Fatalf("cache path = %q, want %q", path, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCacheStaleness asserts that an entry older than the TTL reports
// stale=true while still returning the cached value (callers can choose
// to use stale data while triggering a refresh).
func TestCacheStaleness(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := cache.Write("default", "labels", json.RawMessage(`[]`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Backdate the file so any positive TTL declares it stale.
	path, _ := cache.Path("default", "labels")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	// The stale check uses fetched_at from the entry body, not mtime; rewrite
	// the entry with an old timestamp to exercise the freshness branch.
	stale := cache.Entry{
		Profile:   "default",
		Resource:  "labels",
		Schema:    cache.SchemaVersion,
		FetchedAt: old.UTC(),
		Data:      json.RawMessage(`["old"]`),
	}
	body, _ := json.Marshal(stale)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("rewrite stale entry: %v", err)
	}

	entry, ok, isStale, err := cache.Read("default", "labels", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Read ok=%v err=%v", ok, err)
	}
	if !isStale {
		t.Fatalf("entry from %s with 1h TTL should be stale", old)
	}
	if string(entry.Data) != `["old"]` {
		t.Fatalf("stale entry data = %s", entry.Data)
	}
}

// TestCacheReadMissing reports ok=false (not an error) when the cache file
// does not exist. Callers use this to distinguish "cold cache" from "broken
// cache" — without it they'd have to treat any os.ErrNotExist as a fatal.
func TestCacheReadMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, ok, _, err := cache.Read("default", "labels", time.Hour)
	if err != nil {
		t.Fatalf("Read missing: err=%v, want nil", err)
	}
	if ok {
		t.Fatalf("Read missing: ok=true, want false")
	}
}

// TestCacheClearSingle removes one resource and leaves siblings intact.
func TestCacheClearSingle(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := cache.Write("default", "labels", json.RawMessage(`[]`)); err != nil {
		t.Fatalf("Write labels: %v", err)
	}
	if _, err := cache.Write("default", "projects", json.RawMessage(`[]`)); err != nil {
		t.Fatalf("Write projects: %v", err)
	}
	removed, err := cache.Clear("default", "labels")
	if err != nil || !removed {
		t.Fatalf("Clear labels: removed=%v err=%v", removed, err)
	}
	if _, ok, _, _ := cache.Read("default", "labels", time.Hour); ok {
		t.Fatal("Clear labels did not remove the file")
	}
	if _, ok, _, _ := cache.Read("default", "projects", time.Hour); !ok {
		t.Fatal("Clear labels removed sibling projects file")
	}
}

// TestCacheClearProfile wipes the whole profile directory and counts files.
func TestCacheClearProfile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, r := range []string{"labels", "projects", "epics"} {
		if _, err := cache.Write("default", r, json.RawMessage(`[]`)); err != nil {
			t.Fatalf("Write %s: %v", r, err)
		}
	}
	n, err := cache.ClearProfile("default")
	if err != nil {
		t.Fatalf("ClearProfile: %v", err)
	}
	if n != 3 {
		t.Fatalf("ClearProfile removed %d files, want 3", n)
	}
}

// TestCachePathTraversalSafe pushes deliberately hostile profile and
// resource names through Path and confirms the resulting path stays under
// the cache root. Without sanitisation a profile name like "../../etc"
// would let a caller read or overwrite arbitrary files.
func TestCachePathTraversalSafe(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	for _, tc := range []struct{ profile, resource string }{
		{"../../etc", "passwd"},
		{"default", "../../etc/passwd"},
		{"default/../../etc", "labels"},
	} {
		path, err := cache.Path(tc.profile, tc.resource)
		if err != nil {
			t.Fatalf("Path(%q, %q): err=%v", tc.profile, tc.resource, err)
		}
		clean := filepath.Clean(path)
		expectedRoot := filepath.Join(root, "jira-cli")
		if !strings.HasPrefix(clean, expectedRoot+string(filepath.Separator)) && clean != expectedRoot {
			t.Fatalf("path escaped cache root: profile=%q resource=%q path=%q root=%q",
				tc.profile, tc.resource, clean, expectedRoot)
		}
	}
}

// TestCacheAtomicWrite confirms that the temp-file-then-rename strategy
// leaves no half-written file at the target path even if the writer
// crashes between Write and rename. We can't crash mid-write in a unit
// test, but we can confirm the file at the target path is always either
// missing OR a fully-decodable Entry — never a stray temp-file.
func TestCacheAtomicWrite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for i := range 5 {
		if _, err := cache.Write("default", "labels", json.RawMessage(`["x"]`)); err != nil {
			t.Fatalf("Write iter %d: %v", i, err)
		}
		path, _ := cache.Path("default", "labels")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile iter %d: %v", i, err)
		}
		var e cache.Entry
		if err := json.Unmarshal(b, &e); err != nil {
			t.Fatalf("iter %d: cache file is not a valid Entry: %v\n%s", i, err, b)
		}
	}
	// No leaked temp files in the directory.
	dir := filepath.Dir(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "jira-cli", "default", "labels.json"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leaked temp file in cache dir: %s", e.Name())
		}
	}
}
