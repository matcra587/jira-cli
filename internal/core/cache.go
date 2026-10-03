package core

import (
	"context"
	"errors"
	"time"
)

var ErrCacheManagementUnsupported = errors.New("cache does not support scoped management")

// CacheEntry identifies one disposable file in a selected cache namespace.
// ID is opaque storage identity; Key is the logical key when the entry is valid.
// Malformed entries remain visible so a clear can remove them without trusting
// their contents. SizeBytes counts the stored payload, excluding entry metadata.
type CacheEntry struct {
	ID        string
	Key       string
	Written   time.Time
	SizeBytes int64
	Malformed bool
}

// ManagedCache is the optional local lifecycle seam. Ordinary read-through
// consumers continue to need only Cache. Inventory failures retain inspected
// entries and return an error; they must never be presented as an empty cache.
type ManagedCache interface {
	Entries(ctx context.Context) ([]CacheEntry, error)
	// DeleteEntry reports false when the entry was already absent. A true
	// acknowledgement remains valid even when accompanied by an error.
	DeleteEntry(ctx context.Context, id string) (removed bool, err error)
}
