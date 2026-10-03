package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// ScopedCache isolates metadata by site and configured account. It hashes only
// non-secret identity, never an API token. Length prefixes keep arbitrary tuple
// components unambiguous. Disk namespaces and filenames disclose no identity;
// private entries retain logical keys for resource-specific management.
// Legacy flat disk entries remain untouched and cannot satisfy this namespace.
type ScopedCache struct {
	store   core.Cache
	prefix  string
	managed core.ManagedCache
}

func NewScopedCache(store core.Cache, site, email string) (*ScopedCache, error) {
	if store == nil || xstrings.AnyEmpty(site, email) {
		return nil, fmt.Errorf("cache scope requires storage, site and account")
	}
	identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len(email), email)
	hash := sha256.Sum256([]byte(identity))
	scoped := &ScopedCache{store: store, prefix: fmt.Sprintf("scope-v2:%x:", hash)}
	if disk, ok := store.(*DiskCache); ok {
		// A dedicated directory makes malformed files attributable to this
		// namespace without opening or deleting another account's metadata.
		partition := &DiskCache{dir: filepath.Join(disk.dir, fmt.Sprintf("scope-v2-%x", hash)), now: disk.now}
		scoped.store = partition
		scoped.managed = partition
	}
	return scoped, nil
}

func (c *ScopedCache) Get(key string) ([]byte, time.Time, bool) { return c.store.Get(c.prefix + key) }

func (c *ScopedCache) Set(key string, value []byte) error { return c.store.Set(c.prefix+key, value) }

func (c *ScopedCache) Forget(key string) error { return c.store.Forget(c.prefix + key) }

func (c *ScopedCache) Entries(ctx context.Context) ([]core.CacheEntry, error) {
	if c.managed == nil {
		return nil, core.ErrCacheManagementUnsupported
	}
	entries, err := c.managed.Entries(ctx)
	for i := range entries {
		if strings.HasPrefix(entries[i].Key, c.prefix) {
			entries[i].Key = strings.TrimPrefix(entries[i].Key, c.prefix)
		} else {
			entries[i].Key = ""
			entries[i].Malformed = true
		}
	}
	return entries, err
}

func (c *ScopedCache) DeleteEntry(ctx context.Context, id string) (bool, error) {
	if c.managed == nil {
		return false, core.ErrCacheManagementUnsupported
	}
	return c.managed.DeleteEntry(ctx, id)
}
