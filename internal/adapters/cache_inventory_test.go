package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

func newInventoryScope(t *testing.T, dir, site, account string) *ScopedCache {
	t.Helper()
	cache, err := NewScopedCache(&DiskCache{dir: dir}, site, account)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestCacheInventoryIsolatesScopesAndLegacyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := newInventoryScope(t, dir, "https://one.example.com", "first@example.com")
	second := newInventoryScope(t, dir, "https://one.example.com", "second@example.com")
	otherSite := newInventoryScope(t, dir, "https://two.example.com", "first@example.com")
	legacy := DiskCache{dir: dir}
	if err := legacy.Set("unscoped", []byte("keep legacy")); err != nil {
		t.Fatal(err)
	}
	keys := []string{"v1:boards-list", "v2:board:https://one.example.com:id:12", "v2:board:https://one.example.com:name:Example board"}
	for _, scope := range []*ScopedCache{first, second, otherSite} {
		for _, key := range keys {
			if err := scope.Set(key, []byte("stored")); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := first.Entries(t.Context())
	if err != nil || len(entries) != len(keys) {
		t.Fatalf("inventory = %+v, %v", entries, err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Malformed || entry.Written.IsZero() || entry.SizeBytes != 6 || !validCacheEntryID(entry.ID) || strings.HasPrefix(entry.Key, "scope-") {
			t.Fatalf("invalid inventory receipt: %+v", entry)
		}
		seen[entry.Key] = true
		if removed, err := first.DeleteEntry(t.Context(), entry.ID); err != nil || !removed {
			t.Fatal(err)
		}
	}
	for _, key := range keys {
		if !seen[key] {
			t.Fatalf("inventory omitted selector %q", key)
		}
		if _, _, ok := first.Get(key); ok {
			t.Fatal("deleted selected entry survived")
		}
		for _, cache := range []*ScopedCache{second, otherSite} {
			if raw, _, ok := cache.Get(key); !ok || string(raw) != "stored" {
				t.Fatal("inventory clear crossed a site/account boundary")
			}
		}
	}
	if raw, _, ok := legacy.Get("unscoped"); !ok || string(raw) != "keep legacy" {
		t.Fatal("inventory clear modified legacy flat storage")
	}
}

func TestCacheInventoryRetainsMalformedEntriesWithoutTrustingKeys(t *testing.T) {
	t.Parallel()
	cache := newInventoryScope(t, t.TempDir(), "https://example.com", "person@example.com")
	disk := cache.store.(*DiskCache)
	if err := cache.Set("valid", []byte("data")); err != nil {
		t.Fatal(err)
	}
	for key, body := range map[string]string{"invalid-json": "{", "missing-key": `{"written":"2026-01-02T03:04:05Z","value":"ZA=="}`} {
		if err := os.WriteFile(disk.path(cache.prefix+key), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wrong, err := json.Marshal(diskEntry{Key: cache.prefix + "another-key", Written: time.Now(), Value: []byte("wrong")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk.path(cache.prefix+"mismatched"), wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.Get("mismatched"); ok {
		t.Fatal("read served a mismatched logical key")
	}
	junk := filepath.Join(disk.dir, "unrelated.txt")
	if err := os.WriteFile(junk, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := cache.Entries(t.Context())
	if err != nil || len(entries) != 4 {
		t.Fatalf("malformed inventory = %+v, %v", entries, err)
	}
	malformed := 0
	for _, entry := range entries {
		if entry.Malformed {
			malformed++
			if entry.Key != "" {
				t.Fatal("inventory trusted a malformed logical key")
			}
		}
		if removed, err := cache.DeleteEntry(t.Context(), entry.ID); err != nil || !removed {
			t.Fatal(err)
		}
	}
	if malformed != 3 {
		t.Fatalf("malformed count = %d", malformed)
	}
	if raw, err := os.ReadFile(junk); err != nil || string(raw) != "keep" {
		t.Fatal("inventory clear removed a non-cache file")
	}
}

func TestCacheInventoryRefusesTraversalCancellationAndDirectories(t *testing.T) {
	t.Parallel()
	cache := DiskCache{dir: t.TempDir()}
	if err := cache.Set("valid", []byte("data")); err != nil {
		t.Fatal(err)
	}
	id := filepath.Base(cache.path("valid"))
	for _, invalid := range []string{"", "../" + id, filepath.Join(cache.dir, id), strings.ToUpper(id), strings.Repeat("z", 64) + ".json", id + "/child", "\x00"} {
		if _, err := cache.DeleteEntry(t.Context(), invalid); err == nil {
			t.Fatalf("accepted invalid identity %q", invalid)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.DeleteEntry(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled deletion = %v", err)
	}
	if _, err := cache.Entries(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspection = %v", err)
	}
	if raw, _, ok := cache.Get("valid"); !ok || string(raw) != "data" {
		t.Fatal("refusal changed the entry")
	}
	directory := cache.path("directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.DeleteEntry(t.Context(), filepath.Base(directory)); err == nil {
		t.Fatal("deletion removed a directory masquerading as an entry")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal(err)
	}
}

func TestCacheInventoryMissingAndUnsupportedStorageAreDistinct(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	cache := newInventoryScope(t, dir, "https://example.com", "person@example.com")
	entries, err := cache.Entries(t.Context())
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("missing inventory = %+v, %v", entries, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection created storage")
	}
	id := filepath.Base(cache.store.(*DiskCache).path(cache.prefix + "absent"))
	if removed, err := cache.DeleteEntry(t.Context(), id); err != nil || removed {
		t.Fatalf("absent namespace fabricated removal: %v, %v", removed, err)
	}
	if err := cache.Set("present", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if removed, err := cache.DeleteEntry(t.Context(), id); err != nil || removed {
		t.Fatalf("absent entry fabricated removal: %v, %v", removed, err)
	}
	if raw, _, ok := cache.Get("present"); !ok || string(raw) != "data" {
		t.Fatal("absent deletion changed another entry")
	}
	memory, err := NewScopedCache(&MemoryCache{}, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Entries(t.Context()); !errors.Is(err, core.ErrCacheManagementUnsupported) {
		t.Fatal("unsupported management appeared to be an empty cache")
	}
	if _, err := memory.DeleteEntry(t.Context(), "missing"); !errors.Is(err, core.ErrCacheManagementUnsupported) {
		t.Fatal("unsupported deletion reported success")
	}
}

func TestCacheInventoryDoesNotFollowEntryOrNamespaceSymlinks(t *testing.T) {
	if xos.IsWindows() {
		t.Skip("native Windows symlink privileges require separate acceptance")
	}
	t.Parallel()
	cache := newInventoryScope(t, t.TempDir(), "https://example.com", "person@example.com")
	disk := cache.store.(*DiskCache)
	if err := cache.Set("entry", []byte("inside")); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside.json")
	before, err := os.ReadFile(disk.path(cache.prefix + "entry"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cache.Forget("entry"); err != nil {
		t.Fatal(err)
	}
	path := disk.path(cache.prefix + "entry")
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.Get("entry"); ok {
		t.Fatal("read followed an external entry symlink")
	}
	entries, err := cache.Entries(t.Context())
	if err != nil || len(entries) != 1 || !entries[0].Malformed || entries[0].Key != "" {
		t.Fatalf("symlink inventory = %+v, %v", entries, err)
	}
	if removed, err := cache.DeleteEntry(t.Context(), entries[0].ID); err != nil || !removed {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(external); err != nil || !bytes.Equal(raw, before) {
		t.Fatal("deleting a link modified its target")
	}
	if err := os.Remove(disk.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(external), disk.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Entries(t.Context()); err == nil {
		t.Fatal("inventory followed a namespace symlink")
	}
	if err := cache.Set("entry", []byte("outside overwrite")); err == nil {
		t.Fatal("publication followed a namespace symlink")
	}
	if _, err := cache.DeleteEntry(t.Context(), filepath.Base(disk.path(cache.prefix+"entry"))); err == nil {
		t.Fatal("deletion followed a namespace symlink")
	}
	if raw, err := os.ReadFile(external); err != nil || !bytes.Equal(raw, before) {
		t.Fatal("namespace refusal changed outside data")
	}
}

func TestCacheForgetRefusesNamespaceSymlink(t *testing.T) {
	if xos.IsWindows() {
		t.Skip("native Windows symlink privileges require separate acceptance")
	}
	t.Parallel()
	cache := DiskCache{dir: filepath.Join(t.TempDir(), "namespace")}
	outside := t.TempDir()
	path := filepath.Join(outside, filepath.Base(cache.path("entry")))
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, cache.dir); err != nil {
		t.Fatal(err)
	}
	if err := cache.Forget("entry"); err == nil {
		t.Fatal("forget followed a namespace symlink")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "preserve" {
		t.Fatal("forget changed outside data")
	}
}

func TestCacheReadAndInventoryRejectInternalEntrySymlink(t *testing.T) {
	if xos.IsWindows() {
		t.Skip("native Windows symlink privileges require separate acceptance")
	}
	t.Parallel()
	cache := DiskCache{dir: t.TempDir()}
	if err := cache.Set("entry", []byte("owned")); err != nil {
		t.Fatal(err)
	}
	path := cache.path("entry")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cache.dir, "owned-target")
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("owned-target", path); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.Get("entry"); ok {
		t.Fatal("cache read followed an internal symlink")
	}
	entries, err := cache.Entries(t.Context())
	if err != nil || len(entries) != 1 || !entries[0].Malformed || entries[0].Key != "" {
		t.Fatalf("inventory followed an internal symlink: %+v, %v", entries, err)
	}
	if removed, err := cache.DeleteEntry(t.Context(), entries[0].ID); err != nil || !removed {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(target); err != nil || !bytes.Equal(kept, raw) {
		t.Fatal("entry deletion changed an internal symlink target")
	}
}
