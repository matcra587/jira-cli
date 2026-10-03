package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/core"
)

// DiskCache is a core.Cache that survives the process, which is the whole
// reason it exists beside MemoryCache.
//
// **A CLI runs once and exits.** MemoryCache is a process-lifetime map, so
// it can only ever serve a SECOND read inside one invocation — and the
// problem this cache is for is the opposite one: `issue link create` pays
// a round trip for the site's link-type list on EVERY invocation, for a
// list that changes when an admin edits project configuration and not
// otherwise. Nothing an in-process map does can help with that. The two
// implementations are not redundant; MemoryCache keeps the seam real for
// repeated reads within a run, and this one spans runs.
//
// **Entries expire, and the TTL is the caller's.** A cache over remote
// configuration is a bet that the configuration did not change, and how
// long that bet is good for depends on what is cached — so DiskCache
// stores the write time with each entry and the READER decides whether it
// is too old (see TTLCache). Baking one duration in here would make it a
// property of the storage rather than of the data.
//
// **Every failure is a miss, never an error.** A cache that can fail the
// command it was meant to speed up is worse than no cache: an unreadable
// file, a corrupt entry, a read-only home directory, all of them fall
// through to the network. Set's error IS returned, because the caller may
// reasonably want to say "I could not save this" — but every caller in
// this CLI logs and continues, because a cold cache is not a failure.
type DiskCache struct {
	// dir is where entries live, resolved once at construction.
	dir string
	// now is the clock, injected so a test can age an entry without
	// sleeping. Nil means time.Now.
	now func() time.Time
}

var _ core.Cache = (*DiskCache)(nil)

// NewDiskCache resolves the per-user cache directory and returns a cache
// rooted in it.
//
// It uses shell.CacheDir rather than os.UserCacheDir for the reason
// config.DefaultPath uses shell.ConfigDir: os.UserCacheDir honors
// XDG_CACHE_HOME only on Linux, so the same environment variable would
// move the directory on one machine and be ignored on another.
//
// The CACHE directory, not the config directory, and the distinction is
// load-bearing: everything written here is disposable derived data that
// the CLI can rebuild from the site. A user who deletes it loses nothing
// but a round trip, which is exactly the promise a cache directory makes
// and a config directory does not.
func NewDiskCache() (*DiskCache, error) {
	dir, err := shell.CacheDir("jira")
	if err != nil {
		return nil, err
	}
	return &DiskCache{dir: dir}, nil
}

// Get returns the bytes stored under key, along with when they were
// written. A miss, an unreadable file and a corrupt entry are all reported
// the same way, because the caller's response to all three is identical:
// go to the network.
func (c *DiskCache) Get(key string) ([]byte, time.Time, bool) {
	root, err := c.openRoot()
	if err != nil {
		return nil, time.Time{}, false
	}
	defer root.Close() //nolint:errcheck // Read-only descriptor cleanup does not change the cached value.
	name := filepath.Base(c.path(key))
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, time.Time{}, false
	}
	raw, err := root.ReadFile(name)
	if err != nil {
		return nil, time.Time{}, false
	}
	var entry diskEntry
	if err := json.Unmarshal(raw, &entry); err != nil || entry.Key != key {
		// A corrupt entry is a miss. Deleting it here was considered and
		// rejected: a read path that writes is a surprise, and the next
		// Set overwrites it anyway.
		return nil, time.Time{}, false
	}
	return entry.Value, entry.Written, true
}

// Set stores value under key, stamped with the current time.
//
// AtomicWrite publishes a complete entry from a temporary file in the same
// directory, cleans up any failed publication and syncs the file before rename.
// On Unix it also syncs the directory. A failed directory sync can return an
// error after publication; callers still treat cache storage as best effort.
func (c *DiskCache) Set(key string, value []byte) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	root, err := c.openRoot()
	if err != nil {
		return err
	}
	if err := root.Close(); err != nil {
		return err
	}
	raw, err := json.Marshal(diskEntry{Key: key, Written: c.clock(), Value: value})
	if err != nil {
		return err
	}
	// 0o600: a cache of one user's Jira configuration is not a secret, but
	// it is not other users' business either, and it costs nothing.
	return xos.AtomicWrite(c.path(key), raw, 0o600)
}

// Forget removes one entry, which is what an explicit `--refresh` does
// before re-reading. A missing entry is success: the caller asked for the
// key to be gone and it is.
func (c *DiskCache) Forget(key string) error {
	root, err := c.openRoot()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck // Descriptor cleanup cannot reverse an acknowledged removal.
	if err := root.Remove(filepath.Base(c.path(key))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// path maps a cache key to a file name.
//
// The key is HASHED rather than used directly, because callers key by
// things that are not safe file names — a site URL contains slashes and a
// colon, and on Windows a colon is illegal in a path at all. Hashing also
// keeps the name a fixed length, so a long key cannot exceed the
// filesystem's limit.
//
// SHA-256 and not a shorter hash: this is not security-sensitive, but a
// collision would serve one site's cached data to another, and the cost of
// the wider hash is nothing next to the file read that follows.
func (c *DiskCache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

func (c *DiskCache) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// diskEntry is one stored value and when it was written. The timestamp
// rides the entry rather than being taken from the file's mtime, which a
// backup tool, a sync client or a `cp -p` can rewrite without the content
// changing.
type diskEntry struct {
	Key     string    `json:"key,omitempty"`
	Written time.Time `json:"written"`
	Value   []byte    `json:"value"`
}
