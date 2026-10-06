package adapters

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCacheEntryRejectsNonOpaqueLeaves(t *testing.T) {
	t.Parallel()
	cache := DiskCache{}
	valid := filepath.Base(cache.path("entry"))
	for _, name := range []string{"", ".", "../" + valid, "parent/" + valid, strings.ToUpper(valid), strings.Repeat("z", 64) + ".json"} {
		// A malformed leaf must be rejected before any root method is reached.
		raw, err := readCacheEntry(nil, name)
		if len(raw) != 0 || !errors.Is(err, errUnsafeCacheEntry) {
			t.Fatalf("readCacheEntry(%q) = %q, %v; want an unsafe-leaf refusal", name, raw, err)
		}
	}
}
