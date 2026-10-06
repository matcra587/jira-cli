package adapters

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDiskCacheFailedPublicationRemovesTemporaryFile(t *testing.T) {
	t.Parallel()
	cache := DiskCache{dir: t.TempDir()}
	// A nonempty directory at the destination makes rename fail even when
	// the process can write the parent directory (including when run as root).
	destination := cache.path("boards")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(destination, "keep")
	if err := os.WriteFile(kept, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cache.Set("boards", []byte("new value"))
	if err == nil {
		t.Fatal("failed publication reported success")
	}
	var renameError *os.LinkError
	nativeFailure := nativeCacheRenameFailure(t, filepath.Base(destination))
	if !errors.As(err, &renameError) || renameError.Op != nativeFailure.Op ||
		renameError.New != nativeFailure.New || !errors.Is(err, nativeFailure.Err) {
		t.Fatalf("publication did not preserve the rename error: %v", err)
	}
	entries, err := os.ReadDir(cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Fatalf("failed publication left extra files: %v", entries)
	}
	raw, err := os.ReadFile(kept)
	if err != nil || string(raw) != "untouched" {
		t.Fatalf("failed publication changed destination: %q, %v", raw, err)
	}
}

// Root.Rename's native operation name differs across platforms. Obtain its
// cause independently of DiskCache rather than assuming os.Rename's shape.
func nativeCacheRenameFailure(t *testing.T, destination string) *os.LinkError {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := root.WriteFile("source", []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(filepath.Join(destination, "keep"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = root.Rename("source", destination)
	var nativeFailure *os.LinkError
	if !errors.As(err, &nativeFailure) {
		t.Fatalf("native Root.Rename probe did not fail with a link error: %v", err)
	}
	return nativeFailure
}

func TestDiskCachePersistsExactBytesTimestampAndPrivatePermissions(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cache")
	written := time.Date(2026, time.January, 2, 3, 4, 5, 678, time.UTC)
	cache := DiskCache{dir: dir, now: func() time.Time { return written }}
	key := "https://example.com/path/with:unsafe-filename"
	value := []byte{0, 0xff, '\n', '<', '&'}
	for _, body := range [][]byte{[]byte("old"), value} {
		if err := cache.Set(key, body); err != nil {
			t.Fatal(err)
		}
	}
	other := DiskCache{dir: dir}
	raw, timestamp, ok := other.Get(key)
	if !ok || !bytes.Equal(raw, value) || !timestamp.Equal(written) {
		t.Fatalf("independent read = %q, %v, %v", raw, timestamp, ok)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, %v", entries, err)
	}
	if entries[0].Name() != filepath.Base(cache.path(key)) {
		t.Fatal("published filename does not match the cache key")
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{dir: 0o700, cache.path(key): 0o600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("private permissions on %s: %v, %v", path, info, err)
			}
		}
	}
}

func TestScopedDiskCachePersistsAndForgetsOnlySelectedSiteAccount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scope := func(site, account string) *ScopedCache {
		t.Helper()
		cache, err := NewScopedCache(&DiskCache{dir: dir}, site, account)
		if err != nil {
			t.Fatal(err)
		}
		return cache
	}
	first := scope("https://one.example.com", "first@example.com")
	second := scope("https://one.example.com", "second@example.com")
	otherSite := scope("https://two.example.com", "first@example.com")
	for i, cache := range []*ScopedCache{first, second, otherSite} {
		if err := cache.Set("metadata", []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if raw, _, ok := scope("https://one.example.com", "first@example.com").Get("metadata"); !ok || !bytes.Equal(raw, []byte{0}) {
		t.Fatal("new instance failed to read selected scope")
	}
	if err := first.Forget("metadata"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := first.Get("metadata"); ok {
		t.Fatal("forgotten entry survived")
	}
	for i, cache := range []*ScopedCache{second, otherSite} {
		if raw, _, ok := cache.Get("metadata"); !ok || !bytes.Equal(raw, []byte{byte(i + 1)}) {
			t.Fatal("forget crossed a site/account boundary")
		}
	}
}

func TestDiskCacheConcurrentReplacementNeverPublishesPartialEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache := DiskCache{dir: dir}
	values := [][]byte{bytes.Repeat([]byte("first"), 4096), bytes.Repeat([]byte("second"), 4096)}
	if err := cache.Set("metadata", values[0]); err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	failures := make(chan error, 2)
	for _, value := range values {
		writers.Go(func() {
			other := DiskCache{dir: dir}
			for range 12 {
				if err := other.Set("metadata", value); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	done := make(chan struct{})
	go func() {
		writers.Wait()
		close(done)
	}()
	// Join writers before TempDir cleanup, including on a failed observation.
	defer writers.Wait()
	for {
		raw, err := os.ReadFile(cache.path("metadata"))
		if err != nil {
			t.Fatal(err)
		}
		var entry diskEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("reader observed incomplete publication: %v", err)
		}
		if !bytes.Equal(entry.Value, values[0]) && !bytes.Equal(entry.Value, values[1]) {
			t.Fatal("reader observed bytes from mixed publications")
		}
		select {
		case <-done:
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}
