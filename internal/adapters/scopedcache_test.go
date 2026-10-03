package adapters

import (
	"strings"
	"testing"
)

func TestScopedCacheIsolatesSiteAccountAndLegacyEntries(t *testing.T) {
	t.Parallel()
	var store MemoryCache
	first, err := NewScopedCache(&store, "https://one.example.com", "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := NewScopedCache(&store, "https://one.example.com", "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	otherSite, err := NewScopedCache(&store, "https://two.example.com", "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set("boards", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("boards", []byte("legacy")); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []*ScopedCache{otherAccount, otherSite} {
		if _, _, ok := scope.Get("boards"); ok {
			t.Fatal("scope served another account/site or an unscoped entry")
		}
	}
	if err := otherAccount.Set("boards", []byte("other")); err != nil {
		t.Fatal(err)
	}
	if err := first.Forget("boards"); err != nil {
		t.Fatal(err)
	}
	if raw, _, ok := otherAccount.Get("boards"); !ok || string(raw) != "other" {
		t.Fatal("forget crossed account scope")
	}
	for _, key := range store.Keys() {
		if strings.Contains(key, "example.com") {
			t.Fatal("storage key discloses configured account/site")
		}
	}
}

func TestScopedCacheTupleIsUnambiguous(t *testing.T) {
	t.Parallel()
	var store MemoryCache
	first, err := NewScopedCache(&store, "ab", "c")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewScopedCache(&store, "a", "bc")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set("same", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := second.Get("same"); ok {
		t.Fatal("scope tuple collided")
	}
}
