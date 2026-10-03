package adapters

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

// MemoryCache is a process-lifetime core.Cache — enough for one CLI
// invocation's repeated metadata reads. The per-profile disk cache arrives
// with the cache layer; this keeps the seam real meanwhile. The zero value
// is ready to use.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	// now is the clock, injected so a test can age an entry without
	// sleeping. Nil means time.Now.
	now func() time.Time
}

// memoryEntry is one stored value and when it was written — the same
// pairing DiskCache persists, because core.Cache promises the write time
// from every implementation and a caller applying a TTL cannot tell them
// apart.
type memoryEntry struct {
	written time.Time
	value   []byte
}

var _ core.Cache = (*MemoryCache)(nil)

// Get returns the bytes stored under key. The result is a copy: handing
// out the stored slice would let one caller mutate what the next one
// reads.
func (c *MemoryCache) Get(key string) ([]byte, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, time.Time{}, false
	}
	return slices.Clone(e.value), e.written, true
}

// Set stores a copy of value under key — a copy for the same aliasing
// reason Get makes one. It never fails here; the error return is the
// seam's contract for the fallible disk-backed implementation.
func (c *MemoryCache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]memoryEntry)
	}
	written := time.Now()
	if c.now != nil {
		written = c.now()
	}
	c.entries[key] = memoryEntry{written: written, value: slices.Clone(value)}
	return nil
}

// Forget drops one entry. A key that was never stored is success, for
// DiskCache.Forget's reason: the caller asked for it to be gone.
func (c *MemoryCache) Forget(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

// Keys returns the stored keys, sorted. Exported for tests that assert
// WHICH key a caller wrote under — the cache key carries the site, and a
// key that dropped it would serve one tenant's data to another.
func (c *MemoryCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.entries))
}
